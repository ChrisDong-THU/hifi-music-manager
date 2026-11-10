package main

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"testing"
)

func expectAlbumArtwork(t *testing.T, a *app, track Track, artwork *albumCover) {
	t.Helper()
	index, err := a.readLibraryIndex()
	if err != nil {
		t.Fatal(err)
	}
	key := index.metadata[index.byTrackID[track.ID]].album
	w := request(a, "GET", "/api/albums/cover?key="+url.QueryEscape(key), "", false)
	if artwork == nil {
		if w.Code != http.StatusNotFound {
			t.Fatalf("album with no own artwork returned %d", w.Code)
		}
		return
	}
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != artwork.MIME || !bytes.Equal(w.Body.Bytes(), artwork.Data) {
		t.Fatalf("wrong album artwork: HTTP %d", w.Code)
	}
}

func writeArtworkFixture(t *testing.T, root *os.Root, name string, data []byte) {
	t.Helper()
	f, err := root.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err != nil || closeErr != nil {
		t.Fatalf("could not create artwork fixture: %v %v", err, closeErr)
	}
}

func TestDraftArtworkCannotSpreadAcrossAlbumsInPartialImports(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "First song", 128)
	addInboxFile(t, a, &b, "Second song", "", 129)
	tags := []Tags{b.Files[0].Tags, b.Files[1].Tags}
	tags[0].Album, tags[1].Album = "First album", "Second album"
	cover := testAlbumCover(t, true)
	if w := uploadDraftCover(t, a, b, tags, cover.Data, true); w.Code != http.StatusBadRequest {
		t.Fatalf("mixed draft accepted a shared cover: %d %s", w.Code, w.Body)
	}
	// A stored mixed draft from before validation must not pass the image
	// to the last remaining album when it is imported in separate commands.
	b.Candidates = []Candidate{{ID: "manual", Source: "manual", Tags: tags}}
	b.ChosenCandidate = "manual"
	if err := a.putDraft(b, cover, false); err != nil {
		t.Fatal(err)
	}
	for _, file := range b.Files {
		if err := a.importWithPolicy(b.ID, "keep", []string{file.ID}); err != nil {
			t.Fatal(err)
		}
	}
	for _, track := range a.tracks() {
		expectAlbumArtwork(t, a, track, nil)
	}
}

func TestMatchedImportKeepsExistingEmbeddedArtwork(t *testing.T) {
	for _, mode := range []string{"no artwork", "embedded artwork", "uploaded artwork"} {
		t.Run(mode, func(t *testing.T) {
			a := testApp(t)
			const existingPath = "Physical Artist/Physical Album/Disc 2/zz-existing.flac"
			existing := fixtureAlbumTrack(t, a, existingPath, "2005", 1980)
			originalArtwork, draftArtwork := testAlbumCover(t, true), testAlbumCover(t, false)
			if err := writeTags(a.music, existing.Path, existing.Tags, originalArtwork); err != nil {
				t.Fatal(err)
			}
			indexed, err := inspect(a.music, existingPath)
			if err != nil {
				t.Fatal(err)
			}
			indexed.ID, indexed.Added = existing.ID, existing.Added
			existing = indexed
			// Records without the artwork hint still resolve their embedded picture.
			existing.EmbeddedArtwork = ""
			if err := a.putTrack(existing); err != nil {
				t.Fatal(err)
			}
			b := addBatch(t, a, "aa-new-song", 1981)
			if mode == "embedded artwork" {
				source := b.Files[0]
				if err := writeTags(a.data, source.Path, source.Tags, draftArtwork); err != nil {
					t.Fatal(err)
				}
				file, err := inspect(a.data, source.Path)
				if err != nil {
					t.Fatal(err)
				}
				file.ID = source.ID
				b.Files[0] = file
				if err := a.put("batches", b.ID, b); err != nil {
					t.Fatal(err)
				}
			}
			saveAlbumDraft(t, a, b, "2005", "")
			if mode == "uploaded artwork" {
				fresh, err := get[Batch](a, "batches", b.ID)
				if err != nil {
					t.Fatal(err)
				}
				if w := uploadDraftCover(t, a, fresh, savedDraftTags(fresh), draftArtwork.Data, true); w.Code != 200 {
					t.Fatalf("could not attach draft cover: %d %s", w.Code, w.Body)
				}
			}
			if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err != nil {
				t.Fatal(err)
			}
			tracks := a.tracks()
			if len(tracks) != 2 || tracks[0].Path == existingPath {
				t.Fatalf("new track should precede the original: %+v", tracks)
			}
			for _, track := range tracks {
				if track.Path == existingPath && track.Hash != existing.Hash {
					t.Fatal("import rewrote existing music")
				}
				checkTrackCover(t, a, track, originalArtwork)
			}
			expectAlbumArtwork(t, a, tracks[0], originalArtwork)
		})
	}
}

func TestMatchedImportInheritsSidecarWithoutChangingWebDAVArtwork(t *testing.T) {
	a := testApp(t)
	existing := fixtureAlbumTrack(t, a, "Artist/Album/Disc 2/existing.flac", "2005", 1983)
	cover := testAlbumCover(t, true)
	sidecar := path.Join(path.Dir(existing.Path), "FOLDER.JPG")
	writeArtworkFixture(t, a.music, sidecar, cover.Data)
	expectAlbumArtwork(t, a, existing, cover)
	b := addBatch(t, a, "another song", 1984)
	saveAlbumDraft(t, a, b, "2005", "")
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	for _, track := range a.tracks() {
		if track.ID != existing.ID {
			checkTrackCover(t, a, track, cover)
		}
	}
	w := httptest.NewRecorder()
	a.serveDAV(w, httptest.NewRequest("GET", "/webdav/"+(&url.URL{Path: sidecar}).EscapedPath(), nil))
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), cover.Data) {
		t.Fatalf("WebDAV sidecar changed or disappeared: %d", w.Code)
	}
}

func TestAlbumCoverDoesNotAssignSharedDirectoryArtworkToUnrelatedReleases(t *testing.T) {
	a := testApp(t)
	first := fixtureAlbumTrack(t, a, "Shared/one.flac", "2005", 1985)
	second := addTrack(t, a, "Shared/two.flac", "Unrelated song", 1986)
	cover := testAlbumCover(t, true)
	writeArtworkFixture(t, a.music, "Shared/cover.jpg", cover.Data)
	expectAlbumArtwork(t, a, first, nil)
	expectAlbumArtwork(t, a, second, nil)
}

func TestMatchedImportKeepsGIFAlbumArtworkAndAllowsExplicitAlbumEdit(t *testing.T) {
	a := testApp(t)
	existing := fixtureAlbumTrack(t, a, "Artist/Album/existing.flac", "2005", 1987)
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.White, color.Black})
	var gifData bytes.Buffer
	if err := gif.Encode(&gifData, img, nil); err != nil {
		t.Fatal(err)
	}
	cover := readableArtwork(gifData.Bytes())
	writeArtworkFixture(t, a.music, "Artist/Album/cover.gif", cover.Data)
	b := addBatch(t, a, "another song", 1988)
	track := b.Files[0]
	other := testAlbumCover(t, false)
	if err := writeTags(a.data, track.Path, track.Tags, other); err != nil {
		t.Fatal(err)
	}
	file, err := inspect(a.data, track.Path)
	if err != nil {
		t.Fatal(err)
	}
	file.ID = track.ID
	b.Files[0] = file
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	saveAlbumDraft(t, a, b, "2005", "")
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	if err := a.scan(); err != nil {
		t.Fatal(err)
	}
	expectAlbumArtwork(t, a, existing, cover)
	for _, item := range a.tracks() {
		if item.ID != existing.ID {
			checkTrackCover(t, a, item, other)
		}
	}
	if err := editAlbumForTest(a, albumKey(existing), nil, other); err != nil {
		t.Fatal(err)
	}
	expectAlbumArtwork(t, a, existing, other)
}

func TestMatchedImportLeavesReadOnlyAudioUnchanged(t *testing.T) {
	a := testApp(t)
	existing := addTrack(t, a, "Album/existing.flac", "original", 1989)
	cover := testAlbumCover(t, true)
	if err := writeTags(a.music, existing.Path, Tags{}, cover); err != nil {
		t.Fatal(err)
	}
	if err := a.scan(); err != nil {
		t.Fatal(err)
	}
	b := addBatch(t, a, "readonly", 1990)
	name := path.Join(path.Dir(b.Files[0].Path), "track.wav")
	// One mono 16-bit PCM sample at 44100 Hz.
	wave := []byte("RIFF\x26\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x44\xac\x00\x00\x88\x58\x01\x00\x02\x00\x10\x00data\x02\x00\x00\x00\x01\x00")
	writeArtworkFixture(t, a.data, name, wave)
	file, err := inspect(a.data, name)
	if err != nil || file.Editable {
		t.Fatalf("expected a read-only source: %v", err)
	}
	b.Files = []Track{file}
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	tracks := a.tracks()
	if len(tracks) != 2 {
		t.Fatalf("expected two tracks, found %d", len(tracks))
	}
	for _, track := range tracks {
		if track.Format != "WAV" {
			continue
		}
		if track.Hash != file.Hash || path.Dir(track.Path) != "Album" {
			t.Fatal("read-only track changed or did not match its album")
		}
		w := request(a, "GET", "/api/tracks/"+track.ID+"/cover", "", false)
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), cover.Data) {
			t.Fatalf("read-only track could not use album cover: %d", w.Code)
		}
	}
}
