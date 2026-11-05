package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestAlbumDeletePreservesOtherAlbums(t *testing.T) {
	a := testApp(t)
	first := addTrack(t, a, "album/one.flac", "One", 1)
	second := addTrack(t, a, "album/disc2/two.flac", "Two", 2)
	other := addTrack(t, a, "other/three.flac", "Three", 3)
	if err := a.edit([]string{other.ID}, map[string]string{"album": "Other album"}); err != nil {
		t.Fatal(err)
	}
	other, _ = get[Track](a, "tracks", other.ID)
	ctx, cancel := context.WithCancel(context.Background())
	a.start(ctx)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"key": albumKey(first)})
	if w := request(a, "POST", "/api/albums/delete", string(body), false); w.Code != 403 {
		t.Fatal("album deletion did not enforce session")
	}
	if w := request(a, "POST", "/api/albums/delete", `{"key":""}`, true); w.Code != 400 {
		t.Fatal("empty album key accepted")
	}
	w := request(a, "POST", "/api/albums/delete", string(body), true)
	if w.Code != 202 {
		t.Fatalf("album deletion rejected: %s", w.Body.String())
	}
	var result struct {
		Job string `json:"job"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		for _, job := range a.taskSnapshot() {
			if job.ID != result.Job {
				continue
			}
			if job.State == "failed" {
				t.Fatal(job.Message)
			}
			completed = job.State == "done"
		}
		if completed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !completed {
		t.Fatal("album deletion timed out")
	}
	for _, track := range []Track{first, second} {
		if _, err := a.music.Stat(track.Path); !os.IsNotExist(err) {
			t.Fatal("album file not recycled")
		}
		if _, err := get[Track](a, "tracks", track.ID); err == nil {
			t.Fatal("recycled track still indexed")
		}
	}
	if tracks := a.tracks(); len(tracks) != 1 || tracks[0] != other {
		t.Fatal("album deletion affected another album")
	}
	var deletion Operation
	for _, operation := range all[Operation](a, "operations") {
		if operation.Kind == "delete" {
			deletion = operation
		}
	}
	if deletion.Affected != 2 || deletion.State != "done" {
		t.Fatal("whole album not recorded in one audit record")
	}
	if err := a.deleteAlbum(albumKey(first)); err == nil {
		t.Fatal("missing album accepted")
	}
}
