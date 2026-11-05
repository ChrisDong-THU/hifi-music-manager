package browsebridge

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixture(t *testing.T) *Bridge {
	return fixtureWithArtists(t, false)
}
func fixtureWithArtists(t *testing.T, hasArtists bool) *Bridge {
	return fixtureLibraryState(t, hasArtists, true)
}
func fixtureLibraryState(t *testing.T, hasArtists, hasTracks bool) *Bridge {
	t.Helper()
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/device.xml" {
			_, _ = io.WriteString(w, `<root xmlns="`+deviceNS+`"><device><serviceList><service><serviceType>`+contentNS+`</serviceType><controlURL>/content</controlURL><SCPDURL>/content.xml</SCPDURL></service><service><serviceType>`+connectionNS+`</serviceType><controlURL>/connection</controlURL><SCPDURL>/connection.xml</SCPDURL></service></serviceList></device></root>`)
			return
		}
		data, _ := io.ReadAll(r.Body)
		action, e := soapBody(data)
		if e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		switch action.Name.Local {
		case "GetSystemUpdateID":
			_, _ = w.Write(soap(contentNS, "GetSystemUpdateIDResponse", [][2]string{{"Id", "27"}}))
		case "Browse", "Search":
			didl := `<DIDL-Lite xmlns="` + didlNS + `" xmlns:dc="` + dcNS + `" xmlns:upnp="` + upnpNS + `" xmlns:dlna="urn:schemas-dlna-org:metadata-1-0/"><item id="0$items$1" parentID="0$items" restricted="1"><dc:title>中文 &amp; Test</dc:title><upnp:class>object.item.audioItem.musicTrack</upnp:class><upnp:artist>甲</upnp:artist><upnp:artist role="AlbumArtist">乙</upnp:artist><upnp:albumArtURI dlna:profileID="JPEG_TN">http://192.168.1.5:9790/cover.jpg?a=1&amp;b=2</upnp:albumArtURI><res protocolInfo="http-get:*:audio/flac:DLNA.ORG_OP=01" size="123" duration="0:02:00">http://192.168.1.5:9790/audio.flac?x=1&amp;y=2</res></item></DIDL-Lite>`
			if action.text("ObjectID") == "0" {
				didl = `<DIDL-Lite xmlns="` + didlNS + `"></DIDL-Lite>`
				if hasArtists {
					didl = `<DIDL-Lite xmlns="` + didlNS + `"><container id="0$=Artist" parentID="0" restricted="1"/></DIDL-Lite>`
				}
			} else if !hasTracks {
				didl = `<DIDL-Lite xmlns="` + didlNS + `"></DIDL-Lite>`
			}
			tree, _ := parse([]byte(didl))
			count := fmt.Sprint(len(tree.Children))
			_, _ = w.Write(soap(contentNS, action.Name.Local+"Response", [][2]string{{"Result", didl}, {"NumberReturned", count}, {"TotalMatches", count}, {"UpdateID", "27"}}))
		default:
			_, _ = w.Write(soap(connectionNS, action.Name.Local+"Response", [][2]string{{"Source", "http-get:*:audio/flac:*"}, {"Sink", ""}}))
		}
	}))
	t.Cleanup(native.Close)
	b, e := New(Config{Listen: "192.168.1.5:9792", BackendDescription: native.URL + "/device.xml", UDN: "uuid:12345678-1234-1234-1234-123456789012"})
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestArtistCategoryRemainsBrowsableWhenMinimOmitsEmptyContainer(t *testing.T) {
	for _, test := range []struct{ index, tracks bool }{{false, false}, {false, true}, {true, true}} {
		t.Run(fmt.Sprint(test), func(t *testing.T) {
			b := fixtureLibraryState(t, test.index, test.tracks)
			result := browse(t, b, "artists", "BrowseDirectChildren", "0", "100")
			expected := "0"
			if test.tracks {
				expected = "1"
			}
			if result.text("NumberReturned") != expected || result.text("TotalMatches") != expected {
				t.Fatalf("artist availability %v: %s", test, result.encode())
			}
		})
	}
}
func request(b *Bridge, path, ns, action string, fields [][2]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://192.168.1.5:9792"+path, bytes.NewReader(soap(ns, action, fields)))
	r.RemoteAddr = "192.168.1.3:12345"
	w := httptest.NewRecorder()
	b.Handler().ServeHTTP(w, r)
	return w
}
func browse(t *testing.T, b *Bridge, id, flag, start, count string) *node {
	t.Helper()
	w := request(b, "/upnp/content/control", contentNS, "Browse", [][2]string{{"ObjectID", id}, {"BrowseFlag", flag}, {"Filter", "*"}, {"StartingIndex", start}, {"RequestedCount", count}, {"SortCriteria", ""}})
	if w.Code != 200 {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	n, e := soapBody(w.Body.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func TestFourCategoriesPaginationIcons(t *testing.T) {
	b := fixture(t)
	result := browse(t, b, "0", "BrowseDirectChildren", "0", "0")
	tree, e := parse([]byte(result.text("Result")))
	if e != nil {
		t.Fatal(e)
	}
	want := []string{"专辑", "曲目", "歌手", "我的播放列表"}
	if len(tree.Children) != 4 {
		t.Fatal("root must have exactly four entries")
	}
	for i, n := range tree.Children {
		if n.text("title") != want[i] || n.attr("parentID") != "0" || n.text("albumArtURI") == "" {
			t.Fatalf("bad category: %+v", n)
		}
		name := categories[i].Icon
		image, e := png.Decode(bytes.NewReader(iconPNG(name)))
		if e != nil || image.Bounds().Dx() != iconSize {
			t.Fatal("invalid icon")
		}
	}
	page := browse(t, b, "0", "BrowseDirectChildren", "1", "2")
	if page.text("NumberReturned") != "2" || page.text("TotalMatches") != "4" {
		t.Fatal("pagination broken")
	}
	empty := browse(t, b, "0", "BrowseDirectChildren", "100", "3")
	if empty.text("NumberReturned") != "0" {
		t.Fatal("out of range must be empty")
	}
	meta := browse(t, b, "artists", "BrowseMetadata", "0", "0")
	if !strings.Contains(meta.text("Result"), "歌手") {
		t.Fatal("category metadata missing")
	}
	playlists := browse(t, b, "playlists", "BrowseDirectChildren", "0", "0")
	if playlists.text("NumberReturned") != "0" {
		t.Fatal("empty playlists must remain browsable")
	}
}
func TestMetadataRoundTripAndSearch(t *testing.T) {
	b := fixture(t)
	result := browse(t, b, "tracks", "BrowseDirectChildren", "0", "50")
	tree, e := parse([]byte(result.text("Result")))
	if e != nil {
		t.Fatal(e)
	}
	item := tree.Children[0]
	if item.text("title") != "中文 & Test" || item.attr("parentID") != "tracks" {
		t.Fatal("title or parent changed")
	}
	raw, e := native(item.attr("id"))
	if e != nil || raw != "0$items$1" {
		t.Fatal("stable ID mapping broken")
	}
	res := item.child("res")
	if res.Text != "http://192.168.1.5:9790/audio.flac?x=1&y=2" || res.attr("protocolInfo") != "http-get:*:audio/flac:DLNA.ORG_OP=01" {
		t.Fatal("media URL or protocol metadata changed")
	}
	artists := 0
	for _, n := range item.Children {
		if n.Name.Local == "artist" && n.Name.Space == upnpNS {
			artists++
		}
	}
	if artists != 2 {
		t.Fatal("multiple artist roles lost")
	}
	art := item.child("albumArtURI")
	found := false
	for _, a := range art.Attrs {
		if a.Name.Local == "profileID" && a.Name.Space == "urn:schemas-dlna-org:metadata-1-0/" {
			found = true
		}
	}
	if !found {
		t.Fatal("DLNA namespace lost")
	}
	w := request(b, "/upnp/content/control", contentNS, "Search", [][2]string{{"ContainerID", "0"}, {"SearchCriteria", `dc:title contains "中文"`}, {"Filter", "*"}, {"StartingIndex", "0"}, {"RequestedCount", "20"}, {"SortCriteria", ""}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if e = xml.Unmarshal(w.Body.Bytes(), new(node)); e != nil {
		t.Fatal(e)
	}
	local := element(didlNS, "item", "")
	local.Children = []*node{element(didlNS, "res", "http://127.0.0.1:9790/music/a.flac?x=1&y=2"), element(upnpNS, "albumArtURI", "http://127.0.0.1:9790/cover.png"), element(dcNS, "title", "http://127.0.0.1:9790/keep-this-title")}
	b.publicMediaURLs(local)
	if local.text("res") != "http://192.168.1.5:9790/music/a.flac?x=1&y=2" || local.text("albumArtURI") != "http://192.168.1.5:9790/cover.png" || local.text("title") != "http://127.0.0.1:9790/keep-this-title" {
		t.Fatal("loopback media normalization changed paths or non-resource tags")
	}
}
func TestNetworkAndCallbackBoundaries(t *testing.T) {
	b := fixture(t)
	for _, target := range []struct{ host, remote string }{{"evil.example:9792", "192.168.1.3:1234"}, {"192.168.1.5:9792", "203.0.113.2:1234"}} {
		r := httptest.NewRequest("GET", "http://"+target.host+"/device.xml", nil)
		r.RemoteAddr = target.remote
		w := httptest.NewRecorder()
		b.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("LAN/Host boundary bypass")
		}
	}
	for _, callback := range []string{"<http://127.0.0.1:1234/private>", "<http://192.168.1.2:1234/private>", "<http://example.com:1234/>", "<http://[::1]:1234/>"} {
		r := httptest.NewRequest("SUBSCRIBE", "http://192.168.1.5:9792/upnp/content/event", nil)
		r.RemoteAddr = "192.168.1.3:1234"
		r.Header.Set("NT", "upnp:event")
		r.Header.Set("CALLBACK", callback)
		w := httptest.NewRecorder()
		b.Handler().ServeHTTP(w, r)
		if w.Code != 412 {
			t.Fatal("unsafe callback accepted")
		}
	}
	w := request(b, "/upnp/content/control", contentNS, "DestroyObject", [][2]string{{"ObjectID", "tracks"}})
	if w.Code != 500 || !strings.Contains(w.Body.String(), "401") {
		t.Fatal("write action accepted")
	}
}
