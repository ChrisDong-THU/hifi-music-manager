package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func artistUpload(a *app, name, biography string, picture []byte, csrf bool) *httptest.ResponseRecorder {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("name", name)
	_ = form.WriteField("biography", biography)
	if picture != nil {
		part, _ := form.CreateFormFile("picture", "portrait.png")
		_, _ = part.Write(picture)
	}
	_ = form.Close()
	r := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/artists/edit", &body)
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

func TestArtistProfileUploadAndValidation(t *testing.T) {
	a := testApp(t)
	before := addTrack(t, a, "one.flac", "One", 1)
	name := before.Tags.AlbumArtist
	cover := testAlbumCover(t, false)
	if w := artistUpload(a, name, "简介", cover.Data, false); w.Code != 403 {
		t.Fatal("artist edit did not enforce session")
	}
	for _, data := range [][]byte{[]byte("invalid"), cover.Data[:24], make([]byte, maxAlbumCover+1)} {
		if w := artistUpload(a, name, "简介", data, true); w.Code != 400 {
			t.Fatalf("invalid picture accepted: %d", w.Code)
		}
	}
	if w := artistUpload(a, "missing", "简介", nil, true); w.Code != 400 {
		t.Fatal("unknown artist accepted")
	}
	if w := artistUpload(a, name, strings.Repeat("a", 16001), nil, true); w.Code != 400 {
		t.Fatal("oversized biography accepted")
	}
	if w := request(a, "GET", "/api/artists/detail?name=missing", "", false); w.Code != 404 {
		t.Fatal("unknown artist detail exists")
	}
	if w := artistUpload(a, name, "  简介第一行\n第二行  ", cover.Data, true); w.Code != 200 {
		t.Fatalf("upload failed: %s", w.Body.String())
	}
	profile := a.artistProfile(name)
	if profile.Biography != "简介第一行\n第二行" || profile.imageURL() == "" {
		t.Fatal("profile not saved")
	}
	imageURL := profile.imageURL()
	if w := request(a, "GET", imageURL, "", false); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), cover.Data) || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("portrait response incorrect")
	}
	if w := artistUpload(a, name, "更新简介", nil, true); w.Code != 200 || a.artistProfile(name).imageURL() != imageURL {
		t.Fatal("biography edit removed portrait")
	}
	w := request(a, "GET", "/api/artists/detail?name="+url.QueryEscape(name), "", false)
	var detail map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || detail["biography"] != "更新简介" || detail["image_url"] != imageURL {
		t.Fatalf("detail incorrect: %s", w.Body.String())
	}
	var artists []struct {
		Biography string `json:"biography"`
		ImageURL  string `json:"image_url"`
	}
	w = request(a, "GET", "/api/artists", "", false)
	if err := json.Unmarshal(w.Body.Bytes(), &artists); err != nil || len(artists) != 1 || artists[0].ImageURL != imageURL || artists[0].Biography != "更新简介" {
		t.Fatalf("artist list missing profile: %s", w.Body.String())
	}
	jpeg := testAlbumCover(t, true)
	if w := artistUpload(a, name, "", jpeg.Data, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	profile = a.artistProfile(name)
	if profile.Biography != "" || profile.imageURL() == imageURL {
		t.Fatal("replacement did not clear biography or invalidate image URL")
	}
	if w := request(a, "GET", profile.imageURL(), "", false); !bytes.Equal(w.Body.Bytes(), jpeg.Data) {
		t.Fatal("portrait replacement failed")
	}
	if w := request(a, "GET", imageURL, "", false); w.Code != 404 {
		t.Fatal("superseded artist picture was retained")
	}
	after, _ := get[Track](a, "tracks", before.ID)
	hash, err := fileHash(a.music, before.Path)
	if err != nil || hash != before.Hash || after != before {
		t.Fatal("artist profile edit modified music file or track metadata")
	}
}

func TestArtistProfilePersistsAcrossRestart(t *testing.T) {
	home := t.TempDir()
	cover := testAlbumCover(t, false)
	for pass := 0; pass < 2; pass++ {
		a, err := newApp(home)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer a.close()
			if pass == 0 {
				addTrack(t, a, "one.flac", "One", 1)
				if _, err := a.saveArtistProfile("测试歌手", "保存的简介", cover); err != nil {
					t.Fatal(err)
				}
				return
			}
			profile := a.artistProfile("测试歌手")
			if profile.Biography != "保存的简介" {
				t.Fatal("biography lost after restart")
			}
			if w := request(a, "GET", profile.imageURL(), "", false); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), cover.Data) {
				t.Fatal("portrait lost after restart")
			}
		}()
	}
}
