package main

import (
	"path"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestComponentKeepsTruncatedNamesPortable(t *testing.T) {
	for _, test := range []struct{ name, input, want string }{
		{"rune boundary dot", strings.Repeat("a", 99) + ".b", strings.Repeat("a", 99)},
		{"rune boundary space", strings.Repeat("a", 99) + " b", strings.Repeat("a", 99)},
		{"UTF8 byte boundary", strings.Repeat("音", 67), strings.Repeat("音", 66)},
		{"byte boundary dot", strings.Repeat("音", 66) + ".音", strings.Repeat("音", 66)},
		{"byte boundary space", strings.Repeat("音", 66) + " 音", strings.Repeat("音", 66)},
		{"reserved device", "con.txt", "_con.txt"},
		{"reserved after truncation", "CON" + strings.Repeat(" ", 97) + "x", "_CON"},
		{"long reserved name", "CON." + strings.Repeat("a", 96), "_CON." + strings.Repeat("a", 95)},
		{"empty", " . . ", "未知"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := component(test.input)
			if got != test.want || !utf8.ValidString(got) || len(got) > 200 || utf8.RuneCountInString(got) > 100 {
				t.Fatalf("component(%q) = %q, want %q", test.input, got, test.want)
			}
			if repeated := component(got); repeated != got {
				t.Fatalf("normalization is not stable: %q -> %q", got, repeated)
			}
		})
	}
	for _, device := range []string{"COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³"} {
		for _, suffix := range []string{"", ".flac"} {
			input := strings.ToLower(device) + suffix
			if got := component(input); got != "_"+input || component(got) != got {
				t.Errorf("reserved device %q was not escaped stably: %q", input, got)
			}
		}
	}
}

func TestImportNormalizesNewAlbumDirectoriesWithoutChangingTags(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Portable", 85)
	tags := b.Files[0].Tags
	tags.Artist = "COM¹"
	tags.AlbumArtist = tags.Artist
	tags.Album = strings.Repeat("a", 99) + ".b"
	tags.Title = "LPT²"
	tags.Track = ""
	saveDraftAlbumTags(t, a, b, []Tags{tags}, "")
	if err := a.importWithPolicy(b.ID, "reject", fileIDs(b.Files)); err != nil {
		t.Fatal(err)
	}
	tracks := a.tracks()
	if len(tracks) != 1 {
		t.Fatalf("imported %d tracks", len(tracks))
	}
	want := path.Join("_COM¹", strings.Repeat("a", 99), "_LPT².flac")
	if tracks[0].Path != want {
		t.Fatalf("import path = %q, want %q", tracks[0].Path, want)
	}
	actual, err := inspect(a.music, want)
	if err != nil || actual.Tags.Title != tags.Title || actual.Tags.Album != tags.Album || actual.Tags.Artist != tags.Artist {
		t.Fatalf("normalizing paths changed music metadata: %+v, %v", actual.Tags, err)
	}
}
