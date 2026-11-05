package browsebridge

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MinimServer's Intelligent Browsing omits an index when its value applies to
// every track. Derive that missing Artist view from its unmodified metadata.
// AlbumArtist has the same precedence as MinimServer's main Artist index.
func mainArtists(track *node) []string {
	var artists, albumArtists []string
	for _, child := range track.Children {
		if child.Name.Space != upnpNS || child.Name.Local != "artist" {
			continue
		}
		name := strings.TrimSpace(child.Text)
		if name == "" {
			continue
		}
		if child.attr("role") == "AlbumArtist" {
			albumArtists = append(albumArtists, name)
		} else {
			artists = append(artists, name)
		}
	}
	if len(albumArtists) != 0 {
		return albumArtists
	}
	if len(artists) != 0 {
		return artists
	}
	if creator := strings.TrimSpace(track.text("creator")); creator != "" {
		return []string{creator}
	}
	return []string{"未知歌手"}
}

func artistID(name string) string { return "a:" + base64.RawURLEncoding.EncodeToString([]byte(name)) }

func pageNodes(nodes []*node, start, count uint64) []*node {
	if start >= uint64(len(nodes)) {
		return nil
	}
	end := uint64(len(nodes))
	if count > 0 && count < end-start {
		end = start + count
	}
	return nodes[start:end]
}

func (b *Bridge) artistContainer(name string, tracks []*node) *node {
	n := element(didlNS, "container", "")
	n.setAttr("id", artistID(name))
	n.setAttr("parentID", "artists")
	n.setAttr("restricted", "1")
	n.setAttr("searchable", "0")
	n.setAttr("childCount", strconv.Itoa(len(tracks)))
	n.Children = []*node{element(dcNS, "title", name), element(upnpNS, "class", "object.container.person.musicArtist")}
	art := b.base + "/icons/artists.png?v=" + iconRevision
	if len(tracks) > 0 {
		if cover := tracks[0].child("albumArtURI"); cover != nil && cover.Text != "" {
			art = cover.Text
		}
	}
	n.Children = append(n.Children, element(upnpNS, "albumArtURI", art))
	b.publicMediaURLs(n)
	return n
}

func (b *Bridge) fallbackArtists(w http.ResponseWriter, r *http.Request, id, flag string, start, count uint64) {
	name := ""
	if id != "artists" {
		if !strings.HasPrefix(id, "a:") || len(id) > 8192 {
			fault(w, 701, "No Such Object")
			return
		}
		decoded, err := base64.RawURLEncoding.DecodeString(id[2:])
		if err != nil || len(decoded) == 0 || !utf8.Valid(decoded) || strings.ContainsRune(string(decoded), 0) {
			fault(w, 701, "No Such Object")
			return
		}
		name = string(decoded)
	}
	groups, update, err := b.groupedArtists(r.Context())
	if err != nil {
		fault(w, 501, "Music library offline")
		return
	}
	tree := element(didlNS, "DIDL-Lite", "")
	total := 0
	if id == "artists" {
		names := make([]string, 0, len(groups))
		for artist := range groups {
			names = append(names, artist)
		}
		sort.Strings(names)
		total = len(names)
		if flag == "BrowseDirectChildren" {
			if start >= uint64(len(names)) {
				names = nil
			} else {
				end := uint64(len(names))
				if count > 0 && count < end-start {
					end = start + count
				}
				names = names[start:end]
			}
		}
		for _, artist := range names {
			tree.Children = append(tree.Children, b.artistContainer(artist, groups[artist]))
		}
	} else {
		selected, exists := groups[name]
		if !exists {
			fault(w, 701, "No Such Object")
			return
		}
		if flag == "BrowseMetadata" {
			tree.Children = append(tree.Children, b.artistContainer(name, selected))
			total = 1
		} else {
			total = len(selected)
			for _, track := range pageNodes(selected, start, count) {
				track = cloneNode(track)
				track.setAttr("id", mapped(track.attr("id")))
				track.setAttr("parentID", id)
				if ref := track.attr("refID"); ref != "" {
					track.setAttr("refID", mapped(ref))
				}
				b.publicMediaURLs(track)
				tree.Children = append(tree.Children, track)
			}
		}
	}
	if id == "artists" || flag == "BrowseMetadata" {
		b.decorateArtists(tree.Children)
	}
	b.browseResponse(w, tree, total, update)
}

func cloneNode(n *node) *node {
	copy := *n
	copy.Attrs = append(copy.Attrs[:0:0], n.Attrs...)
	copy.Children = make([]*node, len(n.Children))
	for i, child := range n.Children {
		copy.Children[i] = cloneNode(child)
	}
	return &copy
}

func (b *Bridge) groupedArtists(ctx context.Context) (map[string][]*node, string, error) {
	version, err := b.call(ctx, contentNS, "GetSystemUpdateID", nil)
	if err != nil {
		return nil, "", err
	}
	update := version.text("Id")
	key := b.artistCacheKey(update)
	if err := b.artistsGate.lock(ctx); err != nil {
		return nil, "", err
	}
	defer b.artistsGate.unlock()
	if b.artistGroups != nil && key == b.artistVersion {
		return b.artistGroups, update, nil
	}
	groups := map[string][]*node{}
	metadataBytes := 0
	identities := map[string]bool{}
	expectedTotal := -1
	for start := 0; ; {
		response, err := b.call(ctx, contentNS, "Browse", [][2]string{{"ObjectID", "0$items"}, {"BrowseFlag", "BrowseDirectChildren"}, {"Filter", "*"}, {"StartingIndex", strconv.Itoa(start)}, {"RequestedCount", "256"}, {"SortCriteria", ""}})
		if err != nil {
			return nil, "", err
		}
		metadataBytes += len(response.text("Result"))
		if metadataBytes > 32<<20 {
			return nil, "", errors.New("artist fallback metadata exceeds cache limit")
		}
		if response.text("UpdateID") != update {
			return nil, "", errors.New("music library changed while grouping artists; retry")
		}
		tree, err := parseDIDL([]byte(response.text("Result")))
		if err != nil {
			return nil, "", err
		}
		total, err := strconv.Atoi(response.text("TotalMatches"))
		returned, countErr := strconv.Atoi(response.text("NumberReturned"))
		if err != nil || total < 0 || total > 100000 || countErr != nil || returned != len(tree.Children) || returned > 256 || start+returned > total || (expectedTotal >= 0 && total != expectedTotal) {
			return nil, "", errors.New("invalid library size")
		}
		expectedTotal = total
		for _, track := range tree.Children {
			id := track.attr("id")
			if track.Name.Space != didlNS || track.Name.Local != "item" || id == "" || identities[id] {
				return nil, "", errors.New("invalid or repeated track identity")
			}
			identities[id] = true
			seen := map[string]bool{}
			for _, artist := range mainArtists(track) {
				if !seen[artist] {
					groups[artist] = append(groups[artist], track)
					seen[artist] = true
				}
			}
		}
		start += len(tree.Children)
		if start >= total {
			break
		}
		if len(tree.Children) == 0 {
			return nil, "", errors.New("incomplete library page")
		}
	}
	latest, err := b.call(ctx, contentNS, "GetSystemUpdateID", nil)
	if err != nil {
		return nil, "", err
	}
	if latest.text("Id") != update || key != b.artistCacheKey(update) {
		return nil, "", errors.New("music library changed while grouping artists; retry")
	}
	b.artistGroups, b.artistVersion = groups, key
	return groups, update, nil
}

func (b *Bridge) artistCacheKey(update string) string {
	if b.cfg.ResourceKey != nil {
		// The supervisor's volume and mount generation already identify the
		// source. Public portraits are decorated after paging, not cached here.
		return update + "|" + b.cfg.ResourceKey()
	}
	if root, err := b.resourceRoot(); err == nil {
		defer root.Close()
		return update + "|" + root.Name()
	}
	return update
}
