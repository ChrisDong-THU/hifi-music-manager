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

func (a *app) importLocked(id, policy string, ids []string) error {
	b, err := get[Batch](a, "batches", id)
	if err != nil {
		return err
	}
	if err := a.checkDraftWritable(id); err != nil {
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
	var cover *albumCover
	if saved != nil {
		cover, err = a.draftCover(b)
		if err != nil {
			return err
		}
	}
	known := map[string]Track{}
	existing, err := a.readTracks()
	if err != nil {
		return err
	}
	for _, track := range existing {
		known[track.AudioHash], known[track.Hash] = track, track
	}
	seen := map[string]bool{}
	importTags := make(map[string]Tags, len(selected))
	for i, t := range b.Files {
		if !selected[t.ID] {
			continue
		}
		if cover != nil && !t.Editable {
			return errors.New("此格式暂不支持写入封面")
		}
		tags := t.Tags
		if saved != nil {
			tags = saved[i]
		}
		importTags[t.ID] = tags
		if !t.Editable && tags != t.Tags {
			return errors.New("此目录包含只能原样导入的格式，请选择原始标签")
		}
		_, audioDuplicate := known[t.AudioHash]
		_, fileDuplicate := known[t.Hash]
		if policy == "reject" && (audioDuplicate || fileDuplicate || seen[t.AudioHash] || seen[t.Hash]) {
			return errors.New("检测到相同文件或音频：" + filepath.Base(t.Path) + "；请调整重复文件策略")
		}
		seen[t.AudioHash], seen[t.Hash] = true, true
		h, e := a.operationHash(a.data, t.Path)
		if e != nil || h != t.Hash {
			return errors.New("草稿源文件已变化，请重新读取后操作")
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
		b.State = "ready"
		b.Message = fmt.Sprintf("导入未完成，已保留 %d 首结果，可继续处理剩余文件", len(b.Imported))
		return a.fail(&op, errors.Join(err, a.put("batches", b.ID, b)))
	}
	// Consume one source per committed result. Retries work only on remaining
	// files; previously published music is never undone after a later failure.
	for len(selected) > 0 {
		index := 0
		for !selected[b.Files[index].ID] {
			index++
		}
		source := b.Files[index]
		tags := importTags[source.ID]
		result, duplicate := known[source.AudioHash]
		if !duplicate {
			result, duplicate = known[source.Hash]
		}
		skipped := policy == "skip" && duplicate
		if !skipped {
			result, err = a.publishImport(source, tags, cover)
			if err != nil {
				return fail(err)
			}
			op.Affected++
		} else {
			op.Skipped++
		}
		next := draftWithoutFiles(b, map[string]bool{source.ID: true})
		next.Cleanup = append(append([]string(nil), b.Cleanup...), source.Path)
		next.Imported = append(append([]string(nil), b.Imported...), result.ID)
		if len(next.Files) == 0 {
			next.State, next.Message = "imported", "已导入曲库，上传源文件已清理"
			if next.CoverHash != "" {
				p, e := draftArtworkPath(next)
				if e != nil {
					return fail(e)
				}
				next.Cleanup = append(next.Cleanup, p)
				next.CoverHash = ""
			}
			next.Candidates = nil
		} else if len(selected) == 1 {
			next.State, next.Message = "ready", "已导入所选歌曲，剩余歌曲待处理"
		}
		err = a.db.Update(func(tx *bolt.Tx) error {
			if !skipped {
				if err := storeTrackTx(tx, result); err != nil {
					return err
				}
			}
			raw, err := marshal(next)
			if err != nil {
				return err
			}
			if err = tx.Bucket([]byte("batches")).Put([]byte(next.ID), raw); err != nil {
				return err
			}
			return storeOperationTx(tx, &op)
		})
		if err != nil {
			return fail(err)
		}
		b = next
		delete(selected, source.ID)
		known[source.AudioHash], known[source.Hash] = result, result
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

// Publish a complete, validated file through a unique temporary path. The final
// name is reserved exclusively and never overwrites an existing music file.
func (a *app) publishImport(source Track, tags Tags, cover *albumCover) (Track, error) {
	dst := destination(source, tags)
	if _, err := a.music.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return Track{}, err
		}
		dst = strings.TrimSuffix(dst, filepath.Ext(dst)) + " (" + uid()[:8] + ")" + filepath.Ext(dst)
	}
	if err := ensureDir(a.music, filepath.Dir(dst)); err != nil {
		return Track{}, err
	}
	tmp := filepath.ToSlash(filepath.Join(filepath.Dir(dst), ".hifi-import-"+uid()+filepath.Ext(dst)))
	defer a.music.Remove(tmp)
	if err := a.copyOperationFile(a.data, source.Path, a.music, tmp); err != nil {
		return Track{}, err
	}
	var err error
	if cover != nil || tags != source.Tags {
		err = writeTags(a.music, tmp, tags, cover, tagDiff(source.Tags, tags))
	}
	if err != nil {
		return Track{}, err
	}
	result, err := inspectContext(a.operationContext(), a.music, tmp)
	if err != nil {
		return Track{}, err
	}
	if result.AudioHash != source.AudioHash {
		return Track{}, errors.New("音频数据校验失败")
	}
	if err := a.checkOperation(); err != nil {
		return Track{}, err
	}
	marker := []byte("hifi-reservation:" + uid())
	out, err := a.music.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Track{}, err
	}
	published := false
	defer func() {
		if published {
			return
		}
		if hash, err := fileHash(a.music, dst); err == nil && hash == library.Hash(marker) {
			_ = a.music.Remove(dst)
		}
	}()
	_, err = out.Write(marker)
	if err == nil {
		err = out.Sync()
	}
	err = errors.Join(err, out.Close())
	if err != nil {
		return Track{}, err
	}
	if hash, err := a.operationHash(a.music, dst); err != nil || hash != library.Hash(marker) {
		return Track{}, errors.New("导入位置已被其他程序修改")
	}
	if err := a.music.Rename(tmp, dst); err != nil {
		return Track{}, err
	}
	published = true
	result.Path = dst
	return result, nil
}

func importReservation(root *os.Root, p string) bool {
	info, err := root.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() != 49 {
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
