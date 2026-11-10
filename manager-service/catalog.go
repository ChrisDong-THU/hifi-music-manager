package main

import (
	"path"
	"slices"
	"sort"
	"strings"
)

// catalog is an immutable read model derived from a committed track snapshot.
// It contains no HTTP or storage operations. Positions refer to tracks owned by
// this snapshot; queries copy only the response page, never the entire library.
type catalog struct {
	tracks        []Track
	stats         libraryStats
	hashes        contentIndex
	positions     []int
	metadata      []trackLookup
	byTrackID     map[string]int
	byAlbum       map[string]*albumRelease
	byIdentity    map[string][]*albumRelease
	byArtist      map[string][]int
	byAlbumArtist map[string][]int
	albumLists    map[string][]Album
	artists       []Artist
}

type trackLookup struct {
	album, search         string
	artists, albumArtists []string
	disc, number          int
}

// Positions point to the first indexed track with a given file or audio hash.
// A draft can use the same lookup for the tracks preceding the current one.
type contentIndex map[string]int

func (index contentIndex) add(track Track, position int) {
	for _, hash := range [...]string{track.AudioHash, track.Hash} {
		if _, exists := index[hash]; hash != "" && !exists {
			index[hash] = position
		}
	}
}

func (index contentIndex) find(track Track) int {
	if position, ok := index[track.AudioHash]; ok {
		return position
	}
	if position, ok := index[track.Hash]; ok {
		return position
	}
	return -1
}

type Artist struct {
	Name      string `json:"name"`
	Tracks    int    `json:"tracks"`
	Albums    int    `json:"albums"`
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

// Disc folders are part of the same release. Other physical folders remain
// separate even when their tags identify the same title and album credit.
func albumLocation(name string, tags Tags) string {
	dir := path.Dir(name)
	if albumDirectoryDisc(dir, tags.Album) > 0 {
		return path.Dir(dir)
	}
	return dir
}

func albumDirectoryDisc(dir, title string) int {
	name := path.Base(dir)
	// A real release can be titled "CD1" or "Disc 2". In that case the
	// containing directory is the release, except for its same-named subfolder.
	disc := discDirectoryNumber(name)
	if disc != 0 && identityText(name) == identityText(title) && identityText(path.Base(path.Dir(dir))) != identityText(title) {
		return 0
	}
	return disc
}

type albumGroup struct {
	identity, location string
}

// Each indexed release owns membership, display metadata, and its import
// identity. Both lookup maps reference the same records.
type albumRelease struct {
	Album
	albumGroup
	matchKey                string
	previousMatchKeys       []string
	members, playOrder      []int
	yearConflict, matchable bool
	sharedLocation          bool
	artworkCache            artworkCache
}

func (r *albumRelease) addTrack(i int, t Track) {
	r.members = append(r.members, i)
	r.Count++
	r.HiRes = r.HiRes && t.hiResFLAC()
	r.Added = max(r.Added, t.Added)
	if !slices.Contains(r.Formats, t.Format) {
		r.Formats = append(r.Formats, t.Format)
	}
	if year := editionYear(t.Tags.Year); year != "" && !r.yearConflict {
		if r.Year == "" {
			r.Year = year
		} else if r.Year != year {
			r.Year, r.yearConflict = "", true
		}
	}
	if !r.matchable {
		name, err := cleanRelative(t.Path)
		r.matchable = err == nil && name == t.Path
	}
}

func (c *catalog) indexAlbums() []*albumRelease {
	releases := make([]*albumRelease, len(c.tracks))
	groups := map[albumGroup]*albumRelease{}
	byLocation := map[string]*albumRelease{}
	editionCounts := map[string]int{}
	type albumIdentities struct{ current, previous string }
	identities := map[string]albumIdentities{}
	for i, track := range c.tracks {
		rawKey := albumKey(track)
		identity, cached := identities[rawKey]
		if !cached {
			artist := albumCredit(track.Tags)
			title := identityText(track.Tags.Album) + "\x00"
			identity.current = title + artistCreditIdentity(artist)
			if strings.ContainsRune(artist, '&') {
				identity.previous = title + artistNamesIdentity(splitArtistCredit(artist, false))
			}
			identities[rawKey] = identity
		}
		group := albumGroup{identity.current, albumLocation(track.Path, track.Tags)}
		release := groups[group]
		if release == nil {
			release = &albumRelease{albumGroup: group, matchKey: albumPlacementKey(group.identity, group.location),
				Album: Album{Key: rawKey, Title: track.Tags.Album, Artist: albumCredit(track.Tags), HiRes: true}}
			groups[group] = release
			if previous := byLocation[group.location]; previous != nil {
				previous.sharedLocation, release.sharedLocation = true, true
			} else {
				byLocation[group.location] = release
			}
			editionCounts[identity.current]++
		} else if release.Key != rawKey {
			release.Key = ""
		}
		// Saved draft placements continue to resolve after credit parsing changes.
		if identity.previous != "" && identity.previous != identity.current {
			previous := albumPlacementKey(identity.previous, group.location)
			if !slices.Contains(release.previousMatchKeys, previous) {
				release.previousMatchKeys = append(release.previousMatchKeys, previous)
			}
		}
		release.addTrack(i, track)
		releases[i] = release
	}
	for _, release := range groups {
		if editionCounts[release.identity] > 1 {
			release.Location = release.location
		}
		if release.Location != "" || release.Key == "" {
			release.Key = "edition:" + release.matchKey
		}
		c.byAlbum[release.Key] = release
		if release.matchable {
			c.byIdentity[release.identity] = append(c.byIdentity[release.identity], release)
		}
	}
	for _, matches := range c.byIdentity {
		if len(matches) > 1 {
			sort.Slice(matches, func(i, j int) bool { return matches[i].location < matches[j].location })
		}
	}
	return releases
}

func buildCatalog(tracks []Track) catalog {
	c := catalog{
		tracks: tracks, positions: make([]int, len(tracks)), metadata: make([]trackLookup, len(tracks)),
		hashes: make(contentIndex, len(tracks)*2), byTrackID: make(map[string]int, len(tracks)),
		byAlbum: map[string]*albumRelease{}, byIdentity: map[string][]*albumRelease{}, byArtist: map[string][]int{}, byAlbumArtist: map[string][]int{},
		albumLists: map[string][]Album{},
	}
	releases := c.indexAlbums()
	credits := make(map[string][]string)
	creditNames := func(credit string) []string {
		names, ok := credits[credit]
		if !ok {
			names = artistCreditNames(credit)
			credits[credit] = names
		}
		return names
	}
	for i, t := range tracks {
		key := releases[i].Key
		albumArtists := creditNames(libraryArtist(t))
		artists := albumArtists
		for _, name := range creditNames(t.Tags.Artist) {
			if !slices.Contains(albumArtists, name) {
				if len(artists) == len(albumArtists) {
					artists = slices.Clone(albumArtists)
				}
				artists = append(artists, name)
			}
		}
		c.positions[i] = i
		c.byTrackID[t.ID] = i
		disc := tagNumber(t.Tags.Disc)
		if disc == 0 {
			disc = albumDirectoryDisc(path.Dir(t.Path), t.Tags.Album)
		}
		c.metadata[i] = trackLookup{
			album: key, artists: artists, albumArtists: albumArtists,
			search: strings.ToLower(t.Path + " " + t.Tags.Title + " " + t.Tags.Artist + " " + t.Tags.AlbumArtist + " " + t.Tags.Album + " " + t.Tags.Genre),
			disc:   max(1, disc), number: tagNumber(t.Tags.Track),
		}
		c.stats.Bytes += t.Size
		c.stats.Duration += t.Duration
		c.hashes.add(t, i)
		for _, name := range albumArtists {
			c.byAlbumArtist[name] = append(c.byAlbumArtist[name], i)
		}
		for _, name := range artists {
			c.byArtist[name] = append(c.byArtist[name], i)
		}
	}
	c.stats.Tracks, c.stats.Artists, c.stats.Albums = len(tracks), len(c.byAlbumArtist), len(c.byAlbum)
	albums := make([]Album, 0, len(c.byAlbum))
	for _, release := range c.byAlbum {
		release.playOrder = slices.Clone(release.members)
		sort.Slice(release.playOrder, func(i, j int) bool { return c.trackLess(release.playOrder[i], release.playOrder[j]) })
		albums = append(albums, release.Album)
	}
	for _, mode := range []string{"", "recent", "title", "artist", "year"} {
		ordered := slices.Clone(albums)
		sortAlbums(ordered, mode)
		c.albumLists[mode] = ordered
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
	if f.album != "" {
		base = c.albumMembers(f.album)
	}
	for _, filter := range []struct {
		value string
		index map[string][]int
	}{{f.artist, c.byArtist}, {f.albumArtist, c.byAlbumArtist}} {
		if filter.value != "" && len(filter.index[filter.value]) <= len(base) {
			base = filter.index[filter.value]
		}
	}
	out := make([]int, 0, len(base))
	for _, i := range base {
		m := c.metadata[i]
		if f.album != "" && m.album != f.album {
			continue
		}
		if f.artist != "" && !slices.Contains(m.artists, f.artist) {
			continue
		}
		if f.albumArtist != "" && !slices.Contains(m.albumArtists, f.albumArtist) {
			continue
		}
		if f.query != "" && !strings.Contains(m.search, f.query) {
			continue
		}
		out = append(out, i)
	}
	return out
}

func (c *catalog) projectTrack(position int) Track {
	track := c.tracks[position]
	track.AlbumKey = c.metadata[position].album
	return track
}

func (c *catalog) tracksPage(f libraryFilter, page, limit int, trackOrder bool) trackPage {
	page = max(0, page)
	if limit < 1 || limit > 200 {
		limit = 100
	}
	var positions []int
	if trackOrder && f.album != "" && f.query == "" && f.artist == "" && f.albumArtist == "" {
		if release := c.byAlbum[f.album]; release != nil {
			positions = release.playOrder
		}
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
		out = append(out, c.projectTrack(i))
	}
	return trackPage{Tracks: out, Total: len(positions), Page: page, Limit: limit}
}

func (c *catalog) albumMembers(key string) []int {
	if release := c.byAlbum[key]; release != nil {
		return release.members
	}
	return nil
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
		groups := map[string]Album{}
		for _, i := range c.matching(f) {
			key, format := c.metadata[i].album, c.tracks[i].Format
			album, exists := groups[key]
			if !exists {
				album = c.byAlbum[key].Album
				album.Count, album.Formats = 0, nil
			}
			album.Count++
			if !slices.Contains(album.Formats, format) {
				album.Formats = append(album.Formats, format)
			}
			groups[key] = album
		}
		albums = make([]Album, 0, len(groups))
		for _, album := range groups {
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
		m := c.metadata[i]
		for _, name := range m.albumArtists {
			artist, exists := groups[name]
			if !exists {
				artist = Artist{Name: name}
				sets[name] = map[string]bool{}
			}
			artist.Tracks++
			sets[name][m.album] = true
			groups[name] = artist
		}
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
