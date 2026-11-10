package main

import bolt "go.etcd.io/bbolt"

// Immutable after construction. A reader keeps the index matching its own bbolt
// snapshot, even if another reader has already cached a newer commit.
type libraryIndex struct {
	revision uint64
	catalog
}

func (a *app) libraryIndexTx(tx *bolt.Tx) (*libraryIndex, error) {
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	revision := tx.Bucket([]byte("tracks")).Sequence()
	if a.index != nil && a.index.revision == revision {
		return a.index, nil
	}
	tracks, err := readAllTx[Track](tx, "tracks")
	if err != nil {
		return nil, err
	}
	sortTracks(tracks)
	index := &libraryIndex{revision: revision, catalog: buildCatalog(tracks)}
	if a.index == nil || revision >= a.index.revision {
		a.index = index
	}
	return index, nil
}

func (a *app) readLibraryIndex() (*libraryIndex, error) {
	var index *libraryIndex
	err := a.db.View(func(tx *bolt.Tx) error {
		var err error
		index, err = a.libraryIndexTx(tx)
		return err
	})
	return index, err
}

func (a *app) readAlbumTracks(key string) ([]Track, error) {
	index, err := a.readLibraryIndex()
	if err != nil {
		return nil, err
	}
	positions := index.albumMembers(key)
	tracks := make([]Track, 0, len(positions))
	for _, i := range positions {
		tracks = append(tracks, index.tracks[i])
	}
	return tracks, nil
}

// Mutation-capable consumers receive their own slice. State polling uses the
// immutable aggregates directly, without copying or walking the whole library.
func (a *app) readTracks() ([]Track, error) {
	index, err := a.readLibraryIndex()
	if err != nil {
		return nil, err
	}
	return append([]Track{}, index.tracks...), nil
}
