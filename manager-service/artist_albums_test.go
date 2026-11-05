package main

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestArtistAlbumsMatchArtistGroups(t *testing.T) {
	a := testApp(t)
	for _, track := range []Track{
		{ID: "solo", Tags: Tags{Artist: "Guest", Album: "Solo"}},
		{ID: "guest", Tags: Tags{Artist: "Guest", AlbumArtist: "Various", Album: "Compilation"}},
		{ID: "other", Tags: Tags{Artist: "Other", AlbumArtist: "Various", Album: "Compilation"}},
		{ID: "unknown", Tags: Tags{Album: "Mystery"}},
		{ID: "named-unknown", Tags: Tags{Artist: "未知歌手", Album: "Another"}},
	} {
		if err := a.putTrack(track); err != nil {
			t.Fatal(err)
		}
	}
	w := request(a, "GET", "/api/artists", "", false)
	var artists []struct {
		Name   string `json:"name"`
		Tracks int    `json:"tracks"`
		Albums int    `json:"albums"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &artists); err != nil {
		t.Fatal(err)
	}
	if len(artists) != 3 {
		t.Fatalf("unexpected artist groups: %s", w.Body.String())
	}
	for _, artist := range artists {
		t.Run(artist.Name, func(t *testing.T) {
			w := request(a, "GET", "/api/albums?album_artist="+url.QueryEscape(artist.Name), "", false)
			var result struct {
				Albums []Album `json:"albums"`
				Total  int     `json:"total"`
			}
			if w.Code != 200 {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			tracks := 0
			for _, album := range result.Albums {
				name := album.Artist
				if name == "" {
					name = "未知歌手"
				}
				if name != artist.Name {
					t.Fatalf("album from another artist: %+v", album)
				}
				tracks += album.Count
			}
			if result.Total != artist.Albums || tracks != artist.Tracks {
				t.Fatalf("artist counts do not match: artist=%+v albums=%+v", artist, result)
			}
		})
	}
}
