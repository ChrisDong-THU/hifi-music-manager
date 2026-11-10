package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestIndexCacheIsIsolatedAndInvalidatedByCommittedWrites(t *testing.T) {
	a := testApp(t)
	track := Track{ID: "one", Path: "one.flac", Tags: Tags{Title: "Original"}}
	if err := a.putTrack(track); err != nil {
		t.Fatal(err)
	}
	first, err := a.readTracks()
	if err != nil {
		t.Fatal(err)
	}
	first[0].Tags.Title = "Changed only by caller"
	second, err := a.readTracks()
	if err != nil || second[0].Tags.Title != "Original" {
		t.Fatal("cached tracks were exposed to mutation", second, err)
	}
	track.Tags.Title = "Saved"
	if err := a.putTrack(track); err != nil {
		t.Fatal(err)
	}
	updated, err := a.readTracks()
	if err != nil || updated[0].Tags.Title != "Saved" {
		t.Fatal("cache hid committed changes", updated, err)
	}
	if err := a.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("tracks"))
		if _, err := bucket.NextSequence(); err != nil {
			return err
		}
		return bucket.Put([]byte("one"), []byte("invalid"))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.readTracks(); err == nil || !strings.Contains(err.Error(), "索引失败") {
		t.Fatal("cache hid a corrupt index", err)
	}
}

func TestStateAggregatesFollowTrackCommitsWithoutRebuildingForDraftProgress(t *testing.T) {
	a := testApp(t)
	track := Track{ID: "one", Hash: "file", AudioHash: "audio", Size: 42, Duration: 12, Tags: Tags{Album: "Album", Artist: "Artist"}}
	if err := a.putTrack(track); err != nil {
		t.Fatal(err)
	}
	check := func(tracks int, duplicates int) {
		t.Helper()
		w := conditionalState(a, "")
		var state struct {
			Stats libraryStats `json:"stats"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &state) != nil {
			t.Fatalf("state: %s", w.Body.String())
		}
		var list draftList
		drafts := conditionalDrafts(a, "")
		if drafts.Code != 200 || json.Unmarshal(drafts.Body.Bytes(), &list) != nil {
			t.Fatal(drafts.Body.String())
		}
		if state.Stats.Tracks != tracks || len(list.Drafts) != 1 || len(list.Drafts[0].Duplicates) != duplicates {
			t.Fatalf("stale aggregates: %+v", state)
		}
		if tracks == 1 && (state.Stats.Bytes != 42 || state.Stats.Duration != 12 || state.Stats.Albums != 1 || state.Stats.Artists != 1) {
			t.Fatalf("wrong stats: %+v", state.Stats)
		}
	}
	b := Batch{ID: "draft", State: "ready", Files: []Track{{ID: "source", AudioHash: "audio"}}}
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	check(1, 1)
	index := a.index
	b.QualityState = "running"
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	check(1, 1)
	if a.index != index {
		t.Fatal("draft progress rebuilt the library aggregates")
	}
	if err := a.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("tracks"))
		if _, err := bucket.NextSequence(); err != nil {
			return err
		}
		return bucket.Delete([]byte(track.ID))
	}); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
	if a.index == index {
		t.Fatal("track deletion did not invalidate aggregates")
	}
	if _, ok := index.hashes["audio"]; index.stats.Tracks != 1 || !ok {
		t.Fatal("new state mutated an older snapshot")
	}
}
