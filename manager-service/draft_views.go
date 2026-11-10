package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

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
	AlbumMatch   string                   `json:"album_match,omitempty"`
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

func draftDuplicates(files []Track, known contentIndex) []string {
	var duplicates []string
	seen := contentIndex{}
	for position, file := range files {
		if known.find(file) >= 0 || seen.find(file) >= 0 {
			duplicates = append(duplicates, file.ID)
		}
		seen.add(file, position)
	}
	return duplicates
}

func projectDraft(b Batch, known contentIndex, detail bool) draftView {
	files := append([]Track{}, b.Files...)
	saved := savedDraftTags(b)
	for i := range saved {
		files[i].Tags = saved[i]
	}
	view := draftView{ID: b.ID, Name: b.Name, Created: b.Created, State: b.State, Message: b.Message, ParentID: b.ParentID,
		Files: files, HasSavedTags: saved != nil, Previewed: b.Previewed, CoverHash: b.CoverHash,
		ReadState: b.ReadState, ReadError: b.ReadError, QualityState: b.QualityState, QualityError: b.QualityError,
		Quality: b.Quality, Duplicates: draftDuplicates(b.Files, known)}
	view.EditToken = draftEditToken(b)
	if detail {
		view.AlbumMatch = b.AlbumMatch
	}
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
		Files      []file
		Saved      []Tags
		Cover      string
		AlbumMatch string
	}{files, savedDraftTags(b), b.CoverHash, b.AlbumMatch})
	return fmt.Sprintf(`"%x"`, sha256.Sum256(raw))
}

func (a *app) draftReadRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/albums/matches", a.albumMatches)
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
			for _, b := range batches {
				if visibleDraft(b) {
					result.Drafts = append(result.Drafts, projectDraft(b, index.hashes, false))
				}
			}
			sort.Slice(result.Drafts, func(i, j int) bool {
				left, right := result.Drafts[i], result.Drafts[j]
				if left.Created != right.Created {
					return left.Created > right.Created
				}
				return left.ID < right.ID
			})
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
		var b Batch
		var index *libraryIndex
		err := a.db.View(func(tx *bolt.Tx) error {
			var err error
			b, err = getTx[Batch](tx, "batches", r.PathValue("id"))
			if err == nil && visibleDraft(b) {
				index, err = a.libraryIndexTx(tx)
			}
			return err
		})
		if errors.Is(err, os.ErrNotExist) || (err == nil && !visibleDraft(b)) {
			problem(w, 404, errors.New("草稿不存在或已完成"))
			return
		}
		if err != nil {
			problem(w, 500, err)
			return
		}
		view := projectDraft(b, index.hashes, true)
		if view.AlbumMatch != "" && view.AlbumMatch != "new" && len(view.Files) > 0 {
			tags := view.Files[0].Tags
			if match, _ := chooseAlbum(index.byIdentity[albumIdentity(tags.Album, albumCredit(tags))], view.AlbumMatch, tags.Year); match != nil {
				view.AlbumMatch = match.matchKey
			}
		}
		respond(w, view)
	})
}

func (a *app) albumMatches(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	title, artist, year := query.Get("title"), query.Get("artist"), query.Get("year")
	if len(title) > 4096 || len(artist) > 4096 || len(year) > 16 || strings.ContainsRune(title+artist, 0) {
		problem(w, 400, errors.New("专辑匹配条件无效"))
		return
	}
	matches := []albumPlacement{}
	var requiresChoice bool
	if strings.TrimSpace(title) != "" && strings.TrimSpace(artist) != "" {
		index, err := a.readLibraryIndex()
		if err != nil {
			problem(w, 500, err)
			return
		}
		releases := index.byIdentity[albumIdentity(title, artist)]
		for _, release := range releases {
			matches = append(matches, release.placement())
		}
		requiresChoice = requiresAlbumChoice(releases, year)
	}
	respond(w, map[string]any{"matches": matches, "requires_choice": requiresChoice})
}
