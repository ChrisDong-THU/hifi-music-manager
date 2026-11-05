package main

import (
	"path/filepath"
	"strings"
	"testing"

	library "hifi.local/library"
)

func TestResourceIdentityFollowsValidatedSupervisorSelection(t *testing.T) {
	a := &LibraryService{home: t.TempDir()}
	state := LibraryState{Mount: t.TempDir(), Volume: strings.Repeat("ab", 24), MountID: "generation-a"}
	state.Selected, state.DataRoot = library.MusicPath(state.Mount), library.DataPath(state.Mount)
	publish := func() {
		t.Helper()
		if err := a.writeLibraryState(state); err != nil {
			t.Fatal(err)
		}
	}
	publish()
	first := a.libraryKey()
	state.MountID = "generation-b"
	publish()
	if first == a.libraryKey() || first == "offline" {
		t.Fatal("mount generation did not invalidate resource identity")
	}
	state.Paused = true
	publish()
	if a.libraryKey() != "offline" {
		t.Fatal("paused selection reused online cache identity")
	}
	state.Paused = false
	state.Selected = filepath.Join(t.TempDir(), "music")
	publish()
	if a.libraryKey() != "offline" {
		t.Fatal("inconsistent selection reused online cache identity")
	}
	if root, err := a.openArtists(); err == nil {
		root.Close()
		t.Fatal("inconsistent selection exposed resources")
	}
}
