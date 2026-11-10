package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	bolt "go.etcd.io/bbolt"
)

func walkAudio(ctx context.Context, root *os.Root, dir string, paths *[]string, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > 128 {
		return errors.New("曲库目录层级过深")
	}
	f, err := root.Open(dir)
	if err != nil {
		return err
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(e.Name(), ".") || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if err = walkAudio(ctx, root, p, paths, depth+1); err != nil {
				return err
			}
		} else if audioExtensions[strings.ToLower(filepath.Ext(e.Name()))] {
			*paths = append(*paths, filepath.ToSlash(p))
			if len(*paths) > 100000 {
				return errors.New("单个曲库最多十万首")
			}
		}
	}
	return nil
}
func (a *app) scan() error {
	return a.scanWithProgress(a.reportScanProgress)
}

func (a *app) scanWithProgress(report func(libraryStats, int, int)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	report(libraryStats{}, 0, 0)
	defer func() {
		a.jobMu.Lock()
		a.scanning = nil
		a.jobMu.Unlock()
	}()
	return a.scanLocked(report)
}
func (a *app) scanLocked(report func(libraryStats, int, int)) error {
	if err := a.checkOperation(); err != nil {
		return err
	}
	batches, err := readAll[Batch](a, "batches")
	if err != nil {
		return err
	}
	pendingPaths := map[string]bool{}
	issues := []string{}
	for i := range batches {
		if err := a.reconcileImport(&batches[i]); err != nil {
			name := batches[i].PendingImport.Result.Path
			pendingPaths[identityText(name)] = true
			issues = append(issues, filepath.Base(name)+"：上次导入尚未核验")
		}
	}
	paths := []string{}
	ctx := a.operationContext()
	if err := walkAudio(ctx, a.music, ".", &paths, 0); err != nil {
		return err
	}
	old := map[string]Track{}
	previous, err := a.readTracks()
	if err != nil {
		return err
	}
	for _, t := range previous {
		old[t.Path] = t
	}
	tracks := []Track{}
	stats := newStatsAccumulator()
	report(stats.stats, 0, len(paths))
	for index, p := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(pendingPaths) > 0 && pendingPaths[identityText(p)] {
			continue
		}
		var t Track
		var inspectErr error
		cached := false
		if previous, ok := old[p]; ok && previous.Modified != 0 {
			if info, err := a.music.Stat(p); err == nil && info.Size() == previous.Size && info.ModTime().UnixNano() == previous.Modified {
				t, cached = previous, true
			}
		}
		if !cached {
			t, inspectErr = inspectContext(ctx, a.music, p)
			if inspectErr == nil {
				if prev, ok := old[p]; ok {
					t.ID, t.Added = prev.ID, prev.Added
					if t.Hash == prev.Hash {
						t.AlbumArtworkFallback = prev.AlbumArtworkFallback
					}
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if inspectErr != nil && importReservation(a.music, p) {
			if err := a.checkOperation(); err != nil {
				return err
			}
			if err := a.music.Remove(p); err != nil {
				return err
			}
			continue
		}
		if inspectErr != nil {
			issues = append(issues, filepath.Base(p)+"："+inspectErr.Error())
		} else {
			tracks = append(tracks, t)
			stats.add(t)
		}
		report(stats.stats, index+1, len(paths))
	}
	if err := a.checkOperation(); err != nil {
		return err
	}
	err = a.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("tracks"))
		if _, err := b.NextSequence(); err != nil {
			return err
		}
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if err := c.Delete(); err != nil {
				return err
			}
		}
		for _, t := range tracks {
			if e := putRecord(b, t.ID, t); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := a.refreshDraftAudioInfoLocked(); err != nil {
		return err
	}
	if len(issues) > 0 {
		return fmt.Errorf("已索引 %d 首，%d 个文件未导入索引：%s", len(tracks), len(issues), strings.Join(issues[:min(3, len(issues))], "；"))
	}
	return nil
}
