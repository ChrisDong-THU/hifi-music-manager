package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	bolt "go.etcd.io/bbolt"
)

// The public draft is a work-in-progress resource, not the stored Batch record.
// Cleanup ownership, import bookkeeping and candidate representations stay on
// the server. Files expose the effective tags selected by the saved draft.
type draftView struct {
	ID           string                   `json:"id"`
	Name         string                   `json:"name"`
	Created      string                   `json:"created"`
	State        string                   `json:"state"`
	Message      string                   `json:"message"`
	ParentID     string                   `json:"parent_id,omitempty"`
	Files        []Track                  `json:"files"`
	HasSavedTags bool                     `json:"has_saved_tags"`
	Previewed    bool                     `json:"previewed"`
	CoverHash    string                   `json:"cover_hash,omitempty"`
	ReadState    string                   `json:"read_state,omitempty"`
	ReadError    string                   `json:"read_error,omitempty"`
	QualityState string                   `json:"quality_state,omitempty"`
	QualityError string                   `json:"quality_error,omitempty"`
	Quality      map[string]qualityResult `json:"quality,omitempty"`
	Duplicates   []string                 `json:"duplicates,omitempty"`
	EditToken    string                   `json:"edit_token,omitempty"`
}

type draftList struct {
	Drafts   []draftView `json:"drafts"`
	Revision int         `json:"revision"`
}

var (
	errDraftSnapshotRequired = errors.New("请先读取草稿再提交操作")
	errDraftSnapshotStale    = errors.New("草稿已被修改，请重新打开后提交")
)

func validateDraftSnapshot(b Batch, token string) error {
	if token == "" {
		return errDraftSnapshotRequired
	}
	if token != draftEditToken(b) {
		return errDraftSnapshotStale
	}
	return nil
}

// Bind accepted intent to its resource snapshot. Re-check under the file lock
// because another command may commit between acceptance and execution.
func (a *app) submitDraftChange(kind, id, token string, runLocked func() error) (string, error) {
	b, err := get[Batch](a, "batches", id)
	if err != nil {
		return "", err
	}
	if err := validateDraftSnapshot(b, token); err != nil {
		return "", err
	}
	return a.submitForBatch(kind, id, func() error {
		a.mu.Lock()
		defer a.mu.Unlock()
		current, err := get[Batch](a, "batches", id)
		if err != nil {
			return err
		}
		if err := validateDraftSnapshot(current, token); err != nil {
			return err
		}
		return runLocked()
	})
}

func visibleDraft(b Batch) bool {
	return b.State == "uploading" || b.State == "ready" || b.State == "importing"
}

func projectDraft(b Batch, detail bool) draftView {
	files := append([]Track{}, b.Files...)
	saved := savedDraftTags(b)
	for i := range saved {
		files[i].Tags = saved[i]
	}
	view := draftView{ID: b.ID, Name: b.Name, Created: b.Created, State: b.State, Message: b.Message, ParentID: b.ParentID,
		Files: files, HasSavedTags: saved != nil, Previewed: b.Previewed, CoverHash: b.CoverHash,
		ReadState: b.ReadState, ReadError: b.ReadError, QualityState: b.QualityState, QualityError: b.QualityError,
		Quality: b.Quality, Duplicates: b.Duplicates}
	view.EditToken = draftEditToken(b)
	if !detail {
		for i := range view.Files {
			view.Files[i].Tags.Lyrics = ""
		}
	}
	return view
}

// An edit precondition follows editable content, not progress or unrelated DB
// commits. Reading/analyzing other files must not invalidate a user's form.
func draftEditToken(b Batch) string {
	type file struct {
		ID, Hash string
		Tags     Tags
	}
	files := make([]file, 0, len(b.Files))
	for _, track := range b.Files {
		files = append(files, file{track.ID, track.Hash, track.Tags})
	}
	raw, _ := json.Marshal(struct {
		Files []file
		Saved []Tags
		Cover string
	}{files, savedDraftTags(b), b.CoverHash})
	return fmt.Sprintf(`"%x"`, sha256.Sum256(raw))
}

func (a *app) draftReadRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/drafts", func(w http.ResponseWriter, r *http.Request) {
		var result draftList
		var tag string
		err := a.db.View(func(tx *bolt.Tx) error {
			result.Revision = tx.ID()
			tag = fmt.Sprintf(`"drafts-%s-%d"`, a.librarySession, tx.ID())
			if tag == r.Header.Get("If-None-Match") {
				return nil
			}
			index, err := a.libraryIndexTx(tx)
			if err != nil {
				return err
			}
			batches, err := readAllTx[Batch](tx, "batches")
			if err != nil {
				return err
			}
			result.Drafts = []draftView{}
			for _, b := range sortedBatches(batches, index.hashes) {
				if visibleDraft(b) {
					result.Drafts = append(result.Drafts, projectDraft(b, false))
				}
			}
			return nil
		})
		if err != nil {
			problem(w, 500, err)
			return
		}
		w.Header().Set("ETag", tag)
		if tag == r.Header.Get("If-None-Match") {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		respond(w, result)
	})
	m.HandleFunc("GET /api/drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		b, err := get[Batch](a, "batches", r.PathValue("id"))
		if errors.Is(err, os.ErrNotExist) || (err == nil && !visibleDraft(b)) {
			problem(w, 404, errors.New("草稿不存在或已完成"))
			return
		}
		if err != nil {
			problem(w, 500, err)
			return
		}
		view := projectDraft(b, true)
		respond(w, view)
	})
}
