package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	bolt "go.etcd.io/bbolt"

	"hifi.local/manager-service/internal/audioquality"
)

type qualityResult struct {
	audioquality.Result
	SourceHash string `json:"source_hash"`
	CheckedAt  string `json:"checked_at"`
}

func qualityForFiles(results map[string]qualityResult, files []Track) map[string]qualityResult {
	var out map[string]qualityResult
	for _, file := range files {
		if result, ok := results[file.ID]; ok && result.SourceHash == file.Hash {
			if out == nil {
				out = map[string]qualityResult{}
			}
			out[file.ID] = result
		}
	}
	return out
}

func (a *app) qualityRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/drafts/{id}/quality", func(w http.ResponseWriter, r *http.Request) {
		var options struct{}
		if !decode(w, r, &options) {
			return
		}
		batch := r.PathValue("id")
		id, err := a.submitDraftTask(batch, "quality")
		jobResponse(w, id, err)
	})
}

func (a *app) reportQuality(done, total int, name string) {
	a.jobMu.Lock()
	defer a.jobMu.Unlock()
	for _, job := range a.jobs {
		if (job.Kind == "quality" || job.Kind == "prepare") && job.State == "running" {
			job.Done, job.Total, job.Message = done, total, "检测音源 · "+name
		}
	}
}

// Decode outside the library file mutex. Each short transaction updates only
// quality fields on the current draft and verifies its source snapshot first.
func (a *app) analyzeDraftQuality(id string) (resultErr error) {
	defer func() { resultErr = a.failDraftStages(id, "quality", resultErr) }()
	var files []Track
	if err := a.updateQuality(id, nil, func(current *Batch) error {
		if len(current.Files) == 0 {
			return errors.New("草稿为空")
		}
		files = current.Files
		current.Quality = qualityForFiles(current.Quality, files)
		current.QualityState, current.QualityError = "running", ""
		return nil
	}); err != nil {
		return err
	}
	var issues error
	for i, file := range files {
		if err := a.checkOperation(); err != nil {
			return err
		}
		a.reportQuality(i, len(files), filepath.Base(file.Path))
		result, err := a.analyzeDraftFile(file)
		if a.operationContext().Err() != nil {
			return a.operationContext().Err()
		}
		if err != nil {
			result = audioquality.Result{Status: "error", Message: err.Error()}
			if errors.Is(err, audioquality.ErrUnsupported) {
				result.Status = "unsupported"
			} else {
				issues = errors.Join(issues, fmt.Errorf("%s：%w", filepath.Base(file.Path), err))
			}
		}
		if err := a.updateQuality(id, files, func(current *Batch) error {
			if current.Quality == nil {
				current.Quality = map[string]qualityResult{}
			}
			current.Quality[file.ID] = qualityResult{Result: result, SourceHash: file.Hash, CheckedAt: now()}
			return nil
		}); err != nil {
			return err
		}
		a.reportQuality(i+1, len(files), filepath.Base(file.Path))
	}
	return errors.Join(issues, a.updateQuality(id, files, func(current *Batch) error {
		current.QualityState = "done"
		if issues != nil {
			current.QualityState, current.QualityError = "failed", issues.Error()
		}
		return nil
	}))
}

func (a *app) updateQuality(id string, expected []Track, apply func(*Batch) error) error {
	if err := a.checkOperation(); err != nil {
		return err
	}
	return a.db.Update(func(tx *bolt.Tx) error {
		b, err := getTx[Batch](tx, "batches", id)
		if err != nil {
			return err
		}
		if err := draftWritable(b); err != nil {
			return err
		}
		if expected != nil {
			if len(expected) != len(b.Files) || b.QualityState != "running" {
				return errors.New("草稿文件已变更，请重新检测")
			}
			for i, file := range b.Files {
				previous := expected[i]
				if file.ID != previous.ID || file.Path != previous.Path || file.Hash != previous.Hash {
					return errors.New("草稿文件已变更，请重新检测")
				}
			}
		}
		if err := apply(&b); err != nil {
			return err
		}
		return putRecord(tx.Bucket([]byte("batches")), id, b)
	})
}

func (a *app) analyzeDraftFile(track Track) (audioquality.Result, error) {
	if err := regular(a.data, track.Path); err != nil {
		return audioquality.Result{}, err
	}
	f, err := a.data.Open(track.Path)
	if err != nil {
		return audioquality.Result{}, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return audioquality.Result{}, err
	}
	result, err := audioquality.Analyze(a.operationContext(), f, track.Format)
	if err != nil {
		return audioquality.Result{}, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return audioquality.Result{}, err
	}
	hash, err := digest(operationReader{ctx: a.operationContext(), r: f})
	if err != nil {
		return audioquality.Result{}, err
	}
	after, err := f.Stat()
	if err != nil {
		return audioquality.Result{}, err
	}
	if hash != track.Hash || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return audioquality.Result{}, errors.New("上传文件已变化，请重新读取标签后再检测")
	}
	return result, nil
}
