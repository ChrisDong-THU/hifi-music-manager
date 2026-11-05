package main

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"
)

var discFolder = regexp.MustCompile(`(?i)^(cd|disc|disk)[ _.-]*[0-9]+$`)

// Album tasks refer to the existing inbox files. Grouping never moves or
// renames files, databases, library roots, or existing batch identifiers.
func (a *app) splitAlbums(id string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, err := get[Batch](a, "batches", id)
	if err != nil {
		return nil, err
	}
	if b.State == "deleted" {
		return nil, errors.New("当前草稿不能继续处理")
	}
	if len(b.Children) > 0 {
		return b.Children, nil
	}
	if err := a.checkDraftWritable(id); err != nil {
		return nil, err
	}
	root, err := draftFileRoot(b)
	if err != nil {
		return nil, err
	}
	if len(b.Imported) > 0 {
		return nil, errors.New("当前目录不能重新分组")
	}
	groups := map[string][]Track{}
	for _, f := range b.Files {
		dir := filepath.Dir(f.Path)
		if discFolder.MatchString(filepath.Base(dir)) {
			dir = filepath.Dir(dir)
		}
		key := "folder:" + filepath.ToSlash(dir)
		if f.Tags.Album != "" {
			key += "\x00album:" + albumKey(f)
		}
		groups[key] = append(groups[key], f)
	}
	if len(groups) <= 1 {
		return []string{id}, nil
	}
	keys := []string{}
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	children := []Batch{}
	for _, key := range keys {
		files := groups[key]
		name := files[0].Tags.Album
		if name != "" {
			artist := files[0].Tags.AlbumArtist
			if artist == "" {
				artist = files[0].Tags.Artist
			}
			if artist != "" {
				name = artist + " — " + name
			}
		} else {
			name = filepath.Base(filepath.Dir(files[0].Path))
			if name == b.ID {
				name = b.Name
			}
		}
		child := Batch{ID: uid(), Name: name, Created: now(), State: "ready", Files: files, ParentID: b.ID, Imported: []string{}}
		child.UploadOwner = filepath.Base(root)
		child.ChosenCandidate = "original"
		child.Quality = qualityForFiles(b.Quality, child.Files)
		child.ReadState, child.QualityState = "queued", "queued"
		children = append(children, child)
		b.Children = append(b.Children, child.ID)
	}
	b.Files, b.Candidates = nil, nil
	b.State = "grouped"
	b.Message = fmt.Sprintf("已拆分为 %d 个专辑任务", len(children))
	err = a.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("batches"))
		for _, child := range children {
			raw, e := marshal(child)
			if e != nil {
				return e
			}
			if e = bucket.Put([]byte(child.ID), raw); e != nil {
				return e
			}
		}
		raw, e := marshal(b)
		if e != nil {
			return e
		}
		return bucket.Put([]byte(b.ID), raw)
	})
	return b.Children, err
}

func (a *app) prepare(id string) (resultErr error) {
	defer func() { resultErr = a.failDraftStages(id, "prepare", resultErr) }()
	ids, err := a.splitAlbums(id)
	if err != nil {
		return err
	}
	issues := []string{}
	prepared := []string{}
	for _, id := range ids {
		if err = a.setDraftStage(id, "prepare", "queued", ""); err != nil {
			return err
		}
		if err = a.localPreview(id); err != nil {
			issues = append(issues, err.Error())
			continue
		}
		prepared = append(prepared, id)
	}
	// One queued operation owns the order, including album groups created
	// above. Upload completion only submits this operation once.
	for _, id := range prepared {
		if err = a.analyzeDraftQuality(id); err != nil {
			issues = append(issues, err.Error())
		}
	}
	if len(issues) > 0 {
		return errors.New(strings.Join(issues[:min(3, len(issues))], "；"))
	}
	return nil
}
func (a *app) workflowRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/drafts/{id}/prepare", func(w http.ResponseWriter, r *http.Request) {
		var options struct{}
		if !decode(w, r, &options) {
			return
		}
		batch := r.PathValue("id")
		id, err := a.submitDraftTask(batch, "prepare")
		jobResponse(w, id, err)
	})
	m.HandleFunc("GET /api/drafts/{id}/files/{file}/audio", func(w http.ResponseWriter, r *http.Request) {
		b, err := get[Batch](a, "batches", r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		for _, t := range b.Files {
			if t.ID != r.PathValue("file") {
				continue
			}
			if regular(a.data, t.Path) != nil {
				http.NotFound(w, r)
				return
			}
			f, e := a.data.Open(t.Path)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			defer f.Close()
			st, e := f.Stat()
			if e != nil {
				http.NotFound(w, r)
				return
			}
			http.ServeContent(w, r, filepath.Base(t.Path), st.ModTime(), f)
			return
		}
		http.NotFound(w, r)
	})
}
