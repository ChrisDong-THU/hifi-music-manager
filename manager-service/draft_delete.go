package main

import (
	"errors"
	"path"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// File ownership is independent of grouping. A child deletes only its own list.
func draftFileRoot(b Batch) (string, error) {
	owner := b.UploadOwner
	if owner == "" {
		owner = b.ID
	}
	if !hexID(owner, 32) {
		return "", errors.New("草稿目录标识无效")
	}
	return cleanRelative(uploadsPrefix + owner)
}

func (a *app) deleteBatchRecord(id string) error {
	return a.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("batches")).Delete([]byte(id)) })
}

// Intake can end before the first file is committed. Such a draft is still a
// user-visible resource and must have an explicit, safe terminal transition.
func (a *app) discardEmptyDraft(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkDraftWritable(id); err != nil {
		return err
	}
	b, err := get[Batch](a, "batches", id)
	if err != nil {
		return err
	}
	if len(b.Files) > 0 || len(b.Children) > 0 || len(b.Imported) > 0 || b.CoverHash != "" {
		return errors.New("草稿仍关联歌曲或资源，请选择歌曲后删除")
	}
	op, err := a.begin("draft-delete", "移除空草稿 · "+b.Name)
	if err != nil {
		return err
	}
	if err := a.deleteBatchRecord(id); err != nil {
		return a.fail(&op, err)
	}
	return a.finish(&op)
}

func (a *app) deleteDraftLocked(id string, ids []string) error {
	if err := a.checkOperation(); err != nil {
		return err
	}
	b, err := get[Batch](a, "batches", id)
	if err != nil {
		return err
	}
	if b.State != "ready" && b.State != "uploading" {
		return errors.New("只能删除尚未导入的剩余草稿文件")
	}
	if len(b.Children) > 0 {
		return errors.New("请分别选择分组后的草稿条目")
	}
	if err := a.checkDraftWritable(id); err != nil {
		return err
	}
	selected, err := selectedDraftFiles(b, ids)
	if err != nil {
		return err
	}
	root, err := draftFileRoot(b)
	if err != nil {
		return err
	}
	paths := map[string]bool{}
	for _, track := range b.Files {
		if !selected[track.ID] {
			continue
		}
		p, err := cleanRelative(track.Path)
		key := a.dataPathKey(p)
		if err != nil || p != track.Path || !strings.HasPrefix(p, root+"/") || paths[key] {
			return errors.New("草稿文件路径无效")
		}
		paths[key] = true
	}
	others, err := readAll[Batch](a, "batches")
	if err != nil {
		return err
	}
	for _, other := range others {
		if other.ID == b.ID || other.State == "grouped" || other.State == "imported" {
			continue
		}
		for _, track := range other.Files {
			if paths[a.dataPathKey(track.Path)] {
				return errors.New("文件仍被其他草稿使用，不能删除")
			}
		}
	}
	op, err := a.begin("draft-delete", "删除草稿 · "+b.Name)
	if err != nil {
		return err
	}
	for _, track := range b.Files {
		if selected[track.ID] {
			b.Cleanup = append(b.Cleanup, track.Path)
		}
	}
	b = draftWithoutFiles(b, selected)
	if len(b.Files) == 0 && b.CoverHash != "" {
		p, err := draftArtworkPath(b)
		if err != nil {
			return a.fail(&op, err)
		}
		b.Cleanup = append(b.Cleanup, p)
		b.CoverHash = ""
	}
	op.Affected = len(selected)
	if len(b.Files) == 0 {
		b.Candidates = nil
		b.State, b.Message = "deleted", "已确认删除，正在清理文件"
	} else {
		b.State, b.Message = "ready", "已删除所选歌曲，剩余歌曲待处理"
	}
	if err := a.put("batches", b.ID, b); err != nil {
		return a.fail(&op, err)
	}
	if err := a.cleanupBatchLocked(&b); err != nil {
		a.wakeMaintenance()
		return a.fail(&op, err)
	}
	return a.finish(&op)
}

func validCleanupPath(b Batch, p string) error {
	root, err := draftFileRoot(b)
	clean, e := cleanRelative(p)
	if err != nil || e != nil || clean != p || !strings.HasPrefix(p, root+"/") {
		return errors.New("上传清理路径无效")
	}
	// Artwork is hidden from upload APIs and uses a separate, strict namespace.
	if strings.Contains(p, "/.artwork/") {
		return errors.New("封面路径须通过专用校验")
	}
	return nil
}

func validArtworkCleanup(b Batch, p string) bool {
	dir, err := draftArtworkDirectory(b)
	return err == nil && path.Dir(p) == dir && hexID(path.Base(p), 64)
}
