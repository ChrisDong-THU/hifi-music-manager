package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	bolt "go.etcd.io/bbolt"
	library "hifi.local/library"
)

func conditionalState(a *app, tag string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "http://127.0.0.1:8787/api/state", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("If-None-Match", tag)
	w := httptest.NewRecorder()
	a.protect(a.routes(http.NotFoundHandler())).ServeHTTP(w, r)
	return w
}

func TestDraftListExcludesCompletedImportRecordsBeforeCleanup(t *testing.T) {
	a := testApp(t)
	live := addBatch(t, a, "Live draft", 210)
	completed := Batch{ID: uid(), State: "imported", Imported: []string{"missing-track"}}
	if err := a.put("batches", completed.ID, completed); err != nil {
		t.Fatal(err)
	}
	w := conditionalDrafts(a, "")
	var state struct {
		Batches []draftView `json:"drafts"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || len(state.Batches) != 1 {
		t.Fatalf("invalid state: %d %s", w.Code, w.Body.String())
	}
	if state.Batches[0].ID != live.ID {
		t.Fatal("completed import is still exposed as a draft")
	}
	if _, err := get[Batch](a, "batches", completed.ID); err != nil {
		t.Fatal("reading state must not perform cleanup", err)
	}
}

func TestConditionalStateTracksDatabaseAndJobChanges(t *testing.T) {
	a := testApp(t)
	first := conditionalState(a, "")
	tag := first.Header().Get("ETag")
	if first.Code != 200 || tag == "" {
		t.Fatalf("initial state: %d %q", first.Code, tag)
	}
	unchanged := conditionalState(a, tag)
	if unchanged.Code != 304 || unchanged.Body.Len() != 0 || len(unchanged.Result().Cookies()) == 0 {
		t.Fatalf("unchanged state must be bodyless and renew its session: %d", unchanged.Code)
	}
	if err := a.putTrack(Track{ID: "one", Tags: Tags{Artist: "Track artist", AlbumArtist: "Album artist"}}); err != nil {
		t.Fatal(err)
	}
	updated := conditionalState(a, tag)
	if updated.Code != 200 || updated.Header().Get("ETag") == tag {
		t.Fatal("database changes did not invalidate state")
	}
	var state struct {
		Revision int `json:"revision"`
		Stats    struct {
			Artists int `json:"artists"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &state); err != nil || state.Revision == 0 || state.Stats.Artists != 1 {
		t.Fatalf("state: %+v %v", state, err)
	}
	tag = updated.Header().Get("ETag")
	id, err := a.submit("scan", func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	queued := conditionalState(a, tag)
	if queued.Code != 200 || queued.Header().Get("ETag") == tag {
		t.Fatal("queued job did not invalidate state")
	}
	a.jobMu.Lock()
	a.jobs[id].State = "done"
	a.jobMu.Unlock()
	if done := conditionalState(a, queued.Header().Get("ETag")); done.Code != 200 {
		t.Fatal("completed job did not invalidate state")
	}
}

func TestStateInvalidatesWhenPublicArtistResourcesChange(t *testing.T) {
	a := testApp(t)
	first := conditionalState(a, "")
	tag := first.Header().Get("ETag")
	if _, err := library.SaveArtist(a.artists, library.Artist{Name: "Artist", Biography: "Updated by another client"}, nil); err != nil {
		t.Fatal(err)
	}
	updated := conditionalState(a, tag)
	if updated.Code != 200 || updated.Header().Get("ETag") == tag {
		t.Fatal("public resource change did not invalidate the UI", updated.Code)
	}
	var state struct {
		ResourceRevision uint32 `json:"resource_revision"`
	}
	if json.Unmarshal(updated.Body.Bytes(), &state) != nil || state.ResourceRevision == 0 {
		t.Fatal(updated.Body.String())
	}
}

func TestDraftQueriesUseOneDatabaseSnapshotDuringConcurrentWrites(t *testing.T) {
	a := testApp(t)
	write := func(count int) error {
		return a.db.Update(func(tx *bolt.Tx) error {
			tracks := tx.Bucket([]byte("tracks"))
			if err := tracks.Delete([]byte("second")); err != nil {
				return err
			}
			files := []Track{{ID: "first", Hash: "first", AudioHash: "first"}}
			if count == 2 {
				files = append(files, Track{ID: "second", Hash: "second", AudioHash: "second"})
			}
			for _, track := range files {
				if err := storeTrackTx(tx, track); err != nil {
					return err
				}
			}
			raw, _ := json.Marshal(Batch{ID: "draft", State: "ready", Files: files})
			return tx.Bucket([]byte("batches")).Put([]byte("draft"), raw)
		})
	}
	if err := write(1); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := write(i%2 + 1); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	defer func() { close(stop); wg.Wait() }()
	for range 100 {
		w := conditionalDrafts(a, "")
		var state struct {
			Batches []draftView `json:"drafts"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || len(state.Batches) != 1 {
			t.Fatalf("invalid state: %d %s", w.Code, w.Body.String())
		}
		if len(state.Batches[0].Duplicates) != len(state.Batches[0].Files) {
			t.Fatal("drafts and duplicate lookup came from different database transactions")
		}
	}
}

func TestPaginationCannotOverflow(t *testing.T) {
	a := testApp(t)
	for i := range 3 {
		track := Track{ID: strconv.Itoa(i), Tags: Tags{Title: strconv.Itoa(i), Album: strconv.Itoa(i)}}
		if err := a.putTrack(track); err != nil {
			t.Fatal(err)
		}
	}
	for _, resource := range []string{"tracks", "albums"} {
		for _, page := range []string{"9223372036854775807", "9223372036854775808", "4611686018427387904", "99999999999999999999999999"} {
			w := request(a, "GET", "/api/"+resource+"?page="+page, "", false)
			var result map[string]json.RawMessage
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || string(result[resource]) != "[]" {
				t.Fatalf("%s page %s: %d %s", resource, page, w.Code, w.Body.String())
			}
		}
		for _, page := range []string{"-1", "invalid"} {
			w := request(a, "GET", "/api/"+resource+"?page="+page, "", false)
			var result map[string]json.RawMessage
			_ = json.Unmarshal(w.Body.Bytes(), &result)
			if w.Code != 200 || string(result[resource]) == "[]" {
				t.Fatalf("bad fallback %s %s", resource, page)
			}
		}
	}
}

func TestArtistIdentityAndAlbumArtistSearchAgree(t *testing.T) {
	a := testApp(t)
	for i, tags := range []Tags{{Title: "One", Album: "A", Artist: "Singer", AlbumArtist: "Compilation artist"}, {Title: "Two", Album: "B"}} {
		if err := a.putTrack(Track{ID: strconv.Itoa(i), Tags: tags}); err != nil {
			t.Fatal(err)
		}
	}
	for _, resource := range []string{"tracks", "albums", "artists"} {
		w := request(a, "GET", "/api/"+resource+"?q="+url.QueryEscape("Compilation artist"), "", false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Compilation artist") {
			t.Fatalf("album artist missing in %s: %s", resource, w.Body.String())
		}
	}
	for _, resource := range []string{"tracks", "albums"} {
		w := request(a, "GET", "/api/"+resource+"?artist="+url.QueryEscape("未知歌手"), "", false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":1`) {
			t.Fatalf("unknown artist cannot be filtered in %s: %s", resource, w.Body.String())
		}
	}
}

func TestCorruptIndexIsReportedAndNeverOverwrittenByScan(t *testing.T) {
	a := testApp(t)
	if err := a.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("tracks")).Put([]byte("bad"), []byte("{broken")) }); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"state", "tracks", "albums", "artists", "albums/detail?key=missing", "artists/detail?name=missing"} {
		if w := request(a, "GET", "/api/"+path, "", false); w.Code != 500 {
			t.Fatalf("%s hides corrupt index: %d", path, w.Code)
		}
	}
	if err := a.scan(); err == nil {
		t.Fatal("scan erased an unreadable index")
	}
	if err := a.db.View(func(tx *bolt.Tx) error {
		if string(tx.Bucket([]byte("tracks")).Get([]byte("bad"))) != "{broken" {
			return fmt.Errorf("corrupt record was changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkStatePolling(b *testing.B) {
	a, err := newApp(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer a.close()
	err = a.db.Update(func(tx *bolt.Tx) error {
		for i := range 1000 {
			track := Track{ID: strconv.Itoa(i), Tags: Tags{Title: fmt.Sprintf("Track %d", i), Artist: "Artist", Album: "Album", Lyrics: strings.Repeat("lyrics ", 100)}}
			if err := storeTrackTx(tx, track); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	tag := conditionalState(a, "").Header().Get("ETag")
	handler := a.protect(a.routes(http.NotFoundHandler()))
	for _, tc := range []struct{ name, tag string }{{"full", ""}, {"unchanged", tag}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				r := httptest.NewRequest("GET", "http://127.0.0.1:8787/api/state", nil)
				r.RemoteAddr = "127.0.0.1:12345"
				r.Header.Set("If-None-Match", tc.tag)
				handler.ServeHTTP(httptest.NewRecorder(), r)
			}
		})
	}
}

func TestTagValidationAndPortableDestination(t *testing.T) {
	for _, patch := range []map[string]string{{"year": "-123"}, {"year": "+123"}, {"year": "0000"}, {"track": "1/2/3"}, {"disc": "+1"}} {
		if _, err := patchTags(Tags{}, patch); err == nil {
			t.Fatalf("invalid tags accepted: %v", patch)
		}
	}
	if _, err := patchTags(Tags{}, map[string]string{"year": "2024", "track": "01/10", "disc": "1/2"}); err != nil {
		t.Fatal(err)
	}
	name := component(strings.Repeat("音乐", 100))
	if len(name) > 200 || !utf8.ValidString(name) || strings.ContainsAny(name, `/\`) {
		t.Fatalf("unsafe destination component: %q", name)
	}
}
