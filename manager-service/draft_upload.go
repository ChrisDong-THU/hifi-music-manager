package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
)

const (
	maxDraftFiles    = 300
	maxUploadBytes   = 2 << 30
	maxActiveUploads = 4
	uploadStageName  = ".hifi-upload-"
)

var (
	errUploadConflict = errors.New("该批次已存在不同内容的同名文件")
	errUploadLimit    = errors.New("每个批次最多 300 首")
	errUploadBody     = errors.New("上传未完成或超过单文件 2 GiB 限制")
)

type uploadTarget struct {
	path, stage string
	file        *os.File
}

func (a *app) upload(w http.ResponseWriter, r *http.Request) {
	relative, err := cleanRelative(r.URL.Query().Get("path"))
	if err != nil {
		problem(w, http.StatusBadRequest, err)
		return
	}
	ext := strings.ToLower(path.Ext(relative))
	if !audioExtensions[ext] {
		problem(w, http.StatusBadRequest, errors.New("请选择 FLAC、MP3、M4A、OGG、OPUS、WAV 或 AIFF 文件"))
		return
	}
	if r.ContentLength > maxUploadBytes {
		problem(w, http.StatusBadRequest, errUploadBody)
		return
	}
	if a.activeUploads.Add(1) > maxActiveUploads {
		a.activeUploads.Add(-1)
		problem(w, http.StatusTooManyRequests, errors.New("同时上传的文件过多，请稍后重试"))
		return
	}
	defer a.activeUploads.Add(-1)
	id, ctx := r.PathValue("id"), r.Context()
	if err := a.checkDraftIdle(id); err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		problem(w, status, err)
		return
	}
	target, status, err := a.startUpload(ctx, id, relative)
	if err != nil {
		problem(w, status, err)
		return
	}
	body := operationReader{ctx: ctx, r: http.MaxBytesReader(w, r.Body, maxUploadBytes)}
	var received Track
	if target.file == nil {
		// For a retry of an already committed path, hash the body without
		// taking disk space for another copy of a potentially large file.
		hash := sha256.New()
		received.Size, err = io.Copy(hash, body)
		received.Hash = hex.EncodeToString(hash.Sum(nil))
	} else {
		defer a.releaseUploadStage(target.stage)
		_, err = io.Copy(target.file, body)
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = a.checkOperation()
		}
		if err == nil {
			err = target.file.Sync()
		}
		err = errors.Join(err, target.file.Close())
		if err == nil {
			received, err = inspectContext(ctx, a.data, target.stage)
			if err != nil {
				problem(w, http.StatusBadRequest, fmt.Errorf("文件校验失败：%w", err))
				return
			}
		}
	}
	if err != nil {
		problem(w, http.StatusBadRequest, errUploadBody)
		return
	}
	result, retry, status, err := a.publishUpload(ctx, id, relative, target, received)
	if err == nil && retry {
		result, status, err = a.verifyUploadRetry(ctx, id, relative, target.path, received, result)
	}
	if err != nil {
		problem(w, status, err)
		return
	}
	respond(w, result)
}

// Called under a.mu at each boundary where an upload claims or publishes a
// file. The fresh batch decides whether this path is still writable.
func (a *app) uploadBatch(ctx context.Context, id, relative string) (Batch, string, int, error) {
	if err := ctx.Err(); err != nil {
		return Batch{}, "", http.StatusBadRequest, errUploadBody
	}
	if err := a.checkOperation(); err != nil {
		return Batch{}, "", http.StatusServiceUnavailable, err
	}
	b, err := get[Batch](a, "batches", id)
	if errors.Is(err, os.ErrNotExist) {
		return Batch{}, "", http.StatusNotFound, errors.New("草稿不存在")
	}
	if err != nil {
		return Batch{}, "", http.StatusInternalServerError, err
	}
	if err := draftWritable(b); err != nil {
		return Batch{}, "", http.StatusConflict, err
	}
	if b.ParentID != "" || len(b.Children) > 0 {
		return Batch{}, "", http.StatusConflict, errors.New("分组后的草稿不能追加文件，请新建上传")
	}
	if a.draftTaskActive(b) {
		return Batch{}, "", http.StatusConflict, errDraftBusy
	}
	root, err := draftFileRoot(b)
	if err != nil {
		return Batch{}, "", http.StatusConflict, err
	}
	return b, path.Join(root, relative), 0, nil
}

func (a *app) uploadedTrack(b Batch, destination string) (Track, bool) {
	key := a.dataPathKey(destination)
	for _, track := range b.Files {
		if a.dataPathKey(track.Path) == key {
			return track, true
		}
	}
	return Track{}, false
}

func draftAppendStatus(b Batch) (int, error) {
	if len(b.Files) >= maxDraftFiles {
		return http.StatusBadRequest, errUploadLimit
	}
	if b.ChosenCandidate == "manual" || b.CoverHash != "" || b.AlbumMatch != "" || len(b.Imported) > 0 {
		return http.StatusConflict, errors.New("草稿已有编辑或导入结果，请新建上传")
	}
	return 0, nil
}

func (a *app) startUpload(ctx context.Context, id, relative string) (uploadTarget, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, dst, status, err := a.uploadBatch(ctx, id, relative)
	if err != nil {
		return uploadTarget{}, status, err
	}
	if _, exists := a.uploadedTrack(b, dst); exists {
		return uploadTarget{path: dst}, 0, nil
	}
	if status, err := draftAppendStatus(b); err != nil {
		return uploadTarget{}, status, err
	}
	dir := path.Dir(dst)
	if err := ensureDir(a.data, dir); err != nil {
		return uploadTarget{}, http.StatusBadRequest, err
	}
	if _, err := a.data.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		return uploadTarget{}, http.StatusConflict, errors.New("该批次已存在同名文件")
	}
	stage := path.Join(dir, uploadStageName+uid()+path.Ext(relative))
	f, err := a.data.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return uploadTarget{}, http.StatusInternalServerError, err
	}
	// The sweep holds a.mu while snapshotting the stages and pruning files.
	a.uploadStages.Store(a.dataPathKey(stage), true)
	return uploadTarget{path: dst, stage: stage, file: f}, 0, nil
}

func (a *app) releaseUploadStage(stage string) {
	if a.checkStorage() == nil {
		_ = a.data.Remove(stage)
	}
	a.uploadStages.Delete(a.dataPathKey(stage))
}

func (a *app) publishUpload(ctx context.Context, id, relative string, target uploadTarget, track Track) (Track, bool, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, dst, status, err := a.uploadBatch(ctx, id, relative)
	if err != nil {
		return Track{}, false, status, err
	}
	if dst != target.path {
		return Track{}, false, http.StatusConflict, errUploadConflict
	}
	if existing, ok := a.uploadedTrack(b, dst); ok {
		return existing, true, 0, nil
	}
	if target.stage == "" {
		return Track{}, false, http.StatusConflict, errUploadConflict
	}
	if status, err := draftAppendStatus(b); err != nil {
		return Track{}, false, status, err
	}
	info, err := a.data.Lstat(target.stage)
	if err != nil || !info.Mode().IsRegular() || info.Size() != track.Size || info.ModTime().UnixNano() != track.Modified {
		return Track{}, false, http.StatusConflict, errors.New("上传文件已变更，请重新上传")
	}
	if err := a.publishStaged(a.data, target.stage, dst); errors.Is(err, os.ErrExist) {
		return Track{}, false, http.StatusConflict, errUploadConflict
	} else if err != nil {
		return Track{}, false, http.StatusInternalServerError, err
	}
	committed := false
	defer func() {
		if committed || a.checkStorage() != nil {
			return
		}
		if current, err := a.data.Lstat(dst); err == nil && os.SameFile(info, current) {
			_ = a.data.Remove(dst)
		}
	}()
	if err := ctx.Err(); err != nil {
		return Track{}, false, http.StatusBadRequest, errUploadBody
	}
	if err := a.checkOperation(); err != nil {
		return Track{}, false, http.StatusServiceUnavailable, err
	}
	track.Path = dst
	b.Files = append(b.Files, track)
	b.ReadState, b.ReadError, b.QualityState, b.QualityError = "queued", "", "queued", ""
	b.Candidates, b.ChosenCandidate, b.Previewed = nil, "original", false
	b.State, b.Message = "ready", "上传完成，等待检查预览"
	if err := a.put("batches", b.ID, b); err != nil {
		return Track{}, false, http.StatusInternalServerError, err
	}
	committed = true
	return track, false, 0, nil
}

// Verify the existing bytes outside a.mu, then confirm under a.mu that the
// same file and draft entry are still current before returning its original ID.
func (a *app) verifyUploadRetry(ctx context.Context, id, relative, destination string, received, expected Track) (Track, int, error) {
	if received.Size != expected.Size || received.Hash != expected.Hash || regular(a.data, expected.Path) != nil {
		return Track{}, http.StatusConflict, errUploadConflict
	}
	f, err := a.data.Open(expected.Path)
	if err != nil {
		return Track{}, http.StatusConflict, errUploadConflict
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != expected.Size {
		return Track{}, http.StatusConflict, errUploadConflict
	}
	hash, err := digest(operationReader{ctx: ctx, r: f})
	readInfo, statErr := f.Stat()
	closeErr := f.Close()
	if err != nil || statErr != nil || closeErr != nil || hash != expected.Hash ||
		before.Size() != readInfo.Size() || !before.ModTime().Equal(readInfo.ModTime()) {
		return Track{}, http.StatusConflict, errUploadConflict
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	b, dst, status, err := a.uploadBatch(ctx, id, relative)
	if err != nil {
		return Track{}, status, err
	}
	current, ok := a.uploadedTrack(b, dst)
	if !ok || dst != destination || current.ID != expected.ID || current.Path != expected.Path ||
		current.Hash != expected.Hash || current.Size != expected.Size {
		return Track{}, http.StatusConflict, errUploadConflict
	}
	after, err := a.data.Lstat(current.Path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return Track{}, http.StatusConflict, errUploadConflict
	}
	return current, 0, nil
}
