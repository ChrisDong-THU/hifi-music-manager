package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSeparateUSBIndexesAndResourcesSurviveSwitchBack(t *testing.T) {
	diskA, diskB := t.TempDir(), t.TempDir()
	open := func(disk string) *app {
		home := filepath.Join(disk, "manager")
		a, err := newAppStorage(home, filepath.Join(disk, "music"), home, nil)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	cover := testAlbumCover(t, false)
	a := open(diskA)
	addTrack(t, a, "a.flac", "A", 1)
	if _, err := a.saveArtistProfile("测试歌手", "A biography", cover); err != nil {
		t.Fatal(err)
	}
	if err := a.put("batches", "draft-A", Batch{ID: "draft-A", Name: "A draft"}); err != nil {
		t.Fatal(err)
	}
	a.close()
	b := open(diskB)
	if tracks, _ := b.readTracks(); len(tracks) != 0 {
		t.Fatal("A's index leaked into B")
	}
	if profile := b.artistProfile("测试歌手"); profile.ImageHash != "" {
		t.Fatal("A's portrait leaked into B")
	}
	if batches, _ := readAll[Batch](b, "batches"); len(batches) != 0 {
		t.Fatal("A's drafts leaked into B")
	}
	addTrack(t, b, "b.flac", "B", 2)
	if _, err := b.saveArtistProfile("测试歌手", "B biography", testAlbumCover(t, true)); err != nil {
		t.Fatal(err)
	}
	b.close()
	a = open(diskA)
	defer a.close()
	tracks, _ := a.readTracks()
	if len(tracks) != 1 || tracks[0].Tags.Title != "A" {
		t.Fatal("A's index was replaced")
	}
	if profile := a.artistProfile("测试歌手"); profile.Biography != "A biography" {
		t.Fatal("A's profile was replaced")
	}
	if image := request(a, "GET", a.artistProfile("测试歌手").imageURL(), "", false); !bytes.Equal(image.Body.Bytes(), cover.Data) {
		t.Fatal("A's portrait lost")
	}
	if _, err := get[Batch](a, "batches", "draft-A"); err != nil {
		t.Fatal("A's draft lost")
	}
	// The public portrait survives loss/rebuild of the private Web index.
	a.close()
	a.db = nil
	a.music = nil
	a.data = nil
	if err := os.Remove(filepath.Join(diskA, "manager", "manager.db")); err != nil {
		t.Fatal(err)
	}
	rebuilt := open(diskA)
	defer rebuilt.close()
	if profile := rebuilt.artistProfile("测试歌手"); profile.Biography != "A biography" {
		t.Fatal("public resource depended on the private index")
	}
}

func TestPlaybackFailurePersistsPendingNotificationWithoutFailingSavedResources(t *testing.T) {
	a := testApp(t)
	addTrack(t, a, "a.flac", "A", 1)
	a.rescan = func(context.Context) error { return errors.New("backend offline") }
	if _, err := a.saveArtistProfile("测试歌手", "saved", testAlbumCover(t, false)); err != nil {
		t.Fatal(err)
	}
	if err := a.syncPlayback(context.Background()); err == nil {
		t.Fatal("offline backend unexpectedly acknowledged")
	}
	state := a.playbackSyncState()
	if state.Requested == 0 || state.Delivered != 0 || state.Error == "" {
		t.Fatalf("pending state was not committed: %+v", state)
	}
	for _, job := range a.taskSnapshot() {
		if job.Kind == "sync" && job.State != "running" {
			t.Fatal("a retryable notification was presented as a failed resource operation")
		}
	}
	a.rescan = func(context.Context) error { return nil }
	if err := a.syncPlayback(context.Background()); err != nil {
		t.Fatal(err)
	}
	state = a.playbackSyncState()
	if state.Requested != state.Delivered || state.Error != "" {
		t.Fatal("retry did not acknowledge the pending change")
	}
}
