package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

func sortTracks(tracks []Track) {
	keys := make(map[string]string, len(tracks))
	for _, t := range tracks {
		keys[t.ID] = strings.ToLower(t.Tags.Artist + t.Tags.Album + t.Path)
	}
	sort.Slice(tracks, func(i, j int) bool {
		left, right := tracks[i].ID, tracks[j].ID
		if keys[left] != keys[right] {
			return keys[left] < keys[right]
		}
		return left < right
	})
}

// Clamp before multiplication: a user-supplied page must never overflow an int.
func pageBounds(total, page, limit int) (int, int) {
	page = max(0, page)
	if page > total/limit {
		return total, total
	}
	start := page * limit
	return start, start + min(limit, total-start)
}

// Routes decode transport parameters and enrich shared artist resources. All
// track grouping, ordering and pagination belong to the immutable catalog.
func (a *app) libraryRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/tracks", func(w http.ResponseWriter, r *http.Request) {
		index, err := a.readLibraryIndex()
		if err != nil {
			problem(w, 500, err)
			return
		}
		p := r.URL.Query()
		page, _ := strconv.Atoi(p.Get("page"))
		limit, _ := strconv.Atoi(p.Get("limit"))
		result := index.tracksPage(libraryFilter{query: p.Get("q"), artist: p.Get("artist"), album: p.Get("album")}, page, limit, p.Get("sort") == "track")
		result.Revision = index.revision
		respond(w, result)
	})
	m.HandleFunc("GET /api/albums", func(w http.ResponseWriter, r *http.Request) {
		index, err := a.readLibraryIndex()
		if err != nil {
			problem(w, 500, err)
			return
		}
		p := r.URL.Query()
		page, _ := strconv.Atoi(p.Get("page"))
		result := index.albumsPage(libraryFilter{query: p.Get("q"), artist: p.Get("artist"), albumArtist: p.Get("album_artist")}, page, p.Get("sort"))
		result.Revision = index.revision
		respond(w, result)
	})
	m.HandleFunc("GET /api/artists", func(w http.ResponseWriter, r *http.Request) {
		index, err := a.readLibraryIndex()
		if err != nil {
			problem(w, 500, err)
			return
		}
		artists := index.queryArtists(r.URL.Query().Get("q"))
		for i := range artists {
			profile, err := a.readArtistProfile(artists[i].Name)
			if err != nil {
				problem(w, 500, err)
				return
			}
			artists[i].Biography, artists[i].ImageURL = profile.Biography, profile.imageURL()
		}
		respond(w, artists)
	})
}
