package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// Exercise the public read path against a realistic number of albums, rather
// than measuring a cache accessor in isolation. All storage is disposable.
func BenchmarkLibraryQueries(b *testing.B) {
	a, err := newApp(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer a.close()
	a.db.NoSync = true
	if err := a.db.Update(func(tx *bolt.Tx) error {
		for i := range 10000 {
			artist, album := fmt.Sprintf("Artist %03d", i/100), fmt.Sprintf("Album %04d", i/20)
			t := Track{ID: fmt.Sprint(i), Path: fmt.Sprintf("%s/%s/%03d.flac", artist, album, i%20), Format: "FLAC", Tags: Tags{Title: fmt.Sprintf("Track %d", i), Album: album, Artist: artist, Track: fmt.Sprint(i%20 + 1)}}
			if err := storeTrackTx(tx, t); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	handler := a.routes(http.NotFoundHandler())
	for _, tc := range []struct{ name, path string }{
		{"albums", "/api/albums?sort=title"},
		{"album_tracks", "/api/tracks?sort=track&album=" + url.QueryEscape("Artist 000\x00Album 0000")},
		{"artist_albums", "/api/albums?sort=title&album_artist=Artist%20000"},
		{"search", "/api/tracks?q=track%20999"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			r := httptest.NewRequest("GET", tc.path, nil)
			handler.ServeHTTP(httptest.NewRecorder(), r)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				handler.ServeHTTP(httptest.NewRecorder(), r)
			}
		})
	}
}
