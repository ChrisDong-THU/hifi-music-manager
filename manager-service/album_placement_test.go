package main

import (
	"encoding/json"
	"net/url"
	"path"
	"slices"
	"strings"
	"testing"
)

func fixtureAlbumTrack(t *testing.T, a *app, filename, year string, seed int16) Track {
	t.Helper()
	return fixtureReleaseTrack(t, a, filename, "Shared Album", year, "2", seed)
}

func fixtureReleaseTrack(t *testing.T, a *app, filename, album, year, disc string, seed int16) Track {
	t.Helper()
	track := addTrack(t, a, filename, "Published", seed)
	tags := track.Tags
	tags.Album, tags.AlbumArtist, tags.Year, tags.Disc = album, "Alice & Bob", year, disc
	if err := writeTags(a.music, filename, tags, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.scan(); err != nil {
		t.Fatal(err)
	}
	for _, track := range a.tracks() {
		if track.Path == filename {
			return track
		}
	}
	t.Fatal("album fixture was not indexed")
	return Track{}
}

func saveAlbumDraft(t *testing.T, a *app, b Batch, year, match string) {
	t.Helper()
	tags := make([]Tags, len(b.Files))
	for i, file := range b.Files {
		tags[i] = file.Tags
		tags[i].Album, tags[i].AlbumArtist, tags[i].Year, tags[i].Disc = "shared  album", "bob ＆ ALICE", year, "2"
	}
	saveDraftAlbumTags(t, a, b, tags, match)
}

func saveDraftAlbumTags(t *testing.T, a *app, b Batch, tags []Tags, match string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"tags": tags, "album_match": match})
	if err != nil {
		t.Fatal(err)
	}
	if w := request(a, "POST", "/api/drafts/"+b.ID+"/tags", string(raw), true); w.Code != 200 {
		t.Fatalf("saving draft: %d %s", w.Code, w.Body)
	}
}

func TestImportKeepsDiscNamedAlbumInItsOwnDirectory(t *testing.T) {
	a := testApp(t)
	fixtureReleaseTrack(t, a, "Artist/CD1/existing.flac", "CD1", "2005", "1", 1780)
	b := addBatch(t, a, "New song", 1781)
	tags := b.Files[0].Tags
	tags.Album, tags.AlbumArtist, tags.Year, tags.Disc = "CD1", "Alice & Bob", "2005", "2"
	saveDraftAlbumTags(t, a, b, []Tags{tags}, "")
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	if tracks := a.tracks(); len(tracks) != 2 || path.Dir(tracks[0].Path) != "Artist/CD1" || path.Dir(tracks[1].Path) != "Artist/CD1" {
		t.Fatalf("tracks left the CD1 album directory: %+v", tracks)
	}
}

func TestMatchedImportDistinguishesFlatTracksAndDiscFolders(t *testing.T) {
	a := testApp(t)
	fixtureReleaseTrack(t, a, "Artist/Release/first.flac", "Shared Album", "2005", "1", 1782)
	second := fixtureAlbumTrack(t, a, "Artist/Release/Disc 2/second.flac", "2005", 1783)
	b := addBatch(t, a, "New song", 1784)
	tags := b.Files[0].Tags
	tags.Album, tags.AlbumArtist, tags.Year = "Shared Album", "Alice & Bob", "2005"
	saveDraftAlbumTags(t, a, b, []Tags{tags}, "")
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err == nil || !strings.Contains(err.Error(), "填写所选曲目的碟号") {
		t.Fatalf("import guessed the disc of a track: %v", err)
	}
	if err := a.music.Remove(second.Path); err != nil {
		t.Fatal(err)
	}
	tags.Disc = "2"
	saveDraftAlbumTags(t, a, b, []Tags{tags}, "")
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err == nil || !strings.Contains(err.Error(), "重新扫描") {
		t.Fatalf("import accepted a stale disc location: %v", err)
	}
	if err := a.music.WriteFile(second.Path, toneFLAC("File replaced outside the manager", 1790), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err == nil || !strings.Contains(err.Error(), "重新扫描") {
		t.Fatalf("import treated an unrelated replacement file as an album member: %v", err)
	}
	tags.Disc = "1"
	saveDraftAlbumTags(t, a, b, []Tags{tags}, "")
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	for _, track := range a.tracks() {
		if track.Tags.Title == tags.Title {
			if path.Dir(track.Path) != "Artist/Release" {
				t.Fatalf("first disc belongs in the flat album directory: %s", track.Path)
			}
			return
		}
	}
	t.Fatal("missing imported track")
}

func TestMatchedImportPreservesOriginalDateInMatchingYear(t *testing.T) {
	const fullDate = "2005-01-17"
	a := testApp(t)
	fixtureAlbumTrack(t, a, "Artist/Release/first.flac", fullDate, 1785)
	b := addBatch(t, a, "New song", 1786)
	tags := b.Files[0].Tags
	tags.Album, tags.AlbumArtist, tags.Year = "Shared Album", "Alice & Bob", fullDate
	if err := writeTags(a.data, b.Files[0].Path, tags, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.localPreview(b.ID); err != nil {
		t.Fatal(err)
	}
	b, err := get[Batch](a, "batches", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	tags = b.Files[0].Tags
	tags.Title = "Edited title"
	saveDraftAlbumTags(t, a, b, []Tags{tags}, "")
	q := url.Values{"title": {tags.Album}, "artist": {tags.AlbumArtist}, "year": {fullDate}}
	w := request(a, "GET", "/api/albums/matches?"+q.Encode(), "", false)
	var result struct {
		Matches        []albumPlacement `json:"matches"`
		RequiresChoice bool             `json:"requires_choice"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.RequiresChoice ||
		len(result.Matches) != 1 || result.Matches[0].Year != "2005" {
		t.Fatalf("a full date and its year should match: %d %s", w.Code, w.Body)
	}
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	for _, track := range a.tracks() {
		if track.Tags.Title == tags.Title {
			if path.Dir(track.Path) != "Artist/Release" {
				t.Fatalf("matching chose a new directory: %s", track.Path)
			}
			file, err := a.music.Open(track.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			blocks, _, err := flacHeader(file)
			if err != nil {
				t.Fatal(err)
			}
			for _, block := range blocks {
				if block.kind == 4 {
					_, comments, err := parseComments(block.data)
					if err != nil || !slices.Contains(comments, "DATE="+fullDate) {
						t.Fatalf("import discarded the full date: %v %v", comments, err)
					}
					return
				}
			}
			t.Fatal("missing Vorbis comment block")
		}
	}
	t.Fatal("missing imported track")
}

func TestUndatedMatchDoesNotSilentlyMergeTwoEditions(t *testing.T) {
	a := testApp(t)
	fixtureAlbumTrack(t, a, "Artist/Release/first.flac", "", 1787)
	b := addBatch(t, a, "New song", 1788)
	addInboxFile(t, a, &b, "Other edition", "", 1789)
	tags := []Tags{b.Files[0].Tags, b.Files[1].Tags}
	for i := range tags {
		tags[i].Album, tags[i].AlbumArtist = "Shared Album", "Alice & Bob"
	}
	tags[0].Year, tags[1].Year = "2005", "2024"
	saveDraftAlbumTags(t, a, b, tags, "")
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err == nil || !strings.Contains(err.Error(), "多个发行年份") || len(a.tracks()) != 1 {
		t.Fatalf("an undated release accepted multiple incoming editions: %v", err)
	}
}

func TestDraftAlbumMatchesCanonicalTagsAndExistingDiscDirectory(t *testing.T) {
	a := testApp(t)
	original := fixtureAlbumTrack(t, a, "Physical Artist/Physical Album/Disc 2/existing.flac", "2005", 1771)
	b := addBatch(t, a, "Additional song", 1772)
	saveAlbumDraft(t, a, b, "2005", "")
	q := url.Values{"title": {" SHARED  ALBUM "}, "artist": {"bob ＆ ALICE"}, "year": {"2005"}}
	w := request(a, "GET", "/api/albums/matches?"+q.Encode(), "", false)
	var result struct {
		Matches        []albumPlacement `json:"matches"`
		RequiresChoice bool             `json:"requires_choice"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.RequiresChoice ||
		len(result.Matches) != 1 || result.Matches[0].Location != "Physical Artist/Physical Album" {
		t.Fatalf("unexpected match: %d %s", w.Code, w.Body)
	}
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	for _, track := range a.tracks() {
		if track.ID == original.ID {
			if track.Hash != original.Hash {
				t.Fatal("matched import rewrote the existing album")
			}
			continue
		}
		if path.Dir(track.Path) != "Physical Artist/Physical Album/Disc 2" ||
			track.Tags.Album != "Shared Album" || track.Tags.AlbumArtist != "Alice & Bob" ||
			track.Tags.Artist != b.Files[0].Tags.Artist || track.AudioHash != b.Files[0].AudioHash {
			t.Fatalf("wrong imported track: %s %+v", track.Path, track.Tags)
		}
	}
	if len(a.tracks()) != 2 {
		t.Fatal("missing imported song")
	}
}

func TestNewEditionRequiresChoiceAndSurvivesPartialImport(t *testing.T) {
	for _, year := range []string{"2005", "2024"} {
		t.Run(year, func(t *testing.T) { checkNewEditionPartialImport(t, year) })
	}
}

func checkNewEditionPartialImport(t *testing.T, year string) {
	t.Helper()
	a := testApp(t)
	fixtureAlbumTrack(t, a, "Alice & Bob/Shared Album/existing.flac", "2005", 1773)
	b := addBatch(t, a, "One", 1774)
	addInboxFile(t, a, &b, "Two", "", 1775)
	if year != "2005" {
		saveAlbumDraft(t, a, b, year, "")
		if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err == nil || len(a.tracks()) != 1 {
			t.Fatalf("conflicting edition was imported without a choice: %v", err)
		}
	}
	saveAlbumDraft(t, a, b, year, "new")
	if err := a.importWithPolicy(b.ID, "reject", []string{b.Files[0].ID}); err != nil {
		t.Fatal(err)
	}
	current, err := get[Batch](a, "batches", b.ID)
	if err != nil || !hexID(current.AlbumMatch, 64) {
		t.Fatalf("new edition placement was not recorded for the remaining draft: %q %v", current.AlbumMatch, err)
	}
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(current.Files)); err != nil {
		t.Fatal(err)
	}
	dirs := map[string]int{}
	for _, track := range a.tracks() {
		dirs[path.Dir(track.Path)]++
	}
	if len(dirs) != 2 || dirs["Alice & Bob/Shared Album"] != 1 {
		t.Fatalf("partial import split or overwrote an edition: %+v", dirs)
	}
}

func TestSkipDuplicateRequiresPublishedFileToStillExist(t *testing.T) {
	a := testApp(t)
	original := addTrack(t, a, "same.flac", "Same", 1776)
	b := addBatch(t, a, "Same", 1776)
	if err := a.music.Remove(original.Path); err != nil {
		t.Fatal(err)
	}
	if err := a.importWithPolicy(b.ID, "skip", fileIDs(b.Files)); err == nil || !strings.Contains(err.Error(), "草稿文件已保留") {
		t.Fatalf("stale duplicate was accepted: %v", err)
	}
	if hash, err := fileHash(a.data, b.Files[0].Path); err != nil || hash != b.Files[0].Hash {
		t.Fatalf("draft source was lost: %v", err)
	}
}

func TestSkipDuplicateUsesEarlierTrackPublishedBySameImport(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Original", 1777)
	addInboxFile(t, a, &b, "Same audio with different title", "", 1777)
	if err := a.importWithPolicy(b.ID, "skip", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	if tracks, op := a.tracks(), latestOperation(t, a); len(tracks) != 1 || op.Affected != 1 || op.Skipped != 1 {
		t.Fatalf("expected one published track and one skipped duplicate, got %d tracks and %+v", len(tracks), op)
	}
}
