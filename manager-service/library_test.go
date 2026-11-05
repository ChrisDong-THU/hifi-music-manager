package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"fmt"

	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	id3 "github.com/bogem/id3v2/v2"
)

// A small, self-contained FLAC encoder for test tones. Verbatim subframes,
// frame CRCs and PCM MD5 are valid; no external encoder or Python is needed.
func toneFLAC(title string, seed int16) []byte {
	const frames, block = 20, 256
	var audio, pcm bytes.Buffer
	crc8 := func(b []byte) byte {
		var crc byte
		for _, v := range b {
			crc ^= v
			for i := 0; i < 8; i++ {
				if crc&128 != 0 {
					crc = crc<<1 ^ 7
				} else {
					crc <<= 1
				}
			}
		}
		return crc
	}
	crc16 := func(b []byte) uint16 {
		var crc uint16
		for _, v := range b {
			crc ^= uint16(v) << 8
			for i := 0; i < 8; i++ {
				if crc&0x8000 != 0 {
					crc = crc<<1 ^ 0x8005
				} else {
					crc <<= 1
				}
			}
		}
		return crc
	}
	for n := 0; n < frames; n++ {
		var frame bytes.Buffer
		h := []byte{0xff, 0xf8, 0x89, 0x08, byte(n)}
		frame.Write(h)
		frame.WriteByte(crc8(h))
		frame.WriteByte(2)
		for s := 0; s < block; s++ {
			sample := seed + int16(s%31)
			_ = binary.Write(&frame, binary.BigEndian, sample)
			_ = binary.Write(&pcm, binary.LittleEndian, sample)
		}
		_ = binary.Write(&frame, binary.BigEndian, crc16(frame.Bytes()))
		audio.Write(frame.Bytes())
	}
	info := make([]byte, 34)
	binary.BigEndian.PutUint16(info[0:2], block)
	binary.BigEndian.PutUint16(info[2:4], block)
	binary.BigEndian.PutUint64(info[10:18], uint64(44100)<<44|uint64(15)<<36|frames*block)
	m := md5.Sum(pcm.Bytes())
	copy(info[18:], m[:])
	var comments bytes.Buffer
	_ = binary.Write(&comments, binary.LittleEndian, uint32(4))
	comments.WriteString("test")
	_ = binary.Write(&comments, binary.LittleEndian, uint32(0))
	vc, _ := writeComments(comments.Bytes(), Tags{Title: title, Artist: "测试歌手", Album: "测试专辑", AlbumArtist: "测试歌手", Year: "2024", Track: "1"})
	var out bytes.Buffer
	out.WriteString("fLaC")
	out.Write([]byte{0, 0, 0, 34})
	out.Write(info)
	out.Write([]byte{0x84, byte(len(vc) >> 16), byte(len(vc) >> 8), byte(len(vc))})
	out.Write(vc)
	out.Write(audio.Bytes())
	return out.Bytes()
}
func testApp(t *testing.T) *app {
	t.Helper()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.close)
	return a
}
func addTrack(t *testing.T, a *app, p, title string, seed int16) Track {
	t.Helper()
	if err := ensureDir(a.music, filepath.Dir(p)); err != nil {
		t.Fatal(err)
	}
	f, err := a.music.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(toneFLAC(title, seed))
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.scan(); err != nil {
		t.Fatal(err)
	}
	for _, x := range a.tracks() {
		if x.Path == p {
			return x
		}
	}
	t.Fatal("missing track")
	return Track{}
}
func addBatch(t *testing.T, a *app, title string, seed int16) Batch {
	t.Helper()
	b := Batch{ID: uid(), Name: title, Created: now(), State: "ready"}
	b.UploadOwner = b.ID
	p := "uploads/" + b.ID + "/" + title + ".flac"
	if err := ensureDir(a.data, filepath.Dir(p)); err != nil {
		t.Fatal(err)
	}
	f, err := a.data.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(toneFLAC(title, seed))
	_ = f.Close()
	track, err := inspect(a.data, p)
	if err != nil {
		t.Fatal(err)
	}
	b.Files = []Track{track}
	b.Candidates = []Candidate{originalCandidate(b)}
	if err = a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFLACTagsPreserveAudioAndCustomMetadata(t *testing.T) {
	a := testApp(t)
	track := addTrack(t, a, "歌手/专辑/01.flac", "原始标题", 100)
	f, _ := a.music.Open(track.Path)
	blocks, _, err := flacHeader(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	originalStreamInfo := append([]byte(nil), blocks[0].data...)
	if err = a.edit([]string{track.ID}, map[string]string{"title": "新标题 / 中文", "lyrics": "第一行\n第二行", "track": "2/12", "year": "2025"}); err != nil {
		t.Fatal(err)
	}
	updated, err := get[Track](a, "tracks", track.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AudioHash != track.AudioHash || updated.Hash == track.Hash || updated.Path != track.Path {
		t.Fatal("audio changed, metadata unchanged, or original renamed")
	}
	if updated.Tags.Title != "新标题 / 中文" || updated.Tags.Lyrics != "第一行\n第二行" {
		t.Fatalf("tags not written: %+v", updated.Tags)
	}
	f, _ = a.music.Open(track.Path)
	blocks, _, err = flacHeader(f)
	_ = f.Close()
	if err != nil || !bytes.Equal(blocks[0].data, originalStreamInfo) {
		t.Fatal("STREAMINFO changed")
	}
	ops := all[Operation](a, "operations")
	if len(ops) != 1 || ops[0].State != "done" {
		t.Fatal("missing committed operation")
	}

}
func TestImportDuplicateProtectionAndSourceCleanup(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "单曲", 200)
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	tracks := a.tracks()
	if len(tracks) != 1 {
		t.Fatal("missing imported track")
	}
	if tracks[0].AudioHash != b.Files[0].AudioHash {
		t.Fatal("import changed audio")
	}
	if _, err := a.data.Stat(b.Files[0].Path); !os.IsNotExist(err) {
		t.Fatal("imported source not removed")
	}
	duplicate := addBatch(t, a, "同音频不同标签", 200)
	if err := a.importWithPolicy(duplicate.ID, "reject", fileIDs(duplicate.Files)); err == nil {
		t.Fatal("duplicate audio not rejected")
	}
	if len(a.tracks()) != 1 {
		t.Fatal("duplicate changed library")
	}
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err == nil {
		t.Fatal("imported batch was imported twice")
	}

}
func TestPermanentDeletionAndExternalChangeGuard(t *testing.T) {
	a := testApp(t)
	track := addTrack(t, a, "one.flac", "曲目", 300)
	if err := a.deleteTracks([]string{track.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.music.Stat(track.Path); !os.IsNotExist(err) {
		t.Fatal("file not recycled")
	}
	track = addTrack(t, a, "outside.flac", "external", 301)
	f, _ := a.music.OpenFile(track.Path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.Write([]byte("outside change"))
	_ = f.Close()
	if err := a.edit([]string{track.ID}, map[string]string{"title": "overwrite"}); err == nil {
		t.Fatal("overwrote external modification")
	}
	if err := a.deleteTracks([]string{track.ID}); err == nil {
		t.Fatal("deleted external modification")
	}
}
func TestPathsAndSymlinks(t *testing.T) {
	for _, p := range []string{"../escape.flac", "C:/escape.flac", "/tmp/escape.flac", "ok/../../escape.flac", "ok/.hidden/a.flac", "a\\..\\b.flac", "a\x00.flac"} {
		if _, err := cleanRelative(p); err == nil {
			t.Errorf("accepted %q", p)
		}
	}
	a := testApp(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(a.music.Name(), "link")); err != nil {
		t.Log("symlinks unavailable on this host")
		return
	}
	if err := ensureDir(a.music, "link/child"); err == nil {
		t.Fatal("followed symlink")
	}
}

func request(a *app, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	if strings.HasPrefix(r.URL.Path, "/api/drafts/") && (strings.HasSuffix(r.URL.Path, "/tags") || strings.HasSuffix(r.URL.Path, "/import") || strings.HasSuffix(r.URL.Path, "/delete")) {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) == 5 {
			if b, err := get[Batch](a, "batches", parts[3]); err == nil {
				r.Header.Set("If-Match", draftEditToken(b))
			}
		}
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
func TestHTTPUploadValidationAndOriginGuard(t *testing.T) {
	a := testApp(t)
	if w := request(a, "POST", "/api/drafts", `{"name":"Album"}`, false); w.Code != 403 {
		t.Fatalf("missing csrf accepted: %d", w.Code)
	}
	w := request(a, "POST", "/api/drafts", `{"name":"Album"}`, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var b Batch
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if w = request(a, "POST", "/api/drafts/"+b.ID+"/files?path=bad.flac", "not audio", true); w.Code != 400 {
		t.Fatalf("invalid audio accepted: %d", w.Code)
	}
	if w = request(a, "POST", "/api/drafts/"+b.ID+"/files?path=..%2Fescape.flac", string(toneFLAC("test", 5)), true); w.Code != 400 {
		t.Fatalf("path traversal accepted: %d", w.Code)
	}
	if w = request(a, "POST", "/api/drafts/"+b.ID+"/files?path=one.flac", string(toneFLAC("test", 5)), true); w.Code != 200 {
		t.Fatalf("upload failed: %s", w.Body.String())
	}
	if w = request(a, "POST", "/api/drafts/"+b.ID+"/files?path=one.flac", string(toneFLAC("test", 5)), true); w.Code != 200 {
		t.Fatalf("identical upload retry failed: %d", w.Code)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/scan", nil)
	r.RemoteAddr = "127.0.0.1:2345"
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("X-CSRF-Token", a.csrf)
	r.AddCookie(&http.Cookie{Name: a.cookieName, Value: a.csrf})
	w = httptest.NewRecorder()
	a.protect(a.routes(http.NotFoundHandler())).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	r = httptest.NewRequest("GET", "http://evil.example/api/state", nil)
	r.RemoteAddr = "127.0.0.1:2345"
	w = httptest.NewRecorder()
	a.protect(a.routes(http.NotFoundHandler())).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("DNS rebinding host accepted")
	}
}
func TestHTTPRangeServesOriginalAudio(t *testing.T) {
	a := testApp(t)
	track := addTrack(t, a, "音频.flac", "试听", 5)
	r := httptest.NewRequest("GET", "http://127.0.0.1:8787/api/tracks/"+track.ID+"/audio", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	a.protect(a.routes(http.NotFoundHandler())).ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "fLaC" {
		t.Fatalf("range failed: %d %s", w.Code, w.Body.String())
	}
}

func TestPersistenceAndInterruptedJobRecovery(t *testing.T) {
	home := t.TempDir()
	a, err := newApp(home)
	if err != nil {
		t.Fatal(err)
	}
	b := addBatch(t, a, "持久化", 33)
	b.State = "importing"
	_ = a.put("batches", b.ID, b)
	a.close()
	a, err = newApp(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	b, err = get[Batch](a, "batches", b.ID)
	if err != nil || b.State != "ready" || len(b.Files) != 1 {
		t.Fatal("restart lost batch")
	}
}

func TestMP3EditPreservesAudioAndUnselectedFrames(t *testing.T) {
	a := testApp(t)
	v := id3.NewEmptyTag()
	v.SetDefaultEncoding(id3.EncodingUTF8)
	v.SetTitle("MP3 原标题")
	v.SetArtist("多位歌手")
	v.SetAlbum("MP3 测试专辑")
	v.AddTextFrame("TDRC", id3.EncodingUTF8, "2024-08-23")
	v.AddTextFrame("TRCK", id3.EncodingUTF8, "3/12")
	v.AddUserDefinedTextFrame(id3.UserDefinedTextFrame{Encoding: id3.EncodingUTF8, Description: "REPLAYGAIN_TRACK_GAIN", Value: "-3.2 dB"})
	f, err := a.music.Create("test.mp3")
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
	track := a.tracks()[0]
	if err = a.edit([]string{track.ID}, map[string]string{"title": "MP3 新标题"}); err != nil {
		t.Fatal(err)
	}
	updated, _ := get[Track](a, "tracks", track.ID)
	if updated.AudioHash != track.AudioHash || updated.Tags.Title != "MP3 新标题" || updated.Tags.Track != "3/12" {
		t.Fatalf("MP3 validation failed: %+v", updated)
	}
	f, _ = a.music.Open(track.Path)
	parsed, err := id3.ParseReader(f, id3.Options{Parse: true})
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if frame := parsed.GetTextFrame("TDRC"); frame.Text != "2024-08-23" {
		t.Fatal("unselected date changed")
	}
	if parsed.GetLastFrame("TXXX") == nil {
		t.Fatal("custom frame lost")
	}

}

func TestFLACUnselectedCommentsPreserved(t *testing.T) {
	var comments bytes.Buffer
	write := func(s string) {
		_ = binary.Write(&comments, binary.LittleEndian, uint32(len(s)))
		comments.WriteString(s)
	}
	write("custom")
	_ = binary.Write(&comments, binary.LittleEndian, uint32(5))
	for _, s := range []string{"TITLE=old", "DATE=2024-08-23", "ARTIST=one", "ARTIST=two", "REPLAYGAIN_TRACK_GAIN=-3 dB"} {
		write(s)
	}
	updated, err := writeComments(comments.Bytes(), Tags{Title: "new"}, map[string]string{"title": "new"})
	if err != nil {
		t.Fatal(err)
	}
	vendor, cs, err := parseComments(updated)
	if err != nil || vendor != "custom" {
		t.Fatal("vendor changed")
	}
	joined := strings.Join(cs, "\n")
	for _, expected := range []string{"TITLE=new", "DATE=2024-08-23", "ARTIST=one", "ARTIST=two", "REPLAYGAIN_TRACK_GAIN=-3 dB"} {
		if !strings.Contains(joined, expected) {
			t.Error("lost", expected)
		}
	}
}

func TestSeparateLocalInstancesUseSeparateSessions(t *testing.T) {
	a := testApp(t)
	b := testApp(t)
	if a.cookieName == b.cookieName {
		t.Fatal("local instances share a session cookie")
	}
	for _, x := range []*app{a, b} {
		w := request(x, "GET", "/api/state", "", false)
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != x.cookieName {
			t.Fatal("incorrect session cookie")
		}
	}
}

func TestQueuedMutationAndProgressSnapshots(t *testing.T) {
	a := testApp(t)
	track := addTrack(t, a, "queued.flac", "queued", 55)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.start(ctx)
	done := make(chan error, 1)
	_, err := a.submit("edit", func() error {
		e := a.edit([]string{track.ID}, map[string]string{"title": "queued update"})
		done <- e
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	go func() {
		defer close(stop)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				_ = a.taskSnapshot()
				_ = a.tracks()
			}
		}
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("queued update timed out")
	}
	cancel()
	<-stop
}

func addInboxFile(t *testing.T, a *app, b *Batch, name, album string, seed int16) {
	t.Helper()
	p := "uploads/" + b.ID + "/" + name + ".flac"
	f, err := a.data.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(toneFLAC(name, seed))
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	track, err := inspect(a.data, p)
	if err != nil {
		t.Fatal(err)
	}
	if album != "" {
		tags := track.Tags
		tags.Album = album
		if err = writeTags(a.data, p, tags, nil, map[string]string{"album": album}); err != nil {
			t.Fatal(err)
		}
		track, err = inspect(a.data, p)
		if err != nil {
			t.Fatal(err)
		}
	}
	b.Files = append(b.Files, track)
	b.Candidates = []Candidate{originalCandidate(*b)}
	if err = a.put("batches", b.ID, *b); err != nil {
		t.Fatal(err)
	}
}

func TestAlbumTasksPreserveSourcesAndIdentifiers(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "album-one", 100)
	addInboxFile(t, a, &b, "album-two", "另一张专辑", 200)
	ids, err := a.splitAlbums(b.ID)
	if err != nil || len(ids) != 2 {
		t.Fatalf("split failed: %v %v", ids, err)
	}
	again, err := a.splitAlbums(b.ID)
	if err != nil || strings.Join(ids, ",") != strings.Join(again, ",") {
		t.Fatal("group identities changed")
	}
	for _, f := range b.Files {
		hash, e := fileHash(a.data, f.Path)
		if e != nil || hash != f.Hash {
			t.Fatal("grouping changed source")
		}
	}
	parent, _ := get[Batch](a, "batches", b.ID)
	if parent.ID != b.ID || parent.State != "grouped" {
		t.Fatal("original batch identity lost")
	}
	for _, id := range ids {
		child, e := get[Batch](a, "batches", id)
		if e != nil || child.ParentID != b.ID || len(child.Files) != 1 {
			t.Fatal("invalid album task")
		}
	}
}

func TestDuplicateSkipKeepsExistingMusic(t *testing.T) {
	a := testApp(t)
	existing := addTrack(t, a, "existing.flac", "existing", 100)
	b := addBatch(t, a, "duplicate", 100)
	addInboxFile(t, a, &b, "new", "", 200)
	if err := a.importWithPolicy(b.ID, "skip", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	if len(a.tracks()) != 2 {
		t.Fatal("duplicate not skipped")
	}
	hash, _ := fileHash(a.music, existing.Path)
	if hash != existing.Hash {
		t.Fatal("existing file overwritten")
	}
	ops := all[Operation](a, "operations")
	if ops[0].Skipped != 1 || ops[0].Affected != 1 {
		t.Fatal("incorrect skip journal")
	}

}

func TestOfflinePreparePersistsPreviewAndSelection(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "offline", 100)
	if err := a.prepare(b.ID); err != nil {
		t.Fatal(err)
	}
	fresh, _ := get[Batch](a, "batches", b.ID)
	if !fresh.Previewed || fresh.ChosenCandidate != "original" || len(fresh.Candidates) != 0 || fresh.State == "imported" || len(a.tracks()) != 0 {
		t.Fatal("preview not persisted")
	}
	encoded, _ := json.Marshal(map[string]any{"tags": []Tags{fresh.Files[0].Tags}})
	if w := request(a, "POST", "/api/drafts/"+b.ID+"/tags", string(encoded), true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	fresh, _ = get[Batch](a, "batches", b.ID)
	if fresh.ChosenCandidate != "manual" {
		t.Fatal("candidate selection lost")
	}
}

func TestLocalDraftManualEditAndImport(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Local draft", 150)
	file := b.Files[0]
	manual := file.Tags
	manual.Title, manual.Album = "手动标题", "手动专辑"
	body, _ := json.Marshal(map[string]any{"tags": []Tags{manual}})
	w := request(a, "POST", "/api/drafts/"+b.ID+"/tags", string(body), true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	fresh, _ := get[Batch](a, "batches", b.ID)
	if fresh.ChosenCandidate != "manual" || len(fresh.Candidates) != 1 {
		t.Fatal("manual draft not saved")
	}
	if hash, _ := fileHash(a.data, file.Path); hash != file.Hash {
		t.Fatal("saving draft modified source")
	}
	if err := a.importWithPolicy(b.ID, "skip", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	ts := a.tracks()
	if len(ts) != 1 || ts[0].Tags.Title != manual.Title || ts[0].AudioHash != file.AudioHash {
		t.Fatal("manual import lost tags or audio")
	}
	if err := a.localPreview(b.ID); err == nil {
		t.Fatal("imported batch became editable")
	}
}

func TestAlbumGroupingKeepsEditionsAndJoinsDiscFolders(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "first", 100)
	addInboxFile(t, a, &b, "second", "", 200)
	addInboxFile(t, a, &b, "third", "", 300)
	for i, folder := range []string{"edition-a/CD1", "edition-a/CD2", "edition-b"} {
		source := b.Files[i].Path
		target := filepath.Join("uploads", b.ID, folder, filepath.Base(source))
		if err := ensureDir(a.data, filepath.Dir(target)); err != nil {
			t.Fatal(err)
		}
		if err := a.data.Rename(source, target); err != nil {
			t.Fatal(err)
		}
		b.Files[i].Path = filepath.ToSlash(target)
	}
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	ids, err := a.splitAlbums(b.ID)
	if err != nil || len(ids) != 2 {
		t.Fatalf("album editions merged: %v %v", ids, err)
	}
	counts := map[int]int{}
	for _, id := range ids {
		child, _ := get[Batch](a, "batches", id)
		counts[len(child.Files)]++
	}
	if counts[2] != 1 || counts[1] != 1 {
		t.Fatalf("disc folders split: %v", counts)
	}
}

func TestSearchFiltersUseExactAlbumIdentity(t *testing.T) {
	a := testApp(t)
	one := addTrack(t, a, "one.flac", "one", 100)
	two := addTrack(t, a, "two.flac", "two", 200)
	if err := a.edit([]string{two.ID}, map[string]string{"album": "测试专辑 加长版", "artist": "另一歌手", "album_artist": "另一歌手"}); err != nil {
		t.Fatal(err)
	}
	w := request(a, "GET", "/api/tracks?album="+url.QueryEscape(albumKey(one)), "", false)
	var result struct {
		Tracks []Track `json:"tracks"`
		Total  int     `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Tracks[0].ID != one.ID {
		t.Fatalf("album filter leaked: %s", w.Body.String())
	}
	w = request(a, "GET", "/api/tracks?artist="+url.QueryEscape("另一歌手"), "", false)
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Tracks[0].ID != two.ID {
		t.Fatal("artist filter leaked")
	}
}

func TestAlbumSortModesAndPagination(t *testing.T) {
	a := testApp(t)
	fixtures := []Track{
		{ID: "a", Tags: Tags{Artist: "Zed", Album: "Alpha", Year: "2020"}, Added: "2025-01-01"},
		{ID: "b", Tags: Tags{Artist: "able", Album: "beta", Year: "2023-05-01"}, Added: "2022-01-01"},
		{ID: "c", Tags: Tags{Artist: "Middle", Album: "Gamma", Year: "1999"}, Added: "2024-01-01"},
		{ID: "d", Tags: Tags{Artist: "X", Album: "Delta", Year: "unknown"}, Added: "2023-01-01"},
	}
	for _, track := range fixtures {
		if err := a.putTrack(track); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		mode string
		want string
	}{
		{"recent", "Alpha,Gamma,Delta,beta"},
		{"title", "Alpha,beta,Delta,Gamma"},
		{"artist", "beta,Gamma,Delta,Alpha"},
		{"year", "beta,Alpha,Gamma,Delta"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			w := request(a, "GET", "/api/albums?sort="+tc.mode, "", false)
			var result struct {
				Albums []Album `json:"albums"`
			}
			if w.Code != 200 {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			titles := []string{}
			for _, album := range result.Albums {
				titles = append(titles, album.Title)
			}
			if got := strings.Join(titles, ","); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
	for i := 0; i < 50; i++ {
		track := Track{ID: fmt.Sprintf("page-%02d", i), Tags: Tags{Artist: fmt.Sprintf("Artist %02d", 49-i), Album: fmt.Sprintf("Page %02d", i)}}
		if err := a.putTrack(track); err != nil {
			t.Fatal(err)
		}
	}
	w := request(a, "GET", "/api/albums?q=Page&sort=title&page=1", "", false)
	var page struct {
		Albums []Album `json:"albums"`
		Total  int     `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 50 || len(page.Albums) != 2 || page.Albums[0].Title != "Page 48" || page.Albums[1].Title != "Page 49" {
		t.Fatalf("sorting must precede pagination: %s", w.Body.String())
	}
}

func TestAlbumTracksOrderByDiscAndTrackBeforePagination(t *testing.T) {
	a := testApp(t)
	for _, track := range []Track{
		{ID: "a", Tags: Tags{Artist: "Artist", Album: "Album", Disc: "2/2", Track: "1/10"}},
		{ID: "b", Tags: Tags{Artist: "Artist", Album: "Album", Disc: "1", Track: "10/10"}},
		{ID: "c", Tags: Tags{Artist: "Artist", Album: "Album", Track: "2"}},
		{ID: "d", Tags: Tags{Artist: "Artist", Album: "Album", Track: "1"}},
		{ID: "e", Tags: Tags{Artist: "Artist", Album: "Album", Track: ""}},
		{ID: "f", Tags: Tags{Artist: "Other", Album: "Album", Track: "1"}},
	} {
		if err := a.putTrack(track); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ page, want string }{{"0", "d,c"}, {"1", "b,e"}, {"2", "a"}} {
		w := request(a, "GET", "/api/tracks?sort=track&limit=2&page="+tc.page+"&album="+url.QueryEscape("Artist\x00Album"), "", false)
		var result struct {
			Tracks []Track `json:"tracks"`
			Total  int     `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, track := range result.Tracks {
			ids = append(ids, track.ID)
		}
		if result.Total != 5 || strings.Join(ids, ",") != tc.want {
			t.Fatalf("page %s: %s", tc.page, w.Body.String())
		}
	}
}
