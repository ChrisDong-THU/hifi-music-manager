package browsebridge

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	library "hifi.local/library"
)

func TestArtistPortraitUsesSharedResourcesWithoutWebService(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	profile, err := library.SaveArtist(root, library.Artist{Name: "乙", Biography: "Biography", ImageType: "image/png"}, []byte("portrait"))
	if err != nil {
		t.Fatal(err)
	}
	b := fixture(t)
	b.cfg.Resources = func() (*os.Root, error) { return os.OpenRoot(directory) }
	result := browse(t, b, "artists", "BrowseDirectChildren", "0", "100")
	if !strings.Contains(result.text("Result"), profile.ImageHash) || !strings.Contains(result.text("Result"), "Biography") {
		t.Fatal("shared portrait/biography missing")
	}
	r := httptest.NewRequest("GET", b.base+"/art/artist?name=%E4%B9%99&v="+profile.ImageHash, nil)
	r.RemoteAddr = "192.168.1.3:1234"
	w := httptest.NewRecorder()
	b.Handler().ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "portrait" {
		t.Fatal("music service did not serve public resource")
	}
	if b.combinedUpdate("27") != "28" {
		t.Fatal("resource change did not affect UPnP update ID")
	}
	if _, err := library.SaveArtist(root, library.Artist{Name: "乙", ImageType: "image/jpeg"}, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	old := httptest.NewRecorder()
	b.Handler().ServeHTTP(old, r)
	if old.Code != 404 {
		t.Fatal("superseded portrait was retained")
	}
	// Repeated browses must not remap the cached native IDs a second time.
	for i := 0; i < 3; i++ {
		items := browse(t, b, artistID("乙"), "BrowseDirectChildren", "0", "100")
		tree, _ := parse([]byte(items.text("Result")))
		if tree.Children[0].attr("id") != mapped("0$items$1") {
			t.Fatal("cached node was mutated")
		}
	}
}
