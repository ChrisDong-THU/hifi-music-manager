package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"time"
)

// Cleanup only releases files whose forward operation already committed.
// It has no expiry windows, backup snapshots, dependency graph, or restore path.
func (a *app) cleanupBatchLocked(b *Batch) error {
	if len(b.Cleanup) == 0 {
		if b.State == "deleted" || b.State == "imported" {
			return a.deleteBatchRecord(b.ID)
		}
		return nil
	}
	check := a.checkOperation
	for _, p := range b.Cleanup {
		if err := validCleanupPath(*b, p); err != nil && !validArtworkCleanup(*b, p) {
			return err
		}
	}
	for _, p := range b.Cleanup {
		if err := removeCleanupFile(a.data, p, check); err != nil {
			return fmt.Errorf("清理上传文件失败，将重试：%w", err)
		}
		root, _ := draftFileRoot(*b)
		removeEmptyParents(a.data, path.Dir(p), root)
	}
	next := *b
	next.Cleanup = nil
	if b.State == "deleted" || b.State == "imported" {
		return a.deleteBatchRecord(b.ID)
	}
	if err := a.put("batches", next.ID, next); err != nil {
		return err
	}
	*b = next
	return nil
}

func (a *app) startCleanup(ctx context.Context) {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		sweep := true
		nextSweep := time.Now().Add(time.Hour)
		for {
			if err := a.cleanupFiles(ctx, sweep); err != nil && ctx.Err() == nil {
				log.Printf("文件清理未完成，将重试: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep = !time.Now().Before(nextSweep)
				if sweep {
					nextSweep = time.Now().Add(time.Hour)
				}
			case <-a.maintenanceWake:
				sweep = false
			}
		}
	}()
}

func (a *app) cleanupFiles(ctx context.Context, sweep bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return a.checkStorage()
	}
	if err := check(); err != nil {
		return err
	}
	batches, err := readAll[Batch](a, "batches")
	if err != nil {
		return err
	}
	protected := map[string]bool{}
	a.uploadStages.Range(func(key, _ any) bool {
		protected[key.(string)] = true
		return true
	})
	existing := map[string]bool{}
	var issues error
	for i := range batches {
		b := &batches[i]
		if err := check(); err != nil {
			return errors.Join(issues, err)
		}
		if err := a.reconcileImport(b); err != nil {
			issues = errors.Join(issues, err)
		} else if err := a.cleanupBatchLocked(b); err != nil {
			issues = errors.Join(issues, err)
		}
		if b.State != "deleted" && b.State != "imported" {
			existing[b.ID] = true
		}
		if b.State == "grouped" || b.State == "imported" || b.State == "deleted" {
			continue
		}
		for _, file := range b.Files {
			protected[a.dataPathKey(file.Path)] = true
		}
		if b.CoverHash != "" {
			p, e := draftArtworkPath(*b)
			if e != nil {
				return errors.Join(issues, e)
			}
			protected[a.dataPathKey(p)] = true
		}
	}
	// Grouping containers own no files after splitting and need no tombstones
	// once their last child has been deleted or fully imported.
	for _, b := range batches {
		if b.State != "grouped" {
			continue
		}
		alive := false
		for _, id := range b.Children {
			alive = alive || existing[id]
		}
		if !alive {
			issues = errors.Join(issues, a.deleteBatchRecord(b.ID))
		}
	}
	if !sweep {
		return issues
	}
	now := time.Now()
	issues = errors.Join(issues, pruneUploads(a.data, protected, now, check, a.dataPathKey))
	issues = errors.Join(issues, pruneTemporaryFiles(a.music, ".", now, true, check))
	issues = errors.Join(issues, pruneCaseProbes(a.data, now, check))
	issues = errors.Join(issues, pruneArtistVersions(a.artists, now, check))
	return issues
}
