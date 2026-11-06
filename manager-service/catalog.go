package main

import (
	"sort"
	"strings"
)

// catalog is an immutable read model derived from a committed track snapshot.
// It contains no HTTP or storage operations. Positions refer to tracks owned by
// this snapshot; queries copy only the response page, never the entire library.
type catalog struct {
	tracks        []Track
	stats         libraryStats
	hashes        map[string]bool
	positions     []int
	metadata      []trackLookup
	byAlbum       map[string][]int
	byArtist      map[string][]int
	byAlbumArtist map[string][]int
	albumOrder    map[string][]int
	albums        map[string]Album
	albumLists    map[string][]Album
	artists       []Artist
}

type trackLookup struct {
	album, artist, search string
	disc, number          int
}

type Artist struct {
	Name      string `json:"name"`
	Tracks    int    `json:"tracks"`
	Albums    int    `json:"albums"`
	CoverID   string `json:"cover_id"`
	Biography string `json:"biography,omitempty"`
	ImageURL  string `json:"image_url,omitempty"`
}

type libraryFilter struct{ query, artist, album, albumArtist string }
type trackPage struct {
	Tracks   []Track `json:"tracks"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	Limit    int     `json:"limit"`
	Revision uint64  `json:"revision"`
}
type albumPage struct {
	Albums   []Album `json:"albums"`
	Total    int     `json:"total"`
	Revision uint64  `json:"revision"`
}

func buildCatalog(tracks []Track) catalog {
	c := catalog{
		tracks: tracks, positions: make([]int, len(tracks)), metadata: make([]trackLookup, len(tracks)),
		hashes:  make(map[string]bool, len(tracks)*2),
		byAlbum: map[string][]int{}, byArtist: map[string][]int{}, byAlbumArtist: map[string][]int{},
		albumOrder: map[string][]int{}, albumLists: map[string][]Album{},
	}
	for i, t := range tracks {
		key, artist := albumKey(t), libraryArtist(t)
		c.positions[i] = i
		c.metadata[i] = trackLookup{
			album: key, artist: artist,
			search: strings.ToLower(t.Path + " " + t.Tags.Title + " " + t.Tags.Artist + " " + t.Tags.AlbumArtist + " " + t.Tags.Album + " " + t.Tags.Genre),
			disc:   max(1, tagNumber(t.Tags.Disc)), number: tagNumber(t.Tags.Track),
		}
		c.stats.Bytes += t.Size
		c.stats.Duration += t.Duration
		if t.Hash != "" {
			c.hashes[t.Hash] = true
		}
		if t.AudioHash != "" {
			c.hashes[t.AudioHash] = true
		}
		c.byAlbum[key] = append(c.byAlbum[key], i)
		c.byAlbumArtist[artist] = append(c.byAlbumArtist[artist], i)
		c.byArtist[artist] = append(c.byArtist[artist], i)
		if t.Tags.Artist != artist {
			c.byArtist[t.Tags.Artist] = append(c.byArtist[t.Tags.Artist], i)
		}
	}
	c.stats.Tracks, c.stats.Artists, c.stats.Albums = len(tracks), len(c.byAlbumArtist), len(c.byAlbum)
	c.albums = c.groupAlbums(c.positions)
	for key, positions := range c.byAlbum {
		order := append([]int(nil), positions...)
		sort.Slice(order, func(i, j int) bool { return c.trackLess(order[i], order[j]) })
		c.albumOrder[key] = order
	}
	for _, mode := range []string{"", "recent", "title", "artist", "year"} {
		albums := make([]Album, 0, len(c.albums))
		for _, album := range c.albums {
			albums = append(albums, album)
		}
		sortAlbums(albums, mode)
		c.albumLists[mode] = albums
	}
	c.artists = c.groupArtists(c.positions)
	return c
}

func (c *catalog) trackLess(left, right int) bool {
	l, r := c.metadata[left], c.metadata[right]
	if l.album != r.album {
		return l.album < r.album
	}
	if l.disc != r.disc {
		return l.disc < r.disc
	}
	if l.number != r.number {
		if l.number == 0 {
			return false
		}
		if r.number == 0 {
			return true
		}
		return l.number < r.number
	}
	return c.tracks[left].ID < c.tracks[right].ID
}

func (c *catalog) matching(f libraryFilter) []int {
	f.query = strings.ToLower(strings.TrimSpace(f.query))
	if f == (libraryFilter{}) {
		return c.positions
	}
	base := c.positions
	for _, filter := range []struct {
		value string
		index map[string][]int
	}{{f.album, c.byAlbum}, {f.artist, c.byArtist}, {f.albumArtist, c.byAlbumArtist}} {
		if filter.value != "" && len(filter.index[filter.value]) <= len(base) {
			base = filter.index[filter.value]
		}
	}
	out := make([]int, 0, len(base))
	for _, i := range base {
		m, t := c.metadata[i], c.tracks[i]
		if f.album != "" && m.album != f.album {
			continue
		}
		if f.artist != "" && t.Tags.Artist != f.artist && m.artist != f.artist {
			continue
		}
		if f.albumArtist != "" && m.artist != f.albumArtist {
			continue
		}
		if f.query != "" && !strings.Contains(m.search, f.query) {
			continue
		}
		out = append(out, i)
	}
	return out
}

func (c *catalog) tracksPage(f libraryFilter, page, limit int, trackOrder bool) trackPage {
	page = max(0, page)
	if limit < 1 || limit > 200 {
		limit = 100
	}
	var positions []int
	if trackOrder && f.album != "" && f.query == "" && f.artist == "" && f.albumArtist == "" {
		positions = c.albumOrder[f.album]
	} else {
		positions = c.matching(f)
		if trackOrder {
			positions = append([]int(nil), positions...)
			sort.Slice(positions, func(i, j int) bool { return c.trackLess(positions[i], positions[j]) })
		}
	}
	start, end := pageBounds(len(positions), page, limit)
	out := make([]Track, 0, end-start)
	for _, i := range positions[start:end] {
		out = append(out, c.tracks[i])
	}
	return trackPage{Tracks: out, Total: len(positions), Page: page, Limit: limit}
}

func (c *catalog) groupAlbums(positions []int) map[string]Album {
	out := map[string]Album{}
	for _, i := range positions {
		t, key := c.tracks[i], c.metadata[i].album
		album, exists := out[key]
		if !exists {
			artist := t.Tags.AlbumArtist
			if artist == "" {
				artist = t.Tags.Artist
			}
			album = Album{Key: key, Title: t.Tags.Album, Artist: artist, Year: t.Tags.Year, CoverID: t.ID, Formats: []string{}, HiRes: true}
		}
		album.Count++
		album.HiRes = album.HiRes && t.hiResFLAC()
		if t.Added > album.Added {
			album.Added = t.Added
		}
		found := false
		for _, format := range album.Formats {
			found = found || format == t.Format
		}
		if !found {
			album.Formats = append(album.Formats, t.Format)
		}
		out[key] = album
	}
	return out
}

func sortAlbums(albums []Album, mode string) {
	sort.Slice(albums, func(i, j int) bool {
		l, r := albums[i], albums[j]
		switch mode {
		case "recent":
			if l.Added != r.Added {
				return l.Added > r.Added
			}
		case "title", "artist":
			left, right := l.Title, r.Title
			if mode == "artist" {
				left, right = l.Artist, r.Artist
			}
			left, right = strings.ToLower(strings.TrimSpace(left)), strings.ToLower(strings.TrimSpace(right))
			if left != right {
				if left == "" {
					return false
				}
				if right == "" {
					return true
				}
				return left < right
			}
		case "year":
			if left, right := albumYear(l.Year), albumYear(r.Year); left != right {
				return left > right
			}
		}
		return l.Key < r.Key
	})
}

func (c *catalog) albumsPage(f libraryFilter, page int, mode string) albumPage {
	f.query = strings.ToLower(strings.TrimSpace(f.query))
	albums := c.albumLists[mode]
	if albums == nil {
		albums = c.albumLists[""]
	}
	if f != (libraryFilter{}) {
		groups := c.groupAlbums(c.matching(f))
		albums = make([]Album, 0, len(groups))
		for key, album := range groups {
			// Search changes matching counts, never whole-album classification.
			album.HiRes = c.albums[key].HiRes
			albums = append(albums, album)
		}
		sortAlbums(albums, mode)
	}
	start, end := pageBounds(len(albums), page, 48)
	out := append([]Album{}, albums[start:end]...)
	for i := range out {
		out[i].Formats = append([]string{}, out[i].Formats...)
	}
	return albumPage{Albums: out, Total: len(albums)}
}

func (c *catalog) groupArtists(positions []int) []Artist {
	groups, sets := map[string]Artist{}, map[string]map[string]bool{}
	for _, i := range positions {
		m, t := c.metadata[i], c.tracks[i]
		artist, exists := groups[m.artist]
		if !exists {
			artist = Artist{Name: m.artist, CoverID: t.ID}
			sets[m.artist] = map[string]bool{}
		}
		artist.Tracks++
		sets[m.artist][m.album] = true
		groups[m.artist] = artist
	}
	out := make([]Artist, 0, len(groups))
	for name, artist := range groups {
		artist.Albums = len(sets[name])
		out = append(out, artist)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (c *catalog) queryArtists(query string) []Artist {
	if strings.TrimSpace(query) == "" {
		return append([]Artist{}, c.artists...)
	}
	return c.groupArtists(c.matching(libraryFilter{query: query}))
}
