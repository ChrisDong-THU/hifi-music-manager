package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateRejectsInconsistentSelectionsAndPreservesPublishedSnapshot(t *testing.T) {
	home, mount := t.TempDir(), filepath.Join(t.TempDir(), "USB")
	state := State{Mount: mount, Selected: MusicPath(mount), DataRoot: DataPath(mount),
		Volume: strings.Repeat("ab", 24), MountID: "mount-generation", Available: []string{MusicPath(mount)}}
	if err := state.ValidateSelection(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*State){
		func(s *State) { s.Paused = true },
		func(s *State) { s.Selected = filepath.Join(t.TempDir(), "music") },
		func(s *State) { s.DataRoot = t.TempDir() },
		func(s *State) { s.Mount = "relative" },
		func(s *State) { s.MountID = "" },
		func(s *State) { s.Volume = strings.Repeat("z", 48) },
	} {
		invalid := state
		mutate(&invalid)
		if invalid.ValidateSelection() == nil {
			t.Fatal("inconsistent selection accepted", invalid)
		}
	}
	if err := WriteState(home, state); err != nil {
		t.Fatal(err)
	}
	// A leftover file from the previous writer must never be truncated or used.
	oldTemp := filepath.Join(home, "logs", "library-state.next")
	if err := os.WriteFile(oldTemp, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	state.Message = "音乐盘就绪"
	if err := WriteState(home, state); err != nil {
		t.Fatal(err)
	}
	invalid := state
	invalid.Message = strings.Repeat("x", maxStateBytes)
	if err := WriteState(home, invalid); err == nil {
		t.Fatal("oversized state published")
	}
	got, err := ReadState(home)
	if err != nil || got.Volume != state.Volume || got.MountID != state.MountID || got.Message != state.Message || got.UpdatedAt == "" {
		t.Fatal("last complete snapshot was not preserved", got, err)
	}
	if data, err := os.ReadFile(oldTemp); err != nil || string(data) != "keep" {
		t.Fatal("legacy temporary file was touched")
	}
	files, err := os.ReadDir(filepath.Join(home, "logs"))
	if err != nil || len(files) != 2 {
		t.Fatal("temporary state file leaked", files, err)
	}
}

func TestReadStateBoundsInputAndAcceptsCompatibleExtraFields(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "logs", "library-state.json")
	for _, data := range []string{`{} {}`, strings.Repeat(" ", maxStateBytes) + `{}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadState(home); err == nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
	if err := os.WriteFile(path, []byte(`{"paused":true,"future_field":"compatible"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if state, err := ReadState(home); err != nil || !state.Paused {
		t.Fatal("compatible snapshot rejected", err)
	}
}
