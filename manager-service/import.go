package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	bolt "go.etcd.io/bbolt"
	library "hifi.local/library"
)

const importReservationSize = 49

func (a *app) importLocked(id, policy string, ids []string) error {
	b, err := get[Batch](a, "batches", id)
	if err != nil {
		return err
	}
	if err := a.checkDraftWritable(b); err != nil {
		return err
	}
	if len(b.Files) == 0 {
		return errors.New("草稿为空")
	}
	selected, err := selectedDraftFiles(b, ids)
	if err != nil {
		return err
	}
	if policy != "reject" && policy != "skip" && policy != "keep" {
		return errors.New("重复文件策略无效")
	}
	saved := savedDraftTags(b)
	if saved == nil && b.ChosenCandidate != "" && b.ChosenCandidate != "original" {
		return errors.New("请先选择有效的导入预览")
	}
	var draftArtwork *albumCover
	if b.CoverHash != "" && saved != nil && oneDraftArtworkRelease(saved, b.AlbumMatch) {
		draftArtwork, err = a.draftCover(b)
		if err != nil {
			return err
		}
	}
	index, err := a.readLibraryIndex()
	if err != nil {
		return err
	}
	seen := contentIndex{}
	placements := importPlacementPlan{catalog: &index.catalog, root: a.music,
		new: map[string]string{}, years: map[string]map[string]bool{}}
	type entry struct {
		source      Track
		tags        Tags
		destination string
		release     *albumRelease
		skip        bool
		duplicate   int
		earlier     int
		result      Track
	}
	plan := make([]entry, 0, len(selected))
	for i, track := range b.Files {
		if !selected[track.ID] {
			continue
		}
		tags := track.Tags
		if saved != nil {
			tags = saved[i]
		}
		position, earlier := index.hashes.find(track), seen.find(track)
		duplicate := position >= 0 || earlier >= 0
		if policy == "reject" && duplicate {
			return errors.New("检测到相同文件或音频：" + filepath.Base(track.Path) + "；请调整重复文件策略")
		}
		if policy != "skip" || !duplicate {
			if draftArtwork != nil && !track.Editable {
				return errors.New("此格式暂不支持写入封面")
			}
			if !track.Editable && tags != track.Tags {
				return errors.New("此目录包含只能原样导入的格式，请选择原始标签")
			}
			placements.noteYear(tags)
		}
		seen.add(track, len(plan))
		h, e := a.operationHash(a.data, track.Path)
		if e != nil || h != track.Hash {
			return errors.New("草稿源文件已变化，请重新读取后操作")
		}
		plan = append(plan, entry{source: track, tags: tags, skip: policy == "skip" && duplicate, duplicate: position, earlier: earlier})
	}
	// Only one existing release can be selected by an explicit draft match.
	// Build its duplicate lookup lazily, only if a selected track needs it.
	var releaseHashes contentIndex
	for i := range plan {
		item := &plan[i]
		if !item.skip {
			item.tags, item.destination, item.release, err = placements.place(item.source, item.tags, b.AlbumMatch)
			if err != nil {
				return err
			}
			continue
		}
		if b.AlbumMatch == "" {
			continue
		}
		item.release, _, err = placements.match(item.tags, b.AlbumMatch)
		if err != nil {
			return err
		}
		if releaseHashes == nil && item.release != nil {
			releaseHashes = contentIndex{}
			for _, position := range item.release.members {
				releaseHashes.add(index.tracks[position], position)
			}
		}
		item.duplicate = releaseHashes.find(item.source)
		if item.duplicate < 0 && (item.earlier < 0 || plan[item.earlier].release != item.release) {
			return errors.New("相同音频只存在于其他专辑位置；要导入所选版本，请将重复策略设为「保留副本」")
		}
	}
	op, err := a.begin("import", "导入 · "+b.Name)
	if err != nil {
		return err
	}
	b.State = "importing"
	if err := a.put("batches", b.ID, b); err != nil {
		return a.fail(&op, err)
	}
	fail := func(err error) error {
		if b.PendingImport != nil {
			a.wakeMaintenance()
			return a.fail(&op, err)
		}
		b.State = "ready"
		b.Message = fmt.Sprintf("导入未完成，已保留 %d 首结果，可继续处理剩余文件", len(b.Imported))
		return a.fail(&op, errors.Join(err, a.put("batches", b.ID, b)))
	}
	// Consume one source per committed result. Retries work only on remaining
	// files; previously published music is never undone after a later failure.
	var artworkRelease *albumRelease
	var artwork *albumCover
	var useExistingArtwork bool
	for i := range plan {
		item := &plan[i]
		source := item.source
		a.reportImport(id, i, len(plan), filepath.Base(source.Path))
		var result Track
		var next Batch
		var published *Track
		nextOp := op
		last := i == len(plan)-1
		skipped := item.skip
		if !skipped {
			if item.release != artworkRelease {
				artworkRelease = item.release
				artwork = draftArtwork
				_, existing := a.findReleaseArtwork(a.operationContext(), index, item.release)
				useExistingArtwork = existing != nil
				if existing != nil {
					// Keep existing GIF/WebP artwork usable through the album endpoint.
					// Only validated JPEG/PNG can be embedded into incoming audio.
					artwork, _ = parseAlbumCover(existing.Data)
				}
			}
			cover := draftArtwork
			if item.release != nil {
				cover = artwork
			}
			if !source.Editable {
				cover = nil
			}
			result, err = a.publishImport(source, item.tags, cover, item.destination, useExistingArtwork, func(result Track, marker string) error {
				var err error
				next, err = consumeImport(b, source, result, false, last)
				if err != nil {
					return err
				}
				pending := b
				pending.PendingImport = &importPublication{SourceID: source.ID, OperationID: op.ID, Reservation: marker, Result: result}
				if err := a.put("batches", pending.ID, pending); err != nil {
					return err
				}
				b = pending
				return nil
			})
			if err != nil {
				return fail(err)
			}
			published = &result
			nextOp.Affected++
		} else {
			if item.duplicate >= 0 {
				result = index.tracks[item.duplicate]
			} else if item.earlier >= 0 {
				result = plan[item.earlier].result
			} else {
				return fail(errors.New("找不到重复曲目，请重新扫描曲库"))
			}
			if err := a.current(result); err != nil {
				return fail(errors.New("重复曲目已从曲库移除或修改，请重新扫描；草稿文件已保留"))
			}
			next, err = consumeImport(b, source, result, true, last)
			if err != nil {
				return fail(err)
			}
			nextOp.Skipped++
		}
		err = a.db.Update(func(tx *bolt.Tx) error {
			return commitImportTx(tx, next, published, &nextOp)
		})
		if err != nil {
			return fail(err)
		}
		b, op = next, nextOp
		item.result = result
		a.reportImport(id, i+1, len(plan), filepath.Base(source.Path))
		if err := a.cleanupBatchLocked(&b); err != nil {
			if len(b.Files) > 0 {
				b.State = "ready"
			}
			b.Message = "结果已保存，上传源文件清理将自动重试"
			persistErr := a.put("batches", b.ID, b)
			a.wakeMaintenance()
			return a.fail(&op, errors.Join(fmt.Errorf("音乐已保存，上传源文件清理将自动重试：%w", err), persistErr))
		}
	}
	return a.finish(&op)
}

func (a *app) reportImport(batch string, done, total int, name string) {
	a.jobMu.Lock()
	defer a.jobMu.Unlock()
	for _, job := range a.jobs {
		if job.BatchID == batch && job.Kind == "import" && job.State == "running" {
			job.Done, job.Total, job.Message = done, total, "导入曲目 · "+name
			return
		}
	}
}

// Stage one complete file in the destination directory so DAV readers only
// see the published audio. Publication never overwrites an existing name.
func (a *app) publishImport(source Track, tags Tags, cover *albumCover, dst string, fallbackArtwork bool, prepare func(Track, string) error) (Track, error) {
	if err := a.checkOperation(); err != nil {
		return Track{}, err
	}
	var created []string
	published := false
	defer func() {
		if !published {
			for i := len(created) - 1; i >= 0; i-- {
				if info, err := a.music.Lstat(created[i]); err == nil && info.IsDir() {
					_ = a.music.Remove(created[i])
				}
			}
		}
	}()
	if err := ensureDirectories(a.music, filepath.Dir(dst), &created); err != nil {
		return Track{}, err
	}
	ext := filepath.Ext(dst)
	tmp := filepath.ToSlash(filepath.Join(filepath.Dir(dst), ".hifi-import-"+uid()+ext))
	ctx := a.operationContext()
	rewrite := cover != nil || tags != source.Tags
	var err error
	if rewrite {
		err = stageTagsBetween(ctx, a.data, source.Path, a.music, tmp, tags, cover, tagDiff(source.Tags, tags))
	} else {
		err = copyFileContext(ctx, a.data, source.Path, a.music, tmp)
	}
	if err != nil {
		return Track{}, err
	}
	defer a.music.Remove(tmp)
	result, err := inspectContext(ctx, a.music, tmp)
	if err != nil {
		return Track{}, err
	}
	result.AlbumArtworkFallback = fallbackArtwork
	if result.AudioHash != source.AudioHash || !rewrite && result.Hash != source.Hash {
		return Track{}, errors.New("音频数据校验失败")
	}
	if albumIdentity(result.Tags.Album, albumCredit(result.Tags)) != albumIdentity(tags.Album, albumCredit(tags)) {
		return Track{}, errors.New("写入后的专辑标签与导入目标不一致，请重新读取草稿后重试")
	}
	if err := a.checkOperation(); err != nil {
		return Track{}, err
	}
	for suffix := 1; suffix <= 1000; suffix++ {
		candidate := dst
		if suffix > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(dst, ext), suffix, ext)
		}
		result.Path = candidate
		err := a.publishReserved(a.music, tmp, candidate, func(marker string) error {
			return prepare(result, marker)
		})
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return Track{}, err
		}
		published = true
		return result, nil
	}
	return Track{}, errors.New("专辑目录中存在过多同名曲目")
}

func (a *app) publishStaged(root *os.Root, tmp, dst string) error {
	if err := a.checkOperation(); err != nil {
		return err
	}
	if err := root.Link(tmp, dst); err == nil || errors.Is(err, os.ErrExist) {
		return err
	}
	return a.publishReserved(root, tmp, dst, nil)
}

// The reservation belongs to this operation before any recovery record is
// committed. Scan and WebDAV omit it until the full file is published.
func (a *app) publishReserved(root *os.Root, tmp, dst string, prepare func(string) error) error {
	if err := a.checkOperation(); err != nil {
		return err
	}
	if root == a.music {
		key := identityText(filepath.ToSlash(dst))
		a.musicPublishing.Store(key, true)
		defer a.musicPublishing.Delete(key)
	}
	marker := []byte("hifi-reservation:" + uid())
	out, err := root.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	reserved, _ := out.Stat()
	published := false
	written := 0
	defer func() {
		if published {
			return
		}
		info, err := root.Lstat(dst)
		if err != nil || reserved == nil || !os.SameFile(reserved, info) || info.Size() != int64(written) {
			return
		}
		if hash, err := fileHash(root, dst); err == nil && hash == library.Hash(marker[:written]) {
			_ = root.Remove(dst)
		}
	}()
	written, err = out.Write(marker)
	if err == nil {
		err = out.Sync()
	}
	err = errors.Join(err, out.Close())
	if err != nil {
		return err
	}
	if prepare != nil {
		if err := prepare(string(marker)); err != nil {
			return err
		}
	}
	if hash, err := a.operationHash(root, dst); err != nil || hash != library.Hash(marker) {
		return errors.New("导入位置已被其他程序修改")
	}
	if err := root.Rename(tmp, dst); err != nil {
		return err
	}
	published = true
	return nil
}

func importReservation(root *os.Root, p string) bool {
	info, err := root.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() != importReservationSize {
		return false
	}
	f, err := root.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 50))
	return err == nil && strings.HasPrefix(string(raw), "hifi-reservation:") && hexID(strings.TrimPrefix(string(raw), "hifi-reservation:"), 32)
}
