package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	id3 "github.com/bogem/id3v2/v2"
)

type waitingCoverWriter struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	release chan struct{}
}

func (w *waitingCoverWriter) Write(data []byte) (int, error) {
	close(w.entered)
	<-w.release
	return w.ResponseRecorder.Write(data)
}

func TestSlowDraftCoverReaderDoesNotBlockMutations(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "cover", 10)
	b.ChosenCandidate = "manual"
	a.mu.Lock()
	err := a.putDraft(b, testAlbumCover(t, false), false)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	w := &waitingCoverWriter{httptest.NewRecorder(), make(chan struct{}), make(chan struct{})}
	done := make(chan struct{})
	go func() {
		a.routes(http.NotFoundHandler()).ServeHTTP(w, httptest.NewRequest("GET", "/api/drafts/"+b.ID+"/cover", nil))
		close(done)
	}()
	defer func() { close(w.release); <-done }()
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("cover response did not start")
	}
	locked := make(chan struct{})
	go func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("slow cover response holds the mutation lock")
	}
}

func uploadDraftCover(t *testing.T, a *app, batch Batch, tags []Tags, picture []byte, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	raw, _ := json.Marshal(tags)
	if err := form.WriteField("tags", string(raw)); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("cover", "cover.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(picture); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/drafts/"+batch.ID+"/tags", &body)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", form.FormDataContentType())
	if current, err := get[Batch](a, "batches", batch.ID); err == nil {
		r.Header.Set("If-Match", draftEditToken(current))
	}
	if csrf {
		r.Header.Set(librarySessionHeader, a.librarySession)
		r.Header.Set("X-CSRF-Token", a.csrf)
		r.AddCookie(&http.Cookie{Name: a.cookieName, Value: a.csrf})
	}
	w := httptest.NewRecorder()
	a.protect(a.routes(http.NotFoundHandler())).ServeHTTP(w, r)
	return w
}

func TestDraftCoverSaveImportAndCleanup(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "artwork", 184)
	// Include MP3 and FLAC to exercise artwork on every supported format.
	p := "uploads/" + b.ID + "/second.mp3"
	f, err := a.data.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	v := id3.NewEmptyTag()
	v.SetTitle("Second")
	v.SetArtist("Artist")
	v.SetAlbum("Album")
	_, err = v.WriteTo(f)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		frame := make([]byte, 417)
		copy(frame, []byte{0xff, 0xfb, 0x90, 0x00})
		if _, err = f.Write(frame); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := inspect(a.data, p)
	if err != nil {
		t.Fatal(err)
	}
	b.Files = append(b.Files, second)
	b.Candidates = []Candidate{originalCandidate(b)}
	if err = a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	tags := []Tags{b.Files[0].Tags, second.Tags}
	tags[0].Album, tags[1].Album = "封面专辑", "封面专辑"
	tags[0].AlbumArtist, tags[1].AlbumArtist = "同一专辑歌手", "同一专辑歌手"
	cover := testAlbumCover(t, false)
	w := uploadDraftCover(t, a, b, tags, cover.Data, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	fresh, err := get[Batch](a, "batches", b.ID)
	if err != nil || fresh.CoverHash == "" || fresh.ID != b.ID {
		t.Fatal("draft cover was not persisted", err)
	}
	w = request(a, "GET", "/api/drafts/"+b.ID+"/cover", "", false)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), cover.Data) {
		t.Fatal("saved artwork cannot be previewed")
	}
	// A subsequent tags-only save must keep the saved artwork.
	body, _ := json.Marshal(map[string]any{"tags": tags})
	w = request(a, "POST", "/api/drafts/"+b.ID+"/tags", string(body), true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err = a.importWithPolicy(b.ID, "keep", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	tracks := a.tracks()
	if len(tracks) != 2 {
		t.Fatal("missing imported tracks")
	}
	for _, track := range tracks {
		checkTrackCover(t, a, track, cover)
		if track.Tags.Album != "封面专辑" {
			t.Fatal("tags were not saved with artwork")
		}
		var original Track
		for _, source := range b.Files {
			if source.AudioHash == track.AudioHash {
				original = source
			}
		}
		if original.ID == "" {
			t.Fatal("import changed audio bytes")
		}
	}
	for _, source := range b.Files {
		if _, err := a.data.Stat(source.Path); !os.IsNotExist(err) {
			t.Fatal("imported source was retained")
		}
	}
	if _, err := a.data.Stat("uploads/" + b.ID + "/.artwork/" + b.ID + "/" + fresh.CoverHash); !os.IsNotExist(err) {
		t.Fatal("imported draft artwork was retained")
	}

}

func TestDraftCoverRejectsInvalidChangesAtomically(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "validation", 185)
	cover := testAlbumCover(t, false)
	tags := []Tags{b.Files[0].Tags}
	if w := uploadDraftCover(t, a, b, tags, cover.Data, false); w.Code != 403 {
		t.Fatal("missing session accepted")
	}
	for _, invalid := range [][]byte{[]byte("invalid image"), cover.Data[:24], make([]byte, maxAlbumCover+1)} {
		if w := uploadDraftCover(t, a, b, tags, invalid, true); w.Code != 400 {
			t.Fatal("invalid image accepted", w.Code)
		}
	}
	tags[0].Year = "invalid"
	if w := uploadDraftCover(t, a, b, tags, cover.Data, true); w.Code != 400 {
		t.Fatal("invalid tags accepted")
	}
	fresh, _ := get[Batch](a, "batches", b.ID)
	if fresh.CoverHash != "" || fresh.ChosenCandidate != b.ChosenCandidate {
		t.Fatal("invalid request partially saved draft")
	}
	b.Files[0].Editable = false
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	if w := uploadDraftCover(t, a, b, []Tags{b.Files[0].Tags}, cover.Data, true); w.Code != 400 {
		t.Fatal("unsupported format accepted")
	}
}

func TestDraftCoverFallsBackToEmbeddedArtwork(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "embedded", 186)
	cover := testAlbumCover(t, true)
	if err := writeTags(a.data, b.Files[0].Path, b.Files[0].Tags, cover); err != nil {
		t.Fatal(err)
	}
	w := request(a, "GET", "/api/drafts/"+b.ID+"/cover", "", false)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), cover.Data) {
		t.Fatal("embedded draft cover missing")
	}
}
