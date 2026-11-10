package main

import "testing"

func TestCatalogPreservesAlbumIdentityFiltersAndWholeAlbumClassification(t *testing.T) {
	tracks := []Track{
		{ID: "one", Format: "FLAC", AudioInfo: AudioInfo{BitDepth: 24, SampleRate: 96000}, Tags: Tags{Title: "Match", Artist: "Singer A", AlbumArtist: "Collection", Album: "Mixed", Disc: "2", Track: "1"}},
		{ID: "two", Format: "MP3", Tags: Tags{Title: "Other", Artist: "Singer B", AlbumArtist: "Collection", Album: "Mixed", Disc: "1", Track: "10"}},
		{ID: "unknown", Format: "WAV", Tags: Tags{Title: "Unknown"}},
	}
	sortTracks(tracks)
	c := buildCatalog(tracks)
	page := c.albumsPage(libraryFilter{query: "match"}, 0, "title")
	if page.Total != 1 || page.Albums[0].Count != 1 || page.Albums[0].HiRes || page.Albums[0].Formats[0] != "FLAC" {
		t.Fatalf("filtered album semantics changed: %+v", page)
	}
	key := "Collection\x00Mixed"
	ordered := c.tracksPage(libraryFilter{album: key}, 0, 100, true)
	if ordered.Total != 2 || ordered.Tracks[0].ID != "two" {
		t.Fatal("numeric disc ordering changed", ordered)
	}
	member := c.albumsPage(libraryFilter{artist: "Singer A"}, 0, "")
	if member.Total != 1 || member.Albums[0].Count != 1 {
		t.Fatal("track artist was lost", member)
	}
	owner := c.albumsPage(libraryFilter{albumArtist: "Collection"}, 0, "")
	if owner.Total != 1 || owner.Albums[0].Count != 2 {
		t.Fatal("album artist grouping changed", owner)
	}
	unknown := c.tracksPage(libraryFilter{artist: "未知歌手"}, 0, 100, false)
	if unknown.Total != 1 || unknown.Tracks[0].ID != "unknown" {
		t.Fatal("unknown artist filter changed", unknown)
	}
	page.Albums[0].Formats[0] = "corrupted by caller"
	ordered.Tracks[0].Tags.Title = "corrupted by caller"
	all := c.albumsPage(libraryFilter{}, 0, "title")
	for _, album := range all.Albums {
		for _, format := range album.Formats {
			if format == "corrupted by caller" {
				t.Fatal("response exposed immutable format storage")
			}
		}
	}
	if c.tracksPage(libraryFilter{album: key}, 0, 100, true).Tracks[0].Tags.Title != "Other" {
		t.Fatal("response exposed immutable tracks")
	}
}

func TestCatalogSeparatesPhysicalEditionsAndGroupsEquivalentTags(t *testing.T) {
	tracks := []Track{
		{ID: "first", Path: "Release/CD1/01.flac", Tags: Tags{Title: "First", Album: "Groupies", AlbumArtist: "甲 & 乙", Track: "9"}},
		{ID: "second", Path: "Release/CD2/01.flac", Tags: Tags{Title: "Second", Album: "groupies ", AlbumArtist: "乙＆甲", Track: "1"}},
		{ID: "remaster", Path: "Remaster/01.flac", Tags: Tags{Title: "Remaster", Album: "Groupies", AlbumArtist: "甲 & 乙", Track: "1"}},
	}
	catalog := buildCatalog(tracks)
	stats := newStatsAccumulator()
	for _, track := range tracks {
		stats.add(track)
	}
	if catalog.stats.Albums != 2 || catalog.stats.Albums != stats.stats.Albums {
		t.Fatalf("edition counts disagree: catalog %d scan %d", catalog.stats.Albums, stats.stats.Albums)
	}
	if len(catalog.albumMembers(albumKey(tracks[0]))) != 0 {
		t.Fatal("ambiguous old album key still selects tracks")
	}
	release := catalog.projectTrack(catalog.byTrackID["first"]).AlbumKey
	remaster := catalog.projectTrack(catalog.byTrackID["remaster"]).AlbumKey
	if release == remaster || catalog.byAlbum[release].Location != "Release" || catalog.byAlbum[remaster].Location != "Remaster" {
		t.Fatal("physical editions are not distinguishable", catalog.byAlbum)
	}
	page := catalog.tracksPage(libraryFilter{album: release}, 0, 100, true)
	if page.Total != 2 || page.Tracks[0].ID != "first" || page.Tracks[1].ID != "second" || page.Tracks[1].AlbumKey != release {
		t.Fatal("same-directory spellings or disc ordering split a release", page)
	}
	page = catalog.tracksPage(libraryFilter{album: remaster}, 0, 100, true)
	if page.Total != 1 || page.Tracks[0].ID != "remaster" {
		t.Fatal("tracks from two physical editions were merged", page)
	}
	filtered := catalog.albumsPage(libraryFilter{query: "second"}, 0, "")
	if filtered.Total != 1 || filtered.Albums[0].Key != release || filtered.Albums[0].Title != catalog.byAlbum[release].Title ||
		filtered.Albums[0].Location != "Release" || filtered.Albums[0].Count != 1 {
		t.Fatal("filtered tracks changed the edition label", filtered)
	}
}
