package main

import (
	"context"
	"slices"
	"testing"

	id3 "github.com/bogem/id3v2/v2"
)

func TestArtistCreditSeparatesCompactCollaborators(t *testing.T) {
	for _, tc := range []struct {
		credit string
		want   []string
	}{
		{"黄光熙 & TAEYANG&G~DRAGON", []string{"黄光熙", "TAEYANG", "G~DRAGON"}},
		{"Alice&Bob&Carol", []string{"Alice", "Bob", "Carol"}},
		{"Alice & R&B&Carol", []string{"Alice", "R&B", "Carol"}},
		{"R&B", []string{"R&B"}},
		{"AC&DC", []string{"AC&DC"}},
		{`"AC&DC" & Bob&Carol`, []string{"AC&DC", "Bob", "Carol"}},
		{`"Earth, Wind & Fire" & Guest`, []string{"Earth, Wind & Fire", "Guest"}},
	} {
		if got := artistCreditNames(tc.credit); !slices.Equal(got, tc.want) {
			t.Errorf("%q: got %q, want %q", tc.credit, got, tc.want)
		}
	}
	native := []string{"AC&DC", "R&B", "Guest"}
	if got := artistCreditNames(formatArtistCredit(native)); !slices.Equal(got, native) {
		t.Fatalf("native artist names did not round-trip: %q", got)
	}
}

func TestCompactCreditMatchesExistingAlbumAndSavedPlacement(t *testing.T) {
	const title, credit, location = "Festival", "黄光熙 & TAEYANG&G~DRAGON", "Festival"
	track := Track{ID: "existing", Path: "Festival/first.flac", Tags: Tags{Album: title, Artist: credit}}
	catalog := buildCatalog([]Track{track})
	for _, artist := range []string{"黄光熙", "TAEYANG", "G~DRAGON"} {
		if len(catalog.byArtist[artist]) != 1 || len(catalog.byAlbumArtist[artist]) != 1 {
			t.Fatalf("artist %q does not have a separate index entry", artist)
		}
	}
	oldCredit := artistNamesIdentity([]string{"黄光熙", "TAEYANG&G~DRAGON"})
	saved := albumPlacementKey(identityText(title)+"\x00"+oldCredit, location)
	matches := catalog.byIdentity[albumIdentity(title, "G~DRAGON & 黄光熙 & TAEYANG")]
	if match, err := chooseAlbum(matches, saved, ""); err != nil || match == nil || match.Artist != credit || match.location != location {
		t.Fatalf("old draft placement did not resolve to the original album: %v", err)
	}
}

func TestFLACArtistEditWritesNativeValuesAndKeepsThemDuringImport(t *testing.T) {
	a := testApp(t)
	const source = "duet.flac"
	const destination = "duets/saved.flac"
	if err := a.data.WriteFile(source, toneFLAC("Duet", 42), 0600); err != nil {
		t.Fatal(err)
	}
	patch := map[string]string{"artist": `"Earth, Wind & Fire" & Guest`, "album_artist": "黄光熙 & TAEYANG&G~DRAGON"}
	tags, err := patchTags(Tags{}, patch)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTags(a.data, source, tags, nil, patch); err != nil {
		t.Fatal(err)
	}
	before, err := inspect(a.data, source)
	if err != nil {
		t.Fatal(err)
	}
	if got := artistCreditNames(before.Tags.Artist); !slices.Equal(got, []string{"Earth, Wind & Fire", "Guest"}) {
		t.Fatal("native artist names merged or split", got)
	}
	tags = before.Tags
	tags.Genre = "Soul"
	if err := stageTagsBetween(context.Background(), a.data, source, a.music, destination, tags, nil, map[string]string{"genre": tags.Genre}); err != nil {
		t.Fatal(err)
	}
	after, err := inspect(a.music, destination)
	if err != nil {
		t.Fatal(err)
	}
	if after.Tags != tags || after.AudioHash != before.AudioHash {
		t.Fatalf("unrelated import edit changed credits or audio: %+v", after.Tags)
	}
	file, err := a.music.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	blocks, _, err := flacHeader(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range blocks {
		if block.kind != 4 {
			continue
		}
		_, comments, err := parseComments(block.data)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range []string{"ARTIST=Earth, Wind & Fire", "ARTIST=Guest", "ALBUMARTIST=黄光熙", "ALBUMARTIST=TAEYANG", "ALBUMARTIST=G~DRAGON"} {
			if !slices.Contains(comments, entry) {
				t.Fatal("missing native Vorbis comment", entry, comments)
			}
		}
		return
	}
	t.Fatal("missing Vorbis comment block")
}

func TestID3v24ArtistValuesRemainSeparate(t *testing.T) {
	a := testApp(t)
	const path = "duet.mp3"
	tag := id3.NewEmptyTag()
	tag.SetArtist("Original")
	file, err := a.music.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tag.WriteTo(file); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(append([]byte{0xff, 0xfb, 0x90, 0x00}, make([]byte, 413)...)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := inspect(a.music, path)
	if err != nil {
		t.Fatal(err)
	}
	patch := map[string]string{"artist": "黄光熙 & TAEYANG&G~DRAGON", "album_artist": "甲＆丙"}
	tags, err := patchTags(original.Tags, patch)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTags(a.music, path, tags, nil, patch); err != nil {
		t.Fatal(err)
	}
	updated, err := inspect(a.music, path)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Tags.Artist != "黄光熙 & TAEYANG & G~DRAGON" || updated.Tags.AlbumArtist != "甲 & 丙" || updated.AudioHash != original.AudioHash {
		t.Fatalf("native ID3 artists or audio were lost: %+v", updated.Tags)
	}
	file, err = a.music.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	native, err := id3.ParseReader(file, id3.Options{Parse: true})
	if err != nil {
		t.Fatal(err)
	}
	if native.Version() != 4 || native.GetTextFrame("TPE1").Text != "黄光熙\x00TAEYANG\x00G~DRAGON" || native.GetTextFrame("TPE2").Text != "甲\x00丙" {
		t.Fatal("artist credits were not written as ID3v2.4 text values")
	}
}
