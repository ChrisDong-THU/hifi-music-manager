package library

import (
	"bytes"
	"os"
	"testing"
)

func TestArtistResourceCommitAndImmutablePictureIntegrity(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	before, err := SaveArtist(root, Artist{Name: "A / B", Biography: "first", ImageType: "image/png"}, []byte("first image"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := SaveArtist(root, Artist{Name: "A / B", Biography: "second", ImageType: "image/png"}, []byte("second image"))
	if err != nil {
		t.Fatal(err)
	}
	if before.ImageHash == after.ImageHash || Revision(root) != 2 {
		t.Fatal("resource revision did not change")
	}
	loaded, err := LoadArtist(root, "A / B")
	if err != nil || loaded.Biography != "second" {
		t.Fatal("profile commit failed", err)
	}
	image, err := Picture(root, loaded)
	if err != nil || !bytes.Equal(image, []byte("second image")) {
		t.Fatal("picture mismatch", err)
	}
	if _, err := LoadArtist(root, "../other"); !os.IsNotExist(err) {
		t.Fatal("artist names escaped their hashed resource identity", err)
	}
	if _, err := Picture(root, Artist{Name: "A / B", ImageHash: "../../escape", ImageType: "image/png"}); err == nil {
		t.Fatal("invalid image identity accepted")
	}
}
