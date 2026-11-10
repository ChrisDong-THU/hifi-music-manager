package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestUploadRetryPreservesIdentityAndRejectsChangedContent(t *testing.T) {
	a := testApp(t)
	w := request(a, "POST", "/api/drafts", `{"name":"Retry"}`, true)
	var batch Batch
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &batch) != nil {
		t.Fatal(w.Body.String())
	}
	path := "/api/drafts/" + batch.ID + "/files?path=retry.flac"
	body := string(toneFLAC("retry", 7))
	first := request(a, "POST", path, body, true)
	retry := request(a, "POST", path, body, true)
	if first.Code != 200 || retry.Code != 200 || first.Body.String() != retry.Body.String() {
		t.Fatalf("retry must return the original track: %d %d", first.Code, retry.Code)
	}
	batch, err := get[Batch](a, "batches", batch.ID)
	if err != nil || len(batch.Files) != 1 {
		t.Fatalf("retry duplicated the entry: %+v %v", batch.Files, err)
	}
	if changed := request(a, "POST", path, string(toneFLAC("different", 8)), true); changed.Code != 409 {
		t.Fatalf("changed retry accepted: %d", changed.Code)
	}
	if hash, err := fileHash(a.data, batch.Files[0].Path); err != nil || hash != batch.Files[0].Hash {
		t.Fatal("conflict changed the uploaded file", err)
	}
	// Repeating the last response is also safe at the batch size limit.
	for i := 1; i < 300; i++ {
		batch.Files = append(batch.Files, Track{ID: fmt.Sprint(i), Path: fmt.Sprintf("uploads/%s/%d.flac", batch.ID, i)})
	}
	if err := a.put("batches", batch.ID, batch); err != nil {
		t.Fatal(err)
	}
	if retry = request(a, "POST", path, body, true); retry.Code != 200 {
		t.Fatalf("full batch rejected its last response retry: %d", retry.Code)
	}
	if fresh := request(a, "POST", "/api/drafts/"+batch.ID+"/files?path=new.flac", body, true); fresh.Code != 400 {
		t.Fatalf("batch limit bypassed: %d", fresh.Code)
	}
	// A stale DB record must not hide an externally modified file.
	f, err := a.data.OpenFile(batch.Files[0].Path, os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("external change")
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if changed := request(a, "POST", path, body, true); changed.Code != 409 {
		t.Fatalf("externally modified file accepted: %d", changed.Code)
	}
}

type pausedUploadBody struct {
	io.Reader
	started chan struct{}
	resume  <-chan struct{}
	once    sync.Once
}

func (b *pausedUploadBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.resume
	return b.Reader.Read(p)
}

func uploadResponse(a *app, path string, body io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://127.0.0.1:8787"+path, body)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set(librarySessionHeader, a.librarySession)
	r.Header.Set("X-CSRF-Token", a.csrf)
	r.AddCookie(&http.Cookie{Name: a.cookieName, Value: a.csrf})
	w := httptest.NewRecorder()
	a.protect(a.routes(http.NotFoundHandler())).ServeHTTP(w, r)
	return w
}

func TestSlowUploadLetsOtherFilesCommitAndSurvivesSweep(t *testing.T) {
	for _, same := range []bool{true, false} {
		name := "conflicting content"
		if same {
			name = "identical content"
		}
		t.Run(name, func(t *testing.T) {
			a := testApp(t)
			b := Batch{ID: uid(), State: "uploading"}
			if err := a.put("batches", b.ID, b); err != nil {
				t.Fatal(err)
			}
			prefix := "/api/drafts/" + b.ID + "/files?path="
			original := toneFLAC("upload", 7)
			overlap := original
			if !same {
				overlap = toneFLAC("different", 8)
			}
			gate := make(chan struct{})
			resume := sync.OnceFunc(func() { close(gate) })
			first := make(chan *httptest.ResponseRecorder, 1)
			defer resume()
			reader := &pausedUploadBody{Reader: bytes.NewReader(original), started: make(chan struct{}), resume: gate}
			go func() { first <- uploadResponse(a, prefix+"shared.flac", reader) }()
			select {
			case <-reader.started:
			case <-time.After(5 * time.Second):
				t.Fatal("first upload did not start")
			}

			// The sweeper and a different upload must complete while this
			// client's request body is stalled.
			other := make(chan error, 1)
			go func() {
				if err := a.cleanupFiles(a.operationContext(), true); err != nil {
					other <- err
					return
				}
				w := uploadResponse(a, prefix+"other.flac", bytes.NewReader(original))
				if w.Code != http.StatusOK {
					other <- fmt.Errorf("different path: %d %s", w.Code, w.Body.String())
					return
				}
				other <- nil
			}()
			select {
			case err := <-other:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("slow upload blocked cleanup or another file")
			}
			winner := uploadResponse(a, prefix+"shared.flac", bytes.NewReader(overlap))
			if winner.Code != http.StatusOK {
				t.Fatalf("overlapping request: %d %s", winner.Code, winner.Body.String())
			}
			resume()
			select {
			case w := <-first:
				if same && (w.Code != http.StatusOK || w.Body.String() != winner.Body.String()) {
					t.Fatalf("identical upload did not return the committed track: %d %s", w.Code, w.Body.String())
				}
				if !same && w.Code != http.StatusConflict {
					t.Fatalf("conflicting upload did not fail: %d %s", w.Code, w.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("first upload did not finish")
			}
			current, err := get[Batch](a, "batches", b.ID)
			if err != nil || len(current.Files) != 2 {
				t.Fatalf("lost or duplicated concurrent files: %+v %v", current.Files, err)
			}
			if len(a.uploadsOnDisk(t, b.ID)) != 2 {
				t.Fatal("temporary upload was left behind")
			}
		})
	}
}

func (a *app) uploadsOnDisk(t *testing.T, id string) []os.DirEntry {
	t.Helper()
	f, err := a.data.Open(uploadsPrefix + id)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
