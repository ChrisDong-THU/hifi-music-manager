package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

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

func (a *app) analyzeDraftQuality(id string) (resultErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer func() { resultErr = a.failDraftStages(id, "quality", resultErr) }()
	if err := a.checkDraftWritable(id); err != nil {
		return err
	}
	b, err := get[Batch](a, "batches", id)
	if err != nil {
		return err
	}
	if len(b.Files) == 0 {
		return errors.New("草稿为空")
	}
	b.Quality = qualityForFiles(b.Quality, b.Files)
	if b.Quality == nil {
		b.Quality = map[string]qualityResult{}
	}
	b.QualityState, b.QualityError = "running", ""
	if err := a.put("batches", b.ID, b); err != nil {
		return err
	}
	var issues error
	for i, file := range b.Files {
		if err := a.checkOperation(); err != nil {
			return err
		}
		a.reportQuality(i, len(b.Files), filepath.Base(file.Path))
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
		if err := a.checkOperation(); err != nil {
			return err
		}
		b.Quality[file.ID] = qualityResult{Result: result, SourceHash: file.Hash, CheckedAt: now()}
		if err := a.put("batches", b.ID, b); err != nil {
			return err
		}
		a.reportQuality(i+1, len(b.Files), filepath.Base(file.Path))
	}
	b.QualityState = "done"
	if issues != nil {
		b.QualityState, b.QualityError = "failed", issues.Error()
	}
	if err := a.put("batches", b.ID, b); err != nil {
		return errors.Join(issues, err)
	}
	return issues
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
