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

func TestCollaboratorsShareAlbumAndHaveSeparateArtistProfiles(t *testing.T) {
	a := testApp(t)
	tracks := []Track{
		{ID: "one", Tags: Tags{Artist: "甲 & 丙", AlbumArtist: "甲＆乙 & 乙", Album: "联合专辑"}},
		{ID: "two", Tags: Tags{Artist: "乙", AlbumArtist: "甲＆乙 & 乙", Album: "联合专辑"}},
		{ID: "single", Tags: Tags{Artist: "R&B", Album: "个人专辑"}},
	}
	for _, track := range tracks {
		if err := a.putTrack(track); err != nil {
			t.Fatal(err)
		}
	}
	var artists []Artist
	response := request(a, "GET", "/api/artists", "", false)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &artists) != nil {
		t.Fatalf("could not read artists: %d %s", response.Code, response.Body)
	}
	want := map[string]int{"甲": 2, "乙": 2, "R&B": 1}
	for _, artist := range artists {
		if artist.Tracks != want[artist.Name] || artist.Albums != 1 {
			t.Fatalf("incorrect artist entry: %+v", artist)
		}
		delete(want, artist.Name)
	}
	if len(want) != 0 {
		t.Fatal("missing artist entries", want)
	}
	response = request(a, "GET", "/api/albums?album_artist="+url.QueryEscape("乙"), "", false)
	var albums albumPage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &albums) != nil ||
		len(albums.Albums) != 1 || albums.Albums[0].Count != 2 || albums.Albums[0].Artist != tracks[0].Tags.AlbumArtist {
		t.Fatalf("album credit or membership changed: %d %s", response.Code, response.Body)
	}
	response = request(a, "GET", "/api/tracks?artist="+url.QueryEscape("丙"), "", false)
	var performers trackPage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &performers) != nil ||
		len(performers.Tracks) != 1 || performers.Tracks[0].ID != "one" {
		t.Fatalf("could not find a track collaborator: %d %s", response.Code, response.Body)
	}
	response = request(a, "GET", "/api/artists/detail?name="+url.QueryEscape("乙"), "", false)
	if response.Code != 200 {
		t.Fatalf("collaborator cannot be managed individually: %d %s", response.Code, response.Body)
	}
}

func TestArtistCreditIdentityMatchesWithoutChangingDisplayCredit(t *testing.T) {
	const display = "Björk & 李雷&韩梅梅 & BJÖRK"
	if artistCreditIdentity(display) != artistCreditIdentity("韩梅梅 ＆ BJO\u0308RK ＆ 李雷") {
		t.Fatal("equivalent collaborator credits did not match")
	}
	if artistCreditIdentity("R&B") == artistCreditIdentity("R & B") {
		t.Fatal("a compact artist name was treated as a collaboration")
	}
	if artistCreditIdentity("   ") != "" || artistCreditIdentity("李雷") == artistCreditIdentity(display) {
		t.Fatal("blank or incomplete credits matched a collaboration")
	}
}
