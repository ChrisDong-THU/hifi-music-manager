package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func conditionalDrafts(a *app, tag string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/api/drafts", nil)
	r.Header.Set("If-None-Match", tag)
	w := httptest.NewRecorder()
	a.routes(http.NotFoundHandler()).ServeHTTP(w, r)
	return w
}

func TestPublicDraftSeparatesListDetailAndPersistence(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Draft", 41)
	b.Files[0].Tags.Lyrics = "private editable lyric data"
	b.Candidates = []Candidate{originalCandidate(b), {ID: "manual", Source: "manual", Tags: []Tags{b.Files[0].Tags}}}
	b.Candidates[1].Tags[0].Title = "Saved title"
	b.ChosenCandidate = "manual"
	b.Cleanup = []string{"internal pending path"}
	b.Imported = []string{"internal bookkeeping"}
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	w := conditionalDrafts(a, "")
	var list draftList
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Drafts) != 1 {
		t.Fatal(w.Body.String())
	}
	if list.Drafts[0].Files[0].Tags.Title != "Saved title" || list.Drafts[0].Files[0].Tags.Lyrics != "" {
		t.Fatal("list did not expose the effective lightweight tags")
	}
	for _, field := range []string{`"candidates"`, `"cleanup"`, `"upload_owner"`, `"imported"`} {
		if strings.Contains(w.Body.String(), field) {
			t.Fatal("persistence leaked into draft list", field)
		}
	}
	if conditionalDrafts(a, w.Header().Get("ETag")).Code != 304 {
		t.Fatal("unchanged draft query did not use ETag")
	}
	detail := request(a, "GET", "/api/drafts/"+b.ID, "", false)
	var view draftView
	if detail.Code != 200 || json.Unmarshal(detail.Body.Bytes(), &view) != nil || view.EditToken == "" || view.Files[0].Tags.Lyrics != "private editable lyric data" {
		t.Fatal("detail omitted the editing snapshot", detail.Body.String())
	}
	control := conditionalState(a, "")
	if strings.Contains(control.Body.String(), `"drafts"`) || strings.Contains(control.Body.String(), `"batches"`) || strings.Contains(control.Body.String(), "private editable") {
		t.Fatal("control plane embedded workspace data")
	}
}

func TestBrokenDraftCannotDisableControlPlane(t *testing.T) {
	a := testApp(t)
	if err := a.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("batches")).Put([]byte("broken"), []byte("{invalid")) }); err != nil {
		t.Fatal(err)
	}
	if w := conditionalState(a, ""); w.Code != 200 {
		t.Fatal("draft corruption disabled the control plane", w.Body.String())
	}
	if w := conditionalDrafts(a, ""); w.Code != 500 {
		t.Fatal("draft error was silently hidden", w.Body.String())
	}
}

func TestDraftSavePreconditionProtectsUserEditsWithoutFollowingTaskProgress(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Edits", 53)
	token := draftEditToken(b)
	b.QualityState = "done"
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	if draftEditToken(b) != token {
		t.Fatal("progress invalidated a content snapshot")
	}
	save := func(token, title string) *httptest.ResponseRecorder {
		tags := b.Files[0].Tags
		tags.Title = title
		raw, _ := json.Marshal(map[string]any{"tags": []Tags{tags}})
		r := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/drafts/"+b.ID+"/tags", strings.NewReader(string(raw)))
		r.RemoteAddr = "127.0.0.1:1234"
		r.AddCookie(&http.Cookie{Name: a.cookieName, Value: a.csrf})
		r.Header.Set("X-CSRF-Token", a.csrf)
		r.Header.Set(librarySessionHeader, a.librarySession)
		r.Header.Set("If-Match", token)
		w := httptest.NewRecorder()
		a.protect(a.routes(http.NotFoundHandler())).ServeHTTP(w, r)
		return w
	}
	if w := save("", "Missing"); w.Code != 428 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := save(token, "First editor"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := save(token, "Stale editor"); w.Code != 412 {
		t.Fatal(w.Code, w.Body.String())
	}
	current, err := get[Batch](a, "batches", b.ID)
	if err != nil || projectDraft(current, true).Files[0].Tags.Title != "First editor" {
		t.Fatal("stale form overwrote saved content", err)
	}
}

func TestEmptyDraftHasExplicitDiscardAndRechecksContentAtExecution(t *testing.T) {
	a := testApp(t)
	w := request(a, "POST", "/api/drafts", `{"name":"Interrupted intake"}`, true)
	var created struct {
		ID string `json:"id"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.ID == "" {
		t.Fatal(w.Body.String())
	}
	accepted := request(a, "POST", "/api/drafts/"+created.ID+"/discard", "{}", true)
	if accepted.Code != 202 {
		t.Fatal(accepted.Body.String())
	}
	// A delayed command must not delete a song uploaded after the user's click.
	b, err := get[Batch](a, "batches", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	b.Files = []Track{{ID: "newly-uploaded"}}
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	queued := <-a.queue
	if err := queued.fn(); err == nil {
		t.Fatal("discard removed a draft that acquired content")
	}
	if _, err := get[Batch](a, "batches", b.ID); err != nil {
		t.Fatal("content-bearing draft disappeared", err)
	}
	b.Files = nil
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	if err := a.discardEmptyDraft(b.ID); err != nil {
		t.Fatal(err)
	}
	if list := conditionalDrafts(a, ""); strings.Contains(list.Body.String(), created.ID) {
		t.Fatal("discarded empty draft remains visible")
	}
}

func TestQueuedDraftIntentCannotSilentlyFollowLaterEdits(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Queued intent", 64)
	token := draftEditToken(b)
	called := false
	if _, err := a.submitDraftChange("import", b.ID, token, func() error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	chosen := b.Files[0].Tags
	chosen.Title = "Another editor"
	b.Candidates = append(b.Candidates, Candidate{ID: "manual", Source: "manual", Tags: []Tags{chosen}})
	b.ChosenCandidate = "manual"
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	work := <-a.queue
	if err := work.fn(); !errors.Is(err, errDraftSnapshotStale) || called {
		t.Fatal("queued intent operated on a later snapshot", err)
	}
	if _, err := a.submitDraftChange("draft-delete", b.ID, token, func() error { return nil }); !errors.Is(err, errDraftSnapshotStale) {
		t.Fatal("stale intent was accepted", err)
	}
	if _, err := a.submitDraftChange("import", b.ID, draftEditToken(b), func() error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	work = <-a.queue
	if err := work.fn(); err != nil || !called {
		t.Fatal("valid current intent was rejected", err)
	}
}
