package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
	library "hifi.local/library"
)

// The control plane contains bounded operational metadata. Feature content is
// queried independently and cannot make every poll grow with workspace size.
type serviceState struct {
	CSRF             string       `json:"csrf"`
	Mode             string       `json:"mode"`
	Revision         int          `json:"revision"`
	LibrarySession   string       `json:"library_session"`
	ResourceRevision uint32       `json:"resource_revision"`
	CoverRevision    uint64       `json:"cover_revision"`
	ActiveMutations  int32        `json:"active_mutations"`
	MusicPath        string       `json:"music_path"`
	UploadsPath      string       `json:"uploads_path"`
	Stats            libraryStats `json:"stats"`
	Operations       []Operation  `json:"operations"`
	Jobs             []task       `json:"jobs"`
	PlaybackSync     playbackSync `json:"playback_sync"`
	Storage          *storageInfo `json:"storage"`
}

// The database transaction and runtime jobs identify a state snapshot. Unchanged
// polls need no library/history decoding, response serialization or client render.
func (a *app) state(w http.ResponseWriter, r *http.Request) {
	jobs := a.taskSnapshot()
	resourceRevision := library.Revision(a.artists)
	mutations := a.mutations.Load()
	var revision int
	var tag string
	var index *libraryIndex
	var ops []Operation
	var coverRevision uint64
	var volumeID string
	if a.storage != nil {
		volumeID = a.storage.VolumeID
	}
	rawJobs, _ := json.Marshal(jobs)
	err := a.db.View(func(tx *bolt.Tx) error {
		revision = tx.ID()
		coverRevision = tx.Bucket([]byte("tracks")).Sequence()
		tag = fmt.Sprintf(`"%x"`, sha256.Sum256(fmt.Appendf(rawJobs, "%s:%s:%s:%d:%d:%d", a.csrf, a.librarySession, volumeID, revision, resourceRevision, mutations)))
		if r.Header.Get("If-None-Match") == tag {
			return nil
		}
		var err error
		if index, err = a.libraryIndexTx(tx); err != nil {
			return err
		}
		if ops, err = recentOperationsTx(tx); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		problem(w, 500, err)
		return
	}
	w.Header().Set("ETag", tag)
	http.SetCookie(w, &http.Cookie{Name: a.cookieName, Value: a.csrf, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/", Secure: r.TLS != nil})
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	mode := "local"
	if a.lan.IsValid() {
		mode = "lan"
	}
	respond(w, serviceState{
		CSRF: a.csrf, Mode: mode, Revision: revision, LibrarySession: a.librarySession,
		ResourceRevision: resourceRevision, CoverRevision: coverRevision, ActiveMutations: mutations,
		MusicPath: a.music.Name(), UploadsPath: filepath.Join(a.data.Name(), "uploads"),
		Stats: index.stats, Operations: ops, Jobs: jobs, PlaybackSync: a.playbackSyncState(), Storage: a.storage,
	})
}
