package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	library "hifi.local/library"
)

func initializeLibrary(d USBDrive, stillMounted func(USBDrive) bool) (string, error) {
	return library.InitializeDisk(d, stillMounted)
}

func readVolumeID(path string) (string, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return volumeIDFromRoot(root)
}

func usbFixture(t *testing.T, name string) USBDrive {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	handle, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	id, err := library.DirectoryMountID(handle)
	handle.Close()
	if err != nil {
		t.Fatal(err)
	}
	return USBDrive{Device: "/dev/" + name, Root: root, MountID: id, Filesystem: "exfat", Writable: true}
}
func markedUSB(t *testing.T, name string) USBDrive {
	t.Helper()
	d := usbFixture(t, name)
	if _, err := initializeLibrary(d, func(USBDrive) bool { return true }); err != nil {
		t.Fatal(err)
	}
	return d
}
func watcherFixture(t *testing.T) (*libraryWatcher, *[]USBDrive, *[]string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	a := &LibraryService{home: t.TempDir()}
	drives := []USBDrive{}
	events := []string{}
	w := &libraryWatcher{a: a, list: func(context.Context) ([]USBDrive, error) { return drives, nil }, check: func(d USBDrive) bool {
		for _, current := range drives {
			if sameDrive(d, current) {
				return true
			}
		}
		return false
	}, player: func(_ context.Context, action string) error { events = append(events, action); return nil }, link: func(path string) error { events = append(events, "link:"+path); return nil }, online: func(volume string) (string, error) {
		events = append(events, "index:"+volume)
		return volume, nil
	}}
	return w, &drives, &events
}
func stateFixture(t *testing.T, a *LibraryService) LibraryState {
	t.Helper()
	var state LibraryState
	raw, err := os.ReadFile(filepath.Join(a.home, "logs", "library-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}
func TestAutoUSBPrefersProjectAndPreservesOtherDisks(t *testing.T) {
	w, drives, _ := watcherFixture(t)
	blank := usbFixture(t, "blank")
	music := usbFixture(t, "music")
	if err := os.Mkdir(filepath.Join(music.Root, "music"), 0700); err != nil {
		t.Fatal(err)
	}
	project := markedUSB(t, "project")
	*drives = []USBDrive{blank, music, project}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := stateFixture(t, w.a)
	if state.Device != project.Device {
		t.Fatal("did not prefer project disk")
	}
	for _, untouched := range []USBDrive{blank, music} {
		if _, err := os.Stat(filepath.Join(untouched.Root, library.DataDirectory)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("initialized an unselected disk")
		}
	}
}
func TestBlankUSBRequiresExplicitInitialization(t *testing.T) {
	w, drives, events := watcherFixture(t)
	a, b := usbFixture(t, "a"), usbFixture(t, "b")
	*drives = []USBDrive{b, a}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.current != nil {
		t.Fatal("uninitialized disk selected without confirmation")
	}
	for _, drive := range *drives {
		if _, err := os.Stat(library.DataPath(drive.Root)); !os.IsNotExist(err) {
			t.Fatal("background initialized an unrelated disk")
		}
	}
	if _, err := initializeLibrary(a, w.check); err != nil {
		t.Fatal(err)
	}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.current == nil || w.current.drive.Root != a.Root {
		t.Fatal("explicitly initialized disk not selected")
	}
	before := len(*events)
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*events) != before+1 || (*events)[len(*events)-1] != "ensure" {
		t.Fatal("unnecessary rescan")
	}
}
func TestAutoUSBExistingMusicAndLegacyIdentity(t *testing.T) {
	d := usbFixture(t, "legacy")
	_ = os.MkdirAll(filepath.Join(library.DataPath(d.Root), "music"), 0700)
	_ = os.MkdirAll(library.DataPath(d.Root), 0700)
	id := newID()
	_ = os.WriteFile(filepath.Join(library.DataPath(d.Root), "library-id"), []byte(id), 0600)
	audio := filepath.Join(library.DataPath(d.Root), "music", "keep.flac")
	_ = os.WriteFile(audio, []byte("original audio"), 0600)
	actual, err := initializeLibrary(d, func(USBDrive) bool { return true })
	if err != nil || actual != id {
		t.Fatal("legacy identity changed", err)
	}
	again, err := initializeLibrary(d, func(USBDrive) bool { return true })
	if err != nil || again != id {
		t.Fatal("initialization not idempotent")
	}
	raw, _ := os.ReadFile(audio)
	if string(raw) != "original audio" {
		t.Fatal("existing audio changed")
	}
}
func TestAutoUSBRemovalStopsPlayerAndRestoresNewDisk(t *testing.T) {
	w, drives, events := watcherFixture(t)
	one, two := markedUSB(t, "one"), markedUSB(t, "two")
	*drives = []USBDrive{one}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	volume := w.current.volume
	*drives = []USBDrive{}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.current != nil {
		t.Fatal("removed volume is still selected")
	}
	if stateFixture(t, w.a).Selected != "" {
		t.Fatal("removed disk still online")
	}
	*drives = []USBDrive{two}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.current.volume == volume || w.current.drive.Device != two.Device {
		t.Fatal("did not select new volume")
	}
	foundStop := false
	for _, event := range *events {
		foundStop = foundStop || event == "stop"
	}
	if !foundStop {
		t.Fatal("player not stopped on disk removal")
	}
}
func TestAutoUSBMountGenerationChangesEvenForSamePath(t *testing.T) {
	w, drives, events := watcherFixture(t)
	disk := markedUSB(t, "disk")
	*drives = []USBDrive{disk}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := len(*events)
	disk.MountID = "remounted-instance"
	*drives = []USBDrive{disk}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	newEvents := (*events)[before:]
	if len(newEvents) != 4 || newEvents[0] != "stop" || newEvents[2] != "ensure" || !strings.HasPrefix(newEvents[3], "index:") {
		t.Fatalf("remount must stop, relink, restart and rescan: %v", newEvents)
	}
	if stateFixture(t, w.a).MountID != disk.MountID {
		t.Fatal("mount generation not updated")
	}
}
func TestAutoUSBPauseIsRuntimeOnlyAndLegacyPauseIgnored(t *testing.T) {
	w, drives, _ := watcherFixture(t)
	disk := markedUSB(t, "disk")
	*drives = []USBDrive{disk}
	_ = os.WriteFile(filepath.Join(w.a.home, "library.paused"), nil, 0600)
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stateFixture(t, w.a).Selected == "" {
		t.Fatal("legacy pause blocked startup")
	}
	_ = os.WriteFile(w.a.pausePath(), nil, 0600)
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !stateFixture(t, w.a).Paused {
		t.Fatal("runtime pause ignored")
	}
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stateFixture(t, w.a).Selected == "" || stateFixture(t, w.a).Paused {
		t.Fatal("pause survived simulated reboot runtime directory")
	}
}
func TestAutoUSBRejectsConflictingDirectoriesAndReadOnly(t *testing.T) {
	w, drives, _ := watcherFixture(t)
	bad := usbFixture(t, "bad")
	_ = os.WriteFile(filepath.Join(bad.Root, "music"), []byte("keep"), 0600)
	ro := usbFixture(t, "readonly")
	ro.Writable = false
	good := markedUSB(t, "good")
	*drives = []USBDrive{bad, ro, good}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.current.drive.Root != good.Root {
		t.Fatal("selected invalid disk")
	}
	raw, _ := os.ReadFile(filepath.Join(bad.Root, "music"))
	if string(raw) != "keep" {
		t.Fatal("overwrote conflicting file")
	}
	if _, err := os.Stat(filepath.Join(ro.Root, "music")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wrote to read-only disk")
	}
}
func TestAutoUSBRejectsSymlinks(t *testing.T) {
	d := usbFixture(t, "linked")
	outside := t.TempDir()
	if err := os.Mkdir(library.DataPath(d.Root), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, library.MusicPath(d.Root)); err != nil {
		t.Skip("symlink permission unavailable")
	}
	if _, err := rankDrive(d); err == nil {
		t.Fatal("accepted symlink music directory")
	}
	if _, err := initializeLibrary(d, func(USBDrive) bool { return true }); err == nil {
		t.Fatal("initialized through symlink")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote outside disk")
	}
}
func TestAutoUSBDoesNotInitializeLostMount(t *testing.T) {
	d := usbFixture(t, "lost")
	if _, err := initializeLibrary(d, func(USBDrive) bool { return false }); err == nil {
		t.Fatal("accepted lost mount")
	}
	entries, _ := os.ReadDir(d.Root)
	if len(entries) != 0 {
		t.Fatal("wrote into unmounted directory")
	}
}
func TestAutoUSBKeepsCurrentDiskAmongEqualCandidates(t *testing.T) {
	a, b := markedUSB(t, "a"), markedUSB(t, "b")
	id, _ := readVolumeID(b.Root)
	current := &libraryChoice{drive: b, rank: 3, volume: id}
	choices, issues := chooseDrives([]USBDrive{a, b}, current)
	if len(issues) != 0 || !reflect.DeepEqual(choices[0].drive, b) {
		t.Fatal("current disk was unnecessarily replaced")
	}
}
func TestAutoUSBDiscoveryFailureInvalidatesSelection(t *testing.T) {
	w, drives, _ := watcherFixture(t)
	*drives = []USBDrive{markedUSB(t, "disk")}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.list = func(context.Context) ([]USBDrive, error) { return nil, errors.New("lsblk unavailable") }
	if err := w.step(context.Background()); err == nil {
		t.Fatal("expected discovery error")
	}
	if w.current != nil || stateFixture(t, w.a).Selected != "" {
		t.Fatal("failed discovery left writable selection")
	}
}

func TestExplicitDiskPreferenceDoesNotFallBackToAnotherDisk(t *testing.T) {
	w, drives, _ := watcherFixture(t)
	a, b := markedUSB(t, "a"), markedUSB(t, "b")
	id, err := readVolumeID(a.Root)
	if err != nil {
		t.Fatal(err)
	}
	if err := library.WritePreference(w.a.home, id); err != nil {
		t.Fatal(err)
	}
	*drives = []USBDrive{b}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.current != nil {
		t.Fatal("selected another disk while the preferred disk was absent")
	}
	*drives = []USBDrive{b, a}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.current == nil || w.current.volume != id {
		t.Fatal("did not restore selected disk")
	}
}
