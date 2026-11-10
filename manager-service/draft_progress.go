package main

import (
	"encoding/json"
	"errors"

	bolt "go.etcd.io/bbolt"
)

var errDraftBusy = errors.New("该条目正在处理，请等待完成")

func (a *app) draftTaskActive(b Batch) bool {
	a.jobMu.Lock()
	defer a.jobMu.Unlock()
	for _, j := range a.jobs {
		if (j.State == "running" || j.State == "queued") && (j.BatchID == b.ID || (b.ParentID != "" && j.BatchID == b.ParentID)) {
			return true
		}
	}
	return false
}

func (a *app) checkDraftIdle(id string) error {
	b, err := get[Batch](a, "batches", id)
	if err != nil {
		return err
	}
	if a.draftTaskActive(b) {
		return errDraftBusy
	}
	return nil
}

// A failed preparation can stop before reading, or leave grouped children waiting
// for a stage that can no longer run. Settle only incomplete stages, never discard
// completed measurements or recreate a draft that has already been removed.
func (a *app) failDraftStages(id, kind string, cause error) error {
	if cause == nil || a.operationContext().Err() != nil || a.checkStorage() != nil {
		return cause
	}
	err := a.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("batches"))
		ids := []string{id}
		for i := 0; i < len(ids); i++ {
			raw := bucket.Get([]byte(ids[i]))
			if raw == nil {
				continue
			}
			var b Batch
			if err := json.Unmarshal(raw, &b); err != nil {
				return err
			}
			if i == 0 && kind == "prepare" {
				ids = append(ids, b.Children...)
			}
			changed := false
			fail := func(state, message *string) {
				if *state == "queued" || *state == "running" {
					*state, *message, changed = "failed", cause.Error(), true
				}
			}
			if kind == "preview" || kind == "prepare" {
				fail(&b.ReadState, &b.ReadError)
			}
			if kind == "quality" || kind == "prepare" {
				fail(&b.QualityState, &b.QualityError)
			}
			if changed {
				if err := putRecord(bucket, b.ID, b); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return errors.Join(cause, err)
}

// Stage updates merge into the current record, rather than overwriting a
// worker's candidates or measurements with a request's older snapshot.
func (a *app) setDraftStage(id, kind, state, message string) error {
	return a.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("batches"))
		raw := bucket.Get([]byte(id))
		if raw == nil {
			return errors.New("草稿不存在")
		}
		var b Batch
		if err := json.Unmarshal(raw, &b); err != nil {
			return err
		}
		if kind == "preview" || kind == "prepare" {
			b.ReadState, b.ReadError = state, message
		}
		if kind == "quality" || kind == "prepare" {
			b.QualityState, b.QualityError = state, message
		}
		return putRecord(bucket, id, b)
	})
}

func (a *app) submitDraftTask(id, kind string) (string, error) {
	// This short request lock never waits for the long-running file mutex.
	// The worker still serializes all actual reads and writes through a.mu.
	a.draftSubmitMu.Lock()
	defer a.draftSubmitMu.Unlock()
	b, err := get[Batch](a, "batches", id)
	if err != nil {
		return "", err
	}
	if err := a.checkDraftWritable(b); err != nil {
		return "", err
	}
	if len(b.Files) == 0 {
		return "", errors.New("草稿为空")
	}
	if a.draftTaskActive(b) {
		return "", errDraftBusy
	}
	if err := a.setDraftStage(id, kind, "queued", ""); err != nil {
		return "", err
	}
	jobID, err := a.submitForBatch(kind, id, func() error {
		switch kind {
		case "prepare":
			return a.prepare(id)
		case "preview":
			return a.localPreview(id)
		case "quality":
			return a.analyzeDraftQuality(id)
		default:
			return errors.New("不支持的草稿任务")
		}
	})
	if err != nil {
		return "", errors.Join(err, a.setDraftStage(id, kind, "failed", err.Error()))
	}
	return jobID, nil
}
