package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	bolt "go.etcd.io/bbolt"

	library "hifi.local/library"
)

func (a *app) protect(next http.Handler) http.Handler {
	return a.protectManager(next, nil, nil, "", "")
}

// Fixtures use the production write boundary, including its cache revision.
func (a *app) putTrack(track Track) error {
	return a.db.Update(func(tx *bolt.Tx) error { return storeTrackTx(tx, track) })
}

func newApp(home string) (*app, error) {
	return newAppStorage(filepath.Join(home, library.ManagerDirectory), filepath.Join(home, library.MusicDirectory), filepath.Join(home, library.ManagerDirectory), nil)
}

func inspect(root *os.Root, p string) (Track, error) {
	return inspectContext(context.Background(), root, p)
}

func copyFile(from *os.Root, src string, to *os.Root, dst string) error {
	return copyFileContext(context.Background(), from, src, to, dst)
}

// Convenience helpers for existing fixture assertions. Production callers must
// handle storage errors instead of accepting a partial or empty collection.
func all[T any](a *app, bucket string) []T {
	values, err := readAll[T](a, bucket)
	if err != nil {
		panic(err)
	}
	return values
}

func (a *app) tracks() []Track {
	tracks, err := a.readTracks()
	if err != nil {
		panic(err)
	}
	return tracks
}

func (a *app) artistProfile(name string) ArtistProfile {
	profile, err := a.readArtistProfile(name)
	if err != nil {
		panic(err)
	}
	return profile
}

// Direct domain fixtures bypass HTTP admission but retain the same file lock.
func (a *app) importWithPolicy(id, policy string, ids []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.importLocked(id, policy, ids)
}

// Keep representative existing records in fixtures. New writes no longer
// duplicate file tags into an original candidate.
func originalCandidate(b Batch) Candidate {
	c := Candidate{ID: "original", Source: "file", Tags: []Tags{}}
	for _, t := range b.Files {
		c.Tags = append(c.Tags, t.Tags)
	}
	return c
}

func (a *app) deleteDraft(id string, ids []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.deleteDraftLocked(id, ids)
}
