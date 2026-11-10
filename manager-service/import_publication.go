package main

import (
	"errors"
	"os"
	"strings"

	bolt "go.etcd.io/bbolt"
	library "hifi.local/library"
)

// A single in-flight track links an exclusive destination to its draft source.
// It is cleared in the same transaction that records the published track.
type importPublication struct {
	SourceID    string `json:"source_id"`
	OperationID string `json:"operation_id"`
	Reservation string `json:"reservation"`
	Result      Track  `json:"result"`
}

func consumeImport(b Batch, source, result Track, skipped, last bool) (Batch, error) {
	if err := validCleanupPath(b, source.Path); err != nil {
		return b, err
	}
	next := draftWithoutFiles(b, map[string]bool{source.ID: true})
	next.PendingImport = nil
	if b.AlbumMatch == "new" && !skipped && len(next.Files) > 0 {
		identity := albumIdentity(result.Tags.Album, albumCredit(result.Tags))
		next.AlbumMatch = albumPlacementKey(identity, albumLocation(result.Path, result.Tags))
	}
	next.Cleanup = append(append([]string(nil), b.Cleanup...), source.Path)
	next.Imported = append(append([]string(nil), b.Imported...), result.ID)
	if next.CoverHash != "" && (len(next.Files) == 0 || !oneDraftArtworkRelease(savedDraftTags(b), b.AlbumMatch)) {
		p, err := draftArtworkPath(next)
		if err != nil {
			return b, err
		}
		next.Cleanup = append(next.Cleanup, p)
		next.CoverHash = ""
	}
	if len(next.Files) == 0 {
		next.State, next.Message = "imported", "已导入曲库，上传源文件已清理"
		next.Candidates = nil
	} else if last {
		next.State, next.Message = "ready", "已导入所选歌曲，剩余歌曲待处理"
	}
	return next, nil
}

func commitImportTx(tx *bolt.Tx, b Batch, track *Track, op *Operation) error {
	if track != nil {
		if err := storeTrackTx(tx, *track); err != nil {
			return err
		}
	}
	if err := putRecord(tx.Bucket([]byte("batches")), b.ID, b); err != nil {
		return err
	}
	if op != nil {
		return storeOperationTx(tx, op)
	}
	return nil
}

// Recover only a finished, byte-for-byte verified publication. Never copy from
// the draft, overwrite a destination, or discard a source changed externally.
func (a *app) reconcileImport(b *Batch) error {
	p := b.PendingImport
	if p == nil {
		return nil
	}
	if err := a.checkOperation(); err != nil {
		return err
	}
	result := p.Result
	markerID, markerOK := strings.CutPrefix(p.Reservation, "hifi-reservation:")
	if name, err := cleanRelative(result.Path); err != nil || name != result.Path ||
		!hexID(result.Hash, 64) || !markerOK || !hexID(markerID, 32) {
		return errors.New("无法核验上次导入的目标记录")
	}
	next := *b
	next.PendingImport = nil
	next.State, next.Message = "ready", "上次导入中断，草稿原件已保留"
	var track *Track
	var opID string
	hash, err := a.operationHash(a.music, result.Path)
	switch {
	case errors.Is(err, os.ErrNotExist), errors.Is(err, errUnsafeFilePath):
	case err != nil:
		return err
	case hash == library.Hash([]byte(p.Reservation)):
		if err := a.music.Remove(result.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	case hash == result.Hash:
		for _, source := range b.Files {
			if source.ID != p.SourceID {
				continue
			}
			currentHash, err := a.operationHash(a.data, source.Path)
			if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errUnsafeFilePath) {
				return err
			}
			if errors.Is(err, errUnsafeFilePath) || (err == nil && currentHash != source.Hash) {
				next.Message = "已发布的音乐保留；草稿原件已变化，请重新读取后处理"
				break
			}
			next, err = consumeImport(*b, source, result, false, true)
			if err != nil {
				return err
			}
			track, opID = &result, p.OperationID
			break
		}
	}
	if err := a.db.Update(func(tx *bolt.Tx) error {
		var op *Operation
		if opID != "" {
			current, err := getTx[Operation](tx, "operations", opID)
			if err == nil {
				current.Affected++
				op = &current
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return commitImportTx(tx, next, track, op)
	}); err != nil {
		return err
	}
	*b = next
	return nil
}
