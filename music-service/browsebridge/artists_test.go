package browsebridge

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHiddenArtistIndexPreservesAlbumArtistAndPlayableChildren(t *testing.T) {
	b := fixtureLibraryState(t, false, true)
	index := browse(t, b, "artists", "BrowseDirectChildren", "0", "100")
	tree, err := parse([]byte(index.text("Result")))
	if err != nil || len(tree.Children) != 1 {
		t.Fatal(err)
	}
	artist := tree.Children[0]
	if artist.text("title") != "乙" || !strings.HasPrefix(artist.attr("id"), "a:") {
		t.Fatal("main Artist index must prefer AlbumArtist")
	}
	metadata := browse(t, b, artist.attr("id"), "BrowseMetadata", "0", "0")
	if !strings.Contains(metadata.text("Result"), "object.container.person.musicArtist") {
		t.Fatal("artist metadata missing")
	}
	items := browse(t, b, artist.attr("id"), "BrowseDirectChildren", "0", "1")
	children, err := parse([]byte(items.text("Result")))
	if err != nil || len(children.Children) != 1 {
		t.Fatal(err)
	}
	item := children.Children[0]
	if item.attr("parentID") != artist.attr("id") || item.child("res") == nil || item.child("res").attr("protocolInfo") == "" {
		t.Fatal("playable metadata lost")
	}
	if actual, err := native(item.attr("id")); err != nil || actual != "0$items$1" {
		t.Fatal("native track identity changed")
	}
	page := browse(t, b, "artists", "BrowseDirectChildren", "1", "100")
	if page.text("NumberReturned") != "0" || page.text("TotalMatches") != "1" {
		t.Fatal("artist pagination incorrect")
	}
	if w := request(b, "/upnp/content/control", contentNS, "Browse", [][2]string{{"ObjectID", "a:invalid=="}, {"BrowseFlag", "BrowseDirectChildren"}, {"Filter", "*"}, {"StartingIndex", "0"}, {"RequestedCount", "0"}, {"SortCriteria", ""}}); w.Code != 500 || !strings.Contains(w.Body.String(), "701") {
		t.Fatal("invalid artist ID accepted")
	}
}

func TestArtistGroupingRejectsRepeatedTrackIDsAcrossPages(t *testing.T) {
	b := groupFixture(t, func(action *node) [][2]string {
		if action.Name.Local == "GetSystemUpdateID" {
			return [][2]string{{"Id", "27"}}
		}
		// Both pages have valid counts and the same update ID, but repeat a track.
		return [][2]string{{"Result", fmt.Sprintf(`<DIDL-Lite xmlns="%s"><item id="repeated"/></DIDL-Lite>`, didlNS)}, {"NumberReturned", "1"}, {"TotalMatches", "2"}, {"UpdateID", "27"}}
	})
	if _, _, err := b.groupedArtists(context.Background()); err == nil || b.artistGroups != nil {
		t.Fatal("repeated native track committed to artist cache")
	}
}

func TestCanceledArtistRequestDoesNotWaitForFullLibraryTraversal(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	versions := make(chan struct{}, 2)
	b := groupFixture(t, func(action *node) [][2]string {
		if action.Name.Local == "GetSystemUpdateID" {
			versions <- struct{}{}
			return [][2]string{{"Id", "27"}}
		}
		close(entered)
		<-release
		return [][2]string{{"Result", `<DIDL-Lite xmlns="` + didlNS + `"/>`}, {"NumberReturned", "0"}, {"TotalMatches", "0"}, {"UpdateID", "27"}}
	})
	done := make(chan error, 1)
	go func() { _, _, err := b.groupedArtists(context.Background()); done <- err }()
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	<-entered
	<-versions
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() { _, _, err := b.groupedArtists(ctx); canceled <- err }()
	<-versions // The second request has reached the grouping stage.
	cancel()
	select {
	case err := <-canceled:
		if err != context.Canceled {
			t.Fatal("artist waiter lost cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled request still waits for full traversal")
	}
}

func TestArtistCacheUsesDiskGenerationWithoutOpeningPublicResources(t *testing.T) {
	browses, opens := 0, 0
	key := "disk-a:generation-1"
	b := groupFixture(t, func(action *node) [][2]string {
		if action.Name.Local == "GetSystemUpdateID" {
			return [][2]string{{"Id", "27"}}
		}
		browses++
		return [][2]string{{"Result", `<DIDL-Lite xmlns="` + didlNS + `"/>`}, {"NumberReturned", "0"}, {"TotalMatches", "0"}, {"UpdateID", "27"}}
	})
	b.cfg.ResourceKey = func() string { return key }
	b.cfg.Resources = func() (*os.Root, error) { opens++; return nil, os.ErrNotExist }
	for i := 0; i < 3; i++ {
		if i == 2 {
			key = "disk-a:generation-2"
		}
		if _, _, err := b.groupedArtists(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if browses != 2 || opens != 0 {
		t.Fatal("cache did not follow the disk generation independently of public resources", browses, opens)
	}
}

func TestMainArtistGroupingDeduplicatesAndFallsBack(t *testing.T) {
	track := element(didlNS, "item", "")
	track.Children = []*node{element(upnpNS, "artist", "Singer"), element(upnpNS, "artist", "Singer")}
	if names := mainArtists(track); len(names) != 2 || names[0] != "Singer" {
		t.Fatal(names)
	}
	album := element(upnpNS, "artist", "Compilation")
	album.setAttr("role", "AlbumArtist")
	track.Children = append(track.Children, album)
	if names := mainArtists(track); len(names) != 1 || names[0] != "Compilation" {
		t.Fatal(names)
	}
	if names := mainArtists(element(didlNS, "item", "")); len(names) != 1 || names[0] != "未知歌手" {
		t.Fatal(names)
	}
}
