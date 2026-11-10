package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"
)

func albumDeleteRequest(a *app, key, token string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"key": key})
	r := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/albums/delete", bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set(librarySessionHeader, a.librarySession)
	r.Header.Set("X-CSRF-Token", a.csrf)
	r.Header.Set("If-Match", token)
	r.AddCookie(&http.Cookie{Name: a.cookieName, Value: a.csrf})
	w := httptest.NewRecorder()
	a.protect(a.routes(http.NotFoundHandler())).ServeHTTP(w, r)
	return w
}

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
	detail := request(a, "GET", "/api/albums/detail?key="+url.QueryEscape(albumKey(first)), "", false)
	token := detail.Header().Get("ETag")
	w := albumDeleteRequest(a, albumKey(first), token)
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
	if w := albumDeleteRequest(a, albumKey(first), token); w.Code != http.StatusPreconditionFailed {
		t.Fatal("missing album accepted")
	}
}

func TestQueuedAlbumDeletionCannotIncludeNewTracks(t *testing.T) {
	a := testApp(t)
	first := addTrack(t, a, "album/one.flac", "One", 1)
	key := albumKey(first)
	token := request(a, "GET", "/api/albums/detail?key="+url.QueryEscape(key), "", false).Header().Get("ETag")
	if w := albumDeleteRequest(a, key, ""); w.Code != http.StatusPreconditionRequired {
		t.Fatal("deletion without snapshot was accepted", w.Code, w.Body.String())
	}
	if w := albumDeleteRequest(a, key, token); w.Code != http.StatusAccepted {
		t.Fatal("current deletion was rejected", w.Code, w.Body.String())
	}
	added := addTrack(t, a, "album/two.flac", "Two", 2)
	work := <-a.queue
	if err := work.fn(); !errors.Is(err, errAlbumSnapshotStale) {
		t.Fatal("queued deletion did not reject changed album membership", err)
	}
	for _, track := range []Track{first, added} {
		if _, err := a.music.Stat(track.Path); err != nil {
			t.Fatal("stale album deletion removed a file", err)
		}
	}
	if w := albumDeleteRequest(a, key, token); w.Code != http.StatusPreconditionFailed {
		t.Fatal("stale deletion was accepted", w.Code, w.Body.String())
	}
}
