package main

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dhowden/tag"
)

type artworkPresence string

const (
	artworkAbsent  artworkPresence = "absent"
	artworkPresent artworkPresence = "present"
)

func readableArtwork(data []byte) *albumCover {
	if len(data) == 0 || len(data) > maxAlbumCover {
		return nil
	}
	mime := http.DetectContentType(data)
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return &albumCover{Data: data, MIME: mime}
	}
	return nil
}

type artworkSource struct {
	path    string
	sidecar bool
}

func (source artworkSource) read(ctx context.Context, root *os.Root) *albumCover {
	if ctx.Err() != nil || regular(root, source.path) != nil {
		return nil
	}
	f, err := root.Open(source.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if !source.sidecar {
		metadata, err := tag.ReadFrom(&boundedMetadataReader{
			ReadSeeker: operationReadSeeker{ReadSeeker: f, ctx: ctx}, remaining: 64 << 20,
		})
		if err != nil || metadata.Picture() == nil {
			return nil
		}
		return readableArtwork(metadata.Picture().Data)
	}
	data, err := io.ReadAll(io.LimitReader(operationReader{ctx: ctx, r: f}, maxAlbumCover+1))
	if err != nil {
		return nil
	}
	return readableArtwork(data)
}

func readSidecarArtwork(ctx context.Context, root *os.Root, dir string) (artworkSource, *albumCover) {
	if ctx.Err() != nil {
		return artworkSource{}, nil
	}
	entries, err := fs.ReadDir(root.FS(), dir)
	if err != nil {
		return artworkSource{}, nil
	}
	for _, base := range []string{"cover", "folder"} {
		for _, entry := range entries {
			name := strings.ToLower(entry.Name())
			if strings.TrimSuffix(name, path.Ext(name)) != base || !davArtworkName(name) {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || info.Size() > maxAlbumCover {
				continue
			}
			source := artworkSource{path: path.Join(dir, entry.Name()), sidecar: true}
			if cover := source.read(ctx, root); cover != nil {
				return source, cover
			}
		}
	}
	return artworkSource{}, nil
}

type artworkCache struct {
	mu      sync.Mutex
	source  artworkSource
	expires time.Time
}

// Cache only the selected source, including misses, so repeated cover requests
// need no directory scan and never retain megabytes of image data in the index.
func (a *app) releaseArtwork(ctx context.Context, index *libraryIndex, release *albumRelease) *albumCover {
	if release == nil || ctx.Err() != nil {
		return nil
	}
	cache := &release.artworkCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if time.Now().Before(cache.expires) {
		if cache.source.path == "" {
			return nil
		}
		if cover := cache.source.read(ctx, a.music); cover != nil {
			return cover
		}
	}
	source, cover := a.findReleaseArtwork(ctx, index, release)
	if ctx.Err() == nil {
		cache.source, cache.expires = source, time.Now().Add(15*time.Second)
	}
	return cover
}

// Resolve the release's artwork independently of the song displayed first.
// Older tracks take precedence over newly added tracks, including read-only
// formats whose own pictures cannot be changed during import.
func (a *app) findReleaseArtwork(ctx context.Context, index *libraryIndex, release *albumRelease) (artworkSource, *albumCover) {
	if release == nil || ctx.Err() != nil {
		return artworkSource{}, nil
	}
	order := slices.Clone(release.members)
	slices.SortStableFunc(order, func(i, j int) int {
		return strings.Compare(index.tracks[i].Added, index.tracks[j].Added)
	})
	fromTracks := func(fallback bool) (artworkSource, *albumCover) {
		for _, i := range order {
			track := index.tracks[i]
			if track.EmbeddedArtwork == artworkAbsent || track.AlbumArtworkFallback != fallback {
				continue
			}
			source := artworkSource{path: track.Path}
			if cover := source.read(ctx, a.music); cover != nil {
				return source, cover
			}
		}
		return artworkSource{}, nil
	}
	if source, cover := fromTracks(false); cover != nil {
		return source, cover
	}
	if !release.sharedLocation {
		if source, cover := readSidecarArtwork(ctx, a.music, release.location); cover != nil {
			return source, cover
		}
		seen := map[string]bool{release.location: true}
		for _, i := range order {
			dir := path.Dir(index.tracks[i].Path)
			if !seen[dir] {
				seen[dir] = true
				if source, cover := readSidecarArtwork(ctx, a.music, dir); cover != nil {
					return source, cover
				}
			}
		}
	}
	return fromTracks(true)
}

func serveArtwork(w http.ResponseWriter, r *http.Request, cover *albumCover) {
	if cover == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", cover.MIME)
	_, _ = w.Write(cover.Data)
}
