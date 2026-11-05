package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLibrarySessionRejectsOldTabWritesAfterDiskSwitch(t *testing.T) {
	old, current := testApp(t), testApp(t)
	// The controller intentionally keeps one CSRF identity across disk changes.
	current.csrf, current.cookieName = old.csrf, old.cookieName
	c := &diskController{security: old, active: current, handler: current.routes(http.NotFoundHandler()), volumeID: "new-disk"}
	handler := old.protect(c)
	for _, session := range []string{"", old.librarySession, current.librarySession} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/drafts", strings.NewReader(`{"name":"From tab"}`))
		r.RemoteAddr = "127.0.0.1:1234"
		r.AddCookie(&http.Cookie{Name: old.cookieName, Value: old.csrf})
		r.Header.Set("X-CSRF-Token", old.csrf)
		r.Header.Set(librarySessionHeader, session)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := http.StatusConflict
		if session == current.librarySession {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatalf("session %q: %d %s", session, w.Code, w.Body.String())
		}
	}
	if len(all[Batch](current, "batches")) != 1 || len(all[Batch](old, "batches")) != 0 {
		t.Fatal("a stale tab wrote to the newly selected library")
	}
	state := conditionalState(current, "")
	var payload struct {
		Session string `json:"library_session"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &payload); err != nil || payload.Session != current.librarySession {
		t.Fatalf("state omitted its resource precondition: %s", state.Body.String())
	}
}

func TestReopenLibraryInvalidatesMediaAndMutationContextWithoutChangingData(t *testing.T) {
	a := testApp(t)
	track := addTrack(t, a, "same.flac", "Keep", 1)
	session, tag, home := a.librarySession, conditionalState(a, "").Header().Get("ETag"), a.home
	music, data := a.music.Name(), a.data.Name()
	// Close explicitly; testApp's cleanup tolerates closing already closed handles.
	a.close()
	b, err := newAppStorage(home, music, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if session == b.librarySession {
		t.Fatal("reopen reused an old context")
	}
	stored, err := get[Track](b, "tracks", track.ID)
	if err != nil || stored != track {
		t.Fatal("reopen changed persistent track data", err)
	}
	if conditionalState(b, tag).Code != http.StatusOK {
		t.Fatal("old state ETag survived reopening")
	}
	for _, path := range []string{"/api/tracks", "/api/tracks/" + track.ID + "/audio?library_session=" + session} {
		r := httptest.NewRequest("GET", path, nil)
		if path == "/api/tracks" {
			r.Header.Set(librarySessionHeader, session)
		}
		w := httptest.NewRecorder()
		b.routes(http.NotFoundHandler()).ServeHTTP(w, r)
		if w.Code != http.StatusConflict {
			t.Fatalf("stale read accepted: %d", w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/api/tracks/"+track.ID+"/audio?library_session="+b.librarySession, nil)
	r.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	b.routes(http.NotFoundHandler()).ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.Len() != 4 {
		t.Fatalf("scoped Range failed: %d", w.Code)
	}
}
