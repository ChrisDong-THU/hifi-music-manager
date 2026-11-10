package main

import (
	"errors"
	"fmt"
	"path/filepath"
)

func (a *app) edit(ids []string, patch map[string]string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.editLocked(ids, patch, nil, fmt.Sprintf("修改 %d 首曲目的标签", len(ids)))
}
func (a *app) editLocked(ids []string, patch map[string]string, cover *albumCover, label string) error {
	before := []Track{}
	afterTags := []Tags{}
	for _, id := range ids {
		t, err := get[Track](a, "tracks", id)
		if err != nil {
			return err
		}
		if !t.Editable {
			return errors.New("选择中存在暂不支持编辑的格式")
		}
		if err = a.current(t); err != nil {
			return err
		}
		tags := t.Tags
		if len(patch) > 0 || cover == nil {
			tags, err = patchTags(t.Tags, patch)
			if err != nil {
				return err
			}
		}
		before = append(before, t)
		afterTags = append(afterTags, tags)
	}
	op, err := a.begin("edit", label)
	if err != nil {
		return err
	}
	for i, t := range before {
		// Prepare a short-lived replacement; never retain the original as a backup.
		tmp := filepath.ToSlash(filepath.Join(filepath.Dir(t.Path), ".hifi-edit-"+uid()+filepath.Ext(t.Path)))
		if err = a.checkOperation(); err != nil {
			return a.fail(&op, err)
		}
		ctx := a.operationContext()
		if err = stageTags(ctx, a.music, t.Path, tmp, afterTags[i], cover, patch); err != nil {
			return a.fail(&op, err)
		}
		updated, err := inspectContext(ctx, a.music, tmp)
		if err == nil && updated.AudioHash != t.AudioHash {
			err = errors.New("音频摘要发生变化，已拒绝写入")
		}
		if err != nil {
			_ = a.music.Remove(tmp)
			return a.fail(&op, err)
		}
		updated.ID = t.ID
		updated.Path = t.Path
		updated.Added = t.Added
		if cover == nil && albumIdentity(updated.Tags.Album, albumCredit(updated.Tags)) == albumIdentity(t.Tags.Album, albumCredit(t.Tags)) {
			updated.AlbumArtworkFallback = t.AlbumArtworkFallback
		}
		if err = a.current(t); err == nil {
			err = a.music.Rename(tmp, t.Path)
		}
		if err != nil {
			_ = a.music.Remove(tmp)
			return a.fail(&op, err)
		}
		op.Affected++
		if err := a.recordTrack(&op, &updated, ""); err != nil {
			return a.fail(&op, err)
		}
	}
	return a.finish(&op)
}
func (a *app) deleteTracks(ids []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.deleteTracksLocked(ids)
}

func (a *app) deleteTracksLocked(ids []string) error {
	tracks := []Track{}
	for _, id := range ids {
		t, err := get[Track](a, "tracks", id)
		if err != nil {
			return err
		}
		if err = a.current(t); err != nil {
			return err
		}
		tracks = append(tracks, t)
	}
	op, err := a.begin("delete", fmt.Sprintf("删除曲目 · %d 首", len(tracks)))
	if err != nil {
		return err
	}
	for _, t := range tracks {
		if err = a.checkOperation(); err == nil {
			err = a.current(t)
		}
		if err == nil {
			err = a.music.Remove(t.Path)
		}
		if err != nil {
			return a.fail(&op, err)
		}
		op.Affected++
		if err := a.recordTrack(&op, nil, t.ID); err != nil {
			return a.fail(&op, err)
		}
		removeEmptyParents(a.music, filepath.ToSlash(filepath.Dir(t.Path)), ".")
	}

	return a.finish(&op)
}
