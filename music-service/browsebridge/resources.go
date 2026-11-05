package browsebridge

import (
	"hash/crc32"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	library "hifi.local/library"
)

func (b *Bridge) resourceRoot() (*os.Root, error) {
	if b.cfg.Resources == nil {
		return nil, os.ErrNotExist
	}
	return b.cfg.Resources()
}

func (b *Bridge) decorateArtists(nodes []*node) {
	root, err := b.resourceRoot()
	if err != nil {
		return
	}
	defer root.Close()
	for _, n := range nodes {
		b.decorateArtist(root, n)
	}
}

func (b *Bridge) decorateArtist(root *os.Root, n *node) {
	artist, err := library.LoadArtist(root, n.text("title"))
	if err != nil {
		return
	}
	if artist.ImageHash != "" {
		art := b.base + "/art/artist?name=" + url.QueryEscape(artist.Name) + "&v=" + artist.ImageHash
		if old := n.child("albumArtURI"); old != nil {
			old.Text = art
			old.Attrs = nil // The replacement may have a different image format/profile.
		} else {
			n.Children = append(n.Children, element(upnpNS, "albumArtURI", art))
		}
	}
	if artist.Biography != "" {
		if old := n.child("description"); old != nil {
			old.Text = artist.Biography
		} else {
			n.Children = append(n.Children, element(dcNS, "description", artist.Biography))
		}
	}
}

func (b *Bridge) artistPicture(w http.ResponseWriter, r *http.Request) {
	root, err := b.resourceRoot()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	artist, err := library.LoadArtist(root, r.URL.Query().Get("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if revision := r.URL.Query().Get("v"); revision != "" {
		artist.ImageHash = revision
	}
	data, err := library.Picture(root, artist)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("ETag", `"`+artist.ImageHash+`"`)
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if r.Header.Get("If-None-Match") == w.Header().Get("ETag") {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(data)
}

func (b *Bridge) combinedUpdate(native string) string {
	value, _ := strconv.ParseUint(strings.TrimSpace(native), 10, 32)
	if b.cfg.ResourceKey != nil {
		value += uint64(crc32.ChecksumIEEE([]byte(b.cfg.ResourceKey())))
	}
	if root, err := b.resourceRoot(); err == nil {
		value += uint64(library.Revision(root))
		root.Close()
	}
	return strconv.FormatUint(uint64(uint32(value)), 10)
}
