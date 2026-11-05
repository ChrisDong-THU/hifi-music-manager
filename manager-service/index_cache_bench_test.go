package main

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// Draft progress commits must not repeatedly decode and sort an unchanged library.
func BenchmarkTrackCacheDuringDraftProgress(b *testing.B) {
	home := b.TempDir()
	a, err := newAppStorage(filepath.Join(home, "manager"), filepath.Join(home, "music"), filepath.Join(home, "manager"), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(a.close)
	a.db.NoSync = true // Measure cache work rather than the temporary disk's fsync.
	if err := a.db.Update(func(tx *bolt.Tx) error {
		for i := range 1000 {
			if err := storeTrackTx(tx, Track{ID: fmt.Sprint(i), Path: fmt.Sprintf("artist/album/%d.flac", i), Tags: Tags{Title: "Title", Artist: "Artist", Album: "Album"}}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	if _, err := a.readTracks(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := a.put("batches", "progress", Batch{ID: "progress", QualityState: "running"}); err != nil {
			b.Fatal(err)
		}
		if _, err := a.readTracks(); err != nil {
			b.Fatal(err)
		}
	}
}

// Full state responses during processing, with an unchanged committed library.
// Different sizes make accidental O(library size) polling work visible.
func BenchmarkStateDuringDraftProgress(b *testing.B) {
	for _, size := range []int{1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			home := b.TempDir()
			a, err := newAppStorage(filepath.Join(home, "manager"), filepath.Join(home, "music"), filepath.Join(home, "manager"), nil)
			if err != nil {
				b.Fatal(err)
			}
			defer a.close()
			a.db.NoSync = true
			if err := a.db.Update(func(tx *bolt.Tx) error {
				for i := range size {
					track := Track{ID: fmt.Sprint(i), Hash: fmt.Sprintf("file-%d", i), AudioHash: fmt.Sprintf("audio-%d", i), Tags: Tags{Artist: "Artist", Album: "Album"}}
					if err := storeTrackTx(tx, track); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				b.Fatal(err)
			}
			request := httptest.NewRequest("GET", "/api/state", nil)
			a.state(httptest.NewRecorder(), request)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := a.put("batches", "progress", Batch{ID: "progress", State: "ready", QualityState: "running"}); err != nil {
					b.Fatal(err)
				}
				a.state(httptest.NewRecorder(), request)
			}
		})
	}
}
