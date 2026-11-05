package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestAlbumHiResUsesEveryTrackAndInclusiveBounds(t *testing.T) {
	flac := func(rate, depth int) Track {
		return Track{Format: "FLAC", AudioInfo: AudioInfo{SampleRate: rate, BitDepth: depth}}
	}
	for _, tc := range []struct {
		name   string
		tracks []Track
		want   bool
	}{
		{"lower-bound", []Track{flac(44100, 24)}, true},
		{"upper-bound", []Track{flac(192000, 24)}, true},
		{"mixed-hires-rates", []Track{flac(48000, 24), flac(96000, 24), flac(192000, 24)}, true},
		{"below-range", []Track{flac(44099, 24)}, false},
		{"above-range", []Track{flac(192001, 24)}, false},
		{"cd", []Track{flac(44100, 16)}, false},
		{"32bit", []Track{flac(96000, 32)}, false},
		{"mixed-depth", []Track{flac(96000, 24), flac(44100, 16)}, false},
		{"missing-depth", []Track{flac(96000, 0)}, false},
		{"missing-rate", []Track{flac(0, 24)}, false},
		{"mixed-formats", []Track{flac(96000, 24), {Format: "MP3", AudioInfo: AudioInfo{SampleRate: 48000}}}, false},
		{"wav24", []Track{{Format: "WAV", AudioInfo: AudioInfo{SampleRate: 96000, BitDepth: 24}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			for i, track := range tc.tracks {
				track.ID = fmt.Sprint(i)
				track.Tags = Tags{Album: "Album", Artist: "Artist"}
				if err := a.putTrack(track); err != nil {
					t.Fatal(err)
				}
			}
			w := request(a, "GET", "/api/albums", "", false)
			var result struct {
				Albums []Album `json:"albums"`
			}
			if w.Code != 200 {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Albums) != 1 || result.Albums[0].HiRes != tc.want {
				t.Fatalf("want hi_res=%v: %s", tc.want, w.Body.String())
			}
		})
	}
}

func TestAlbumHiResIsNotChangedBySearchFilters(t *testing.T) {
	a := testApp(t)
	for _, track := range []Track{
		{ID: "high", Format: "FLAC", AudioInfo: AudioInfo{SampleRate: 96000, BitDepth: 24}, Tags: Tags{Title: "HighOnly", Artist: "Guest", AlbumArtist: "Various", Album: "Mixed"}},
		{ID: "low", Format: "FLAC", AudioInfo: AudioInfo{SampleRate: 44100, BitDepth: 16}, Tags: Tags{Title: "Other", Artist: "Other", AlbumArtist: "Various", Album: "Mixed"}},
	} {
		if err := a.putTrack(track); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{"?q=HighOnly", "?artist=Guest", "?q=HighOnly&artist=Guest"} {
		w := request(a, "GET", "/api/albums"+query, "", false)
		var result struct {
			Albums []Album `json:"albums"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Albums) != 1 || result.Albums[0].HiRes {
			t.Fatalf("filter hid non-HiRes track and mislabeled album: %s", w.Body.String())
		}
	}
}
