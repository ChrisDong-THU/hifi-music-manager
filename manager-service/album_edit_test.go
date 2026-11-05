package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	id3 "github.com/bogem/id3v2/v2"
	"github.com/dhowden/tag"
)

func testAlbumCover(t *testing.T, jpegFormat bool) *albumCover {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 24, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 24; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 10), uint8(y * 10), 90, 255})
		}
	}
	var data bytes.Buffer
	var err error
	if jpegFormat {
		err = jpeg.Encode(&data, img, nil)
	} else {
		err = png.Encode(&data, img)
	}
	if err != nil {
		t.Fatal(err)
	}
	cover, err := parseAlbumCover(data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return cover
}

func checkTrackCover(t *testing.T, a *app, track Track, cover *albumCover) {
	t.Helper()
	f, err := a.music.Open(track.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	metadata, err := tag.ReadFrom(f)
	if err != nil || metadata.Picture() == nil {
		t.Fatalf("missing cover: %v", err)
	}
	if !bytes.Equal(metadata.Picture().Data, cover.Data) {
		t.Fatal("embedded cover differs")
	}
	w := request(a, "GET", "/api/tracks/"+track.ID+"/cover", "", false)
	if w.Code != 200 || w.Header().Get("Content-Type") != cover.MIME || !bytes.Equal(w.Body.Bytes(), cover.Data) {
		t.Fatalf("cover response failed: %d", w.Code)
	}
}

func TestAlbumEditUpdatesTagsAndCoverWhilePreservingTrackIdentityAndAudio(t *testing.T) {
	a := testApp(t)
	first := addTrack(t, a, "album/one.flac", "One", 1)
	second := addTrack(t, a, "album/two.flac", "Two", 2)
	cover := testAlbumCover(t, false)
	patch := map[string]string{"album": "New album", "album_artist": "New artist", "genre": "Jazz", "year": "2026"}
	if err := a.editAlbum(albumKey(first), patch, cover); err != nil {
		t.Fatal(err)
	}
	for _, before := range []Track{first, second} {
		after, err := get[Track](a, "tracks", before.ID)
		if err != nil {
			t.Fatal(err)
		}
		if after.ID != before.ID || after.Path != before.Path || after.Added != before.Added || after.AudioHash != before.AudioHash {
			t.Fatal("identity, path or audio changed")
		}
		if after.Tags.Album != "New album" || after.Tags.AlbumArtist != "New artist" || after.Tags.Genre != "Jazz" || after.Tags.Year != "2026" {
			t.Fatalf("album tags not applied: %+v", after.Tags)
		}
		if after.Tags.Title != before.Tags.Title || after.Tags.Artist != before.Tags.Artist || after.Tags.Track != before.Tags.Track {
			t.Fatal("track fields were overwritten")
		}
		checkTrackCover(t, a, after, cover)
	}
	ops := all[Operation](a, "operations")
	if len(ops) != 1 || ops[0].Affected != 2 {
		t.Fatal("album did not use one audit record")
	}
	updatedFirst, _ := get[Track](a, "tracks", first.ID)
	replacement := testAlbumCover(t, true)
	if err := a.editAlbum(albumKey(updatedFirst), map[string]string{}, replacement); err != nil {
		t.Fatal(err)
	}
	for _, track := range a.tracks() {
		checkTrackCover(t, a, track, replacement)
		f, _ := a.music.Open(track.Path)
		blocks, _, err := flacHeader(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		pictures := 0
		for _, block := range blocks {
			if block.kind == 6 {
				pictures++
			}
		}
		if pictures != 1 {
			t.Fatal("FLAC cover replacement duplicated picture blocks")
		}
	}

}

func TestMP3AlbumCoverOnlyPreservesAudioAndTags(t *testing.T) {
	a := testApp(t)
	v := id3.NewEmptyTag()
	v.SetTitle("Song")
	v.SetArtist("Artist")
	v.SetAlbum("Album")
	v.AddTextFrame("TRCK", id3.EncodingUTF8, "3/12")
	v.AddUserDefinedTextFrame(id3.UserDefinedTextFrame{Encoding: id3.EncodingUTF8, Description: "REPLAYGAIN_TRACK_GAIN", Value: "-3.2 dB"})
	f, err := a.music.Create("one.mp3")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = v.WriteTo(f)
	for i := 0; i < 10; i++ {
		frame := make([]byte, 417)
		copy(frame, []byte{0xff, 0xfb, 0x90, 0x00})
		_, _ = f.Write(frame)
	}
	_ = f.Close()
	if err = a.scan(); err != nil {
		t.Fatal(err)
	}
	before := a.tracks()[0]
	for _, jpegFormat := range []bool{true, false} {
		cover := testAlbumCover(t, jpegFormat)
		if err = a.editAlbum(albumKey(before), map[string]string{}, cover); err != nil {
			t.Fatal(err)
		}
		after, _ := get[Track](a, "tracks", before.ID)
		if after.Tags != before.Tags || after.AudioHash != before.AudioHash {
			t.Fatal("cover-only edit changed tags or audio")
		}
		checkTrackCover(t, a, after, cover)
		f, _ := a.music.Open(after.Path)
		parsed, err := id3.ParseReader(f, id3.Options{Parse: true})
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(parsed.GetFrames("APIC")) != 1 || len(parsed.GetFrames("TXXX")) != 1 {
			t.Fatal("replacement duplicated cover or removed custom metadata")
		}
	}
}

func TestAlbumDetailReportsMixedFieldsAndRejectsInvalidPatch(t *testing.T) {
	a := testApp(t)
	first := addTrack(t, a, "one.flac", "One", 1)
	second := addTrack(t, a, "two.flac", "Two", 2)
	if err := a.edit([]string{second.ID}, map[string]string{"genre": "Different"}); err != nil {
		t.Fatal(err)
	}
	w := request(a, "GET", "/api/albums/detail?key="+url.QueryEscape(albumKey(first)), "", false)
	var detail struct {
		Mixed []string `json:"mixed"`
		Count int      `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || detail.Count != 2 || len(detail.Mixed) != 1 || detail.Mixed[0] != "genre" {
		t.Fatalf("mixed details incorrect: %s", w.Body.String())
	}
	if err := a.editAlbum(albumKey(first), map[string]string{"title": "Wrong scope"}, nil); err == nil {
		t.Fatal("album editor accepted track field")
	}
	if err := a.editAlbum(albumKey(first), map[string]string{}, nil); err == nil {
		t.Fatal("empty edit accepted")
	}
	if err := a.editAlbum("missing", map[string]string{"genre": "Jazz"}, nil); err == nil {
		t.Fatal("missing album accepted")
	}
}

func TestAlbumMultipartUploadAndValidation(t *testing.T) {
	a := testApp(t)
	before := addTrack(t, a, "one.flac", "One", 1)
	ctx, cancel := context.WithCancel(context.Background())
	a.start(ctx)
	defer cancel()
	cover := testAlbumCover(t, false)
	upload := func(data []byte, csrf bool) *httptest.ResponseRecorder {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		_ = form.WriteField("key", albumKey(before))
		_ = form.WriteField("patch", `{"genre":"Jazz"}`)
		part, _ := form.CreateFormFile("cover", "cover.png")
		_, _ = part.Write(data)
		_ = form.Close()
		r := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/albums/edit", &body)
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Content-Type", form.FormDataContentType())
		if csrf {
			r.Header.Set(librarySessionHeader, a.librarySession)
			r.Header.Set("X-CSRF-Token", a.csrf)
			r.AddCookie(&http.Cookie{Name: a.cookieName, Value: a.csrf})
		}
		w := httptest.NewRecorder()
		a.protect(a.routes(http.NotFoundHandler())).ServeHTTP(w, r)
		return w
	}
	if w := upload(cover.Data, false); w.Code != 403 {
		t.Fatal("upload did not enforce session")
	}
	for _, bad := range [][]byte{[]byte("not a picture"), cover.Data[:24], make([]byte, maxAlbumCover+1)} {
		if w := upload(bad, true); w.Code != 400 {
			t.Fatalf("invalid picture accepted: %d", w.Code)
		}
	}
	w := upload(cover.Data, true)
	if w.Code != http.StatusAccepted {
		t.Fatalf("upload rejected: %s", w.Body.String())
	}
	var response struct {
		Job string `json:"job"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, job := range a.taskSnapshot() {
			if job.ID == response.Job && job.State == "failed" {
				t.Fatal(job.Message)
			}
			if job.ID == response.Job && job.State == "done" {
				after, _ := get[Track](a, "tracks", before.ID)
				if after.Tags.Genre != "Jazz" {
					t.Fatal("multipart tags missing")
				}
				checkTrackCover(t, a, after, cover)
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("album upload job timed out")
}
