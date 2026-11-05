package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCancelledScanKeepsCommittedIndex(t *testing.T) {
	a := testApp(t)
	original := addTrack(t, a, "01.flac", "Indexed", 1)
	if err := os.WriteFile(filepath.Join(a.music.Name(), "02.flac"), toneFLAC("New", 2), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.ctx = ctx
	err := a.scanWithProgress(func(stats libraryStats, done, total int) {
		a.reportScanProgress(stats, done, total)
		if done == 1 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("scan did not stop on cancellation", err)
	}
	tracks, err := a.readTracks()
	if err != nil || len(tracks) != 1 || tracks[0].ID != original.ID || a.scanning != nil {
		t.Fatal("cancelled scan changed the committed index or retained progress", tracks, err)
	}
}

func TestScanProgressIsLiveWhileCommittedIndexRemainsComplete(t *testing.T) {
	a := testApp(t)
	first := addTrack(t, a, "01.flac", "First", 1)
	second := addTrack(t, a, "02.flac", "Second", 2)
	third := toneFLAC("Third", 3)
	if err := os.WriteFile(filepath.Join(a.music.Name(), "03.flac"), third, 0600); err != nil {
		t.Fatal(err)
	}
	a.jobs["scan-test"] = &task{ID: "scan-test", Kind: "scan", State: "running"}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- a.scanWithProgress(func(stats libraryStats, done, total int) {
			a.reportScanProgress(stats, done, total)
			if done == 1 {
				close(entered)
				<-release
			}
		})
	}()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
			<-finished
		}
	}()
	<-entered
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() { response <- request(a, "GET", "/api/scan", "", false) }()
	var progress struct {
		Active bool `json:"active"`
		scanProgress
	}
	select {
	case w := <-response:
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &progress) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("scan's file lock blocked progress reads")
	}
	// Directory iteration order differs across filesystems. Any one of the
	// three fixtures may be processed first, while the committed index stays whole.
	validBytes := progress.Stats.Bytes == first.Size || progress.Stats.Bytes == second.Size || progress.Stats.Bytes == int64(len(third))
	if !progress.Active || progress.Done != 1 || progress.Total != 3 || progress.Stats.Tracks != 1 || progress.Stats.Albums != 1 || progress.Stats.Artists != 1 || !validBytes || progress.Stats.Duration != first.Duration {
		t.Fatal("incorrect live totals", progress)
	}
	if jobs := a.taskSnapshot(); len(jobs) != 1 || jobs[0].Done != 1 || jobs[0].Total != 3 {
		t.Fatal("shared notification lacks scan progress", jobs)
	}
	if w := request(a, "POST", "/api/scan", "{}", true); w.Code != http.StatusConflict {
		t.Fatal("duplicate scan was queued", w.Code, w.Body.String())
	}
	var committed struct {
		Stats libraryStats `json:"stats"`
	}
	w := conditionalState(a, "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &committed) != nil || committed.Stats.Tracks != 2 {
		t.Fatal("partial scan leaked into the committed index", w.Body.String())
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	a.jobMu.Lock()
	a.jobs["scan-test"].State = "done"
	a.jobMu.Unlock()
	w = request(a, "GET", "/api/scan", "", false)
	if json.Unmarshal(w.Body.Bytes(), &progress) != nil || progress.Active {
		t.Fatal("completed scan left a running progress snapshot", w.Body.String())
	}
	w = conditionalState(a, "")
	if json.Unmarshal(w.Body.Bytes(), &committed) != nil || committed.Stats.Tracks != 3 || committed.Stats.Artists != 1 || committed.Stats.Albums != 1 {
		t.Fatal("final totals differ from the scan", w.Body.String())
	}
	for _, track := range a.tracks() {
		if track.Path == first.Path && (track.ID != first.ID || track.Added != first.Added) {
			t.Fatal("refresh changed existing track identity")
		}
	}
}

func TestScanFailureClearsLiveTotalsAndRetainsCommittedIndex(t *testing.T) {
	a := testApp(t)
	addTrack(t, a, "01.flac", "First", 1)
	if err := os.WriteFile(filepath.Join(a.music.Name(), "02.flac"), toneFLAC("Second", 2), 0600); err != nil {
		t.Fatal(err)
	}
	err := a.scanWithProgress(func(stats libraryStats, done, total int) {
		a.reportScanProgress(stats, done, total)
		if done == total && total > 0 {
			a.storageGuard = func() error { return errors.New("disk removed") }
		}
	})
	a.storageGuard = nil
	if err == nil {
		t.Fatal("scan ignored a removed disk before commit")
	}
	if tracks := a.tracks(); len(tracks) != 1 {
		t.Fatal("failed scan committed a partial index", len(tracks))
	}
	w := request(a, "GET", "/api/scan", "", false)
	var progress struct {
		Active bool          `json:"active"`
		Stats  *libraryStats `json:"stats"`
	}
	if json.Unmarshal(w.Body.Bytes(), &progress) != nil || progress.Active || progress.Stats != nil {
		t.Fatal("failed scan retained live counters", w.Body.String())
	}
}
