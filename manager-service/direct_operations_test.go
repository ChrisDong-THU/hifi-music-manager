package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func latestOperation(t *testing.T, a *app) Operation {
	t.Helper()
	var latest Operation
	for _, op := range all[Operation](a, "operations") {
		if op.Sequence > latest.Sequence {
			latest = op
		}
	}
	if latest.ID == "" {
		t.Fatal("missing operation")
	}
	return latest
}

func assertNoCopies(t *testing.T, a *app) {
	t.Helper()
	if _, err := a.data.Stat("recovery"); !os.IsNotExist(err) {
		t.Fatal("recovery directory exists", err)
	}
	for _, root := range []*os.Root{a.music, a.data} {
		err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if strings.Contains(p, ".hifi-") || strings.Contains(p, ".upload-") || strings.Contains(p, ".pending-") {
				t.Errorf("temporary file retained: %s", p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirectEditsHaveNoInverseAPIOrCopies(t *testing.T) {
	a := testApp(t)
	track := addTrack(t, a, "one.flac", "Original", 201)
	for _, title := range []string{"A", "B"} {
		if err := a.edit([]string{track.ID}, map[string]string{"title": title}); err != nil {
			t.Fatal(err)
		}
	}
	current, _ := get[Track](a, "tracks", track.ID)
	if current.Tags.Title != "B" || current.AudioHash != track.AudioHash {
		t.Fatal("incorrect final file")
	}
	for _, op := range all[Operation](a, "operations") {
		for _, action := range []string{"undo", "retain"} {
			w := request(a, "POST", "/api/operations/"+op.ID+"/"+action, "{}", true)
			if w.Code < 400 {
				t.Fatal("inverse endpoint still available", w.Code)
			}
		}
	}
	state := request(a, "GET", "/api/state", "", false)
	for _, private := range []string{`"backup"`, `"changes"`, `"resources"`, `"undo"`, `"expires_at"`} {
		if strings.Contains(state.Body.String(), private) {
			t.Fatal("state exposes recovery metadata", private)
		}
	}
	assertNoCopies(t, a)
}

func TestPartialEditKeepsPublishedResultsAndAccurateIndex(t *testing.T) {
	a := testApp(t)
	first := addTrack(t, a, "one.flac", "One", 201)
	second := addTrack(t, a, "two.flac", "Two", 202)
	injected := false
	a.storageGuard = func() error {
		track, _ := get[Track](a, "tracks", first.ID)
		if !injected && track.Tags.Title == "Changed" {
			injected = true
			return errors.New("injected disk error")
		}
		return nil
	}
	if err := a.edit([]string{first.ID, second.ID}, map[string]string{"title": "Changed"}); err == nil {
		t.Fatal("fault did not fail")
	}
	a.storageGuard = nil
	left, _ := get[Track](a, "tracks", first.ID)
	right, _ := get[Track](a, "tracks", second.ID)
	if left.Tags.Title != "Changed" || right.Tags.Title != "Two" {
		t.Fatal("partial result not truthful")
	}
	if op := latestOperation(t, a); op.State != "failed" || op.Affected != 1 {
		t.Fatal("incorrect audit", op)
	}
	assertNoCopies(t, a)
}

func TestImportConsumesSourcesAndRetriesOnlyRemainingFiles(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "First", 211)
	addInboxFile(t, a, &b, "Second", "", 212)
	first := b.Files[0]
	injected := false
	a.storageGuard = func() error {
		if !injected && len(a.tracks()) == 1 {
			injected = true
			return errors.New("injected cleanup error")
		}
		return nil
	}
	if err := a.importWithPolicy(b.ID, "keep", fileIDs(b.Files)); err == nil {
		t.Fatal("fault did not fail")
	}
	a.storageGuard = nil
	pending, _ := get[Batch](a, "batches", b.ID)
	if pending.State != "ready" || len(pending.Files) != 1 || len(pending.Imported) != 1 || len(pending.Cleanup) != 1 {
		t.Fatalf("wrong remaining work: %+v", pending)
	}
	if len(a.tracks()) != 1 {
		t.Fatal("committed import lost")
	}
	if err := a.cleanupFiles(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.data.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatal("source cleanup did not retry")
	}
	if err := a.importWithPolicy(b.ID, "keep", fileIDs(pending.Files)); err != nil {
		t.Fatal(err)
	}
	if len(a.tracks()) != 2 {
		t.Fatal("retry duplicated completed music")
	}
	for _, source := range b.Files {
		if _, err := a.data.Stat(source.Path); !os.IsNotExist(err) {
			t.Fatal("import source retained")
		}
	}
	if _, err := get[Batch](a, "batches", b.ID); !os.IsNotExist(err) {
		t.Fatal("completed import retained a draft record", err)
	}
	w := request(a, "GET", "/api/drafts", "", false)
	var state struct {
		Batches []draftView `json:"drafts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Batches) != 0 {
		t.Fatal("completed import is still exposed as a draft")
	}
	if len(a.tracks()) != 2 {
		t.Fatal("draft cleanup deleted music")
	}
	assertNoCopies(t, a)
}

func TestCleanupRemovesExistingImportedRecordsAndPreservesLiveDrafts(t *testing.T) {
	a := testApp(t)
	track := addTrack(t, a, "existing.flac", "Existing", 213)
	live := addBatch(t, a, "Live draft", 214)
	completed := Batch{ID: uid(), State: "imported", Imported: []string{track.ID, "missing-track"}}
	if err := a.put("batches", completed.ID, completed); err != nil {
		t.Fatal(err)
	}
	if err := a.cleanupFiles(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := get[Batch](a, "batches", completed.ID); !os.IsNotExist(err) {
		t.Fatal("existing imported record was retained", err)
	}
	if _, err := get[Batch](a, "batches", live.ID); err != nil {
		t.Fatal("live draft was removed", err)
	}
	if err := regular(a.data, live.Files[0].Path); err != nil {
		t.Fatal("live upload was removed", err)
	}
	if err := regular(a.music, track.Path); err != nil || len(a.tracks()) != 1 {
		t.Fatal("imported music was changed", err)
	}
}

func TestCompletedImportCleanupSurvivesRestartWithoutRetainingADraft(t *testing.T) {
	home := t.TempDir()
	a, err := newApp(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.close() })
	b := addBatch(t, a, "Import", 215)
	a.storageGuard = func() error {
		if len(a.tracks()) == 1 {
			return errors.New("injected cleanup error")
		}
		return nil
	}
	if err := a.importWithPolicy(b.ID, "keep", fileIDs(b.Files)); err == nil {
		t.Fatal("cleanup fault did not fail")
	}
	a.storageGuard = nil
	pending, err := get[Batch](a, "batches", b.ID)
	if err != nil || pending.State != "imported" || len(pending.Files) != 0 || len(pending.Cleanup) != 1 {
		t.Fatalf("incorrect cleanup retry record: %+v %v", pending, err)
	}
	w := request(a, "GET", "/api/drafts", "", false)
	var state struct {
		Batches []draftView `json:"drafts"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || len(state.Batches) != 0 {
		t.Fatal("completed import is visible while cleanup is pending", w.Body.String())
	}
	a.close()
	a, err = newApp(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.cleanupFiles(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := get[Batch](a, "batches", b.ID); !os.IsNotExist(err) {
		t.Fatal("completed import record retained after cleanup", err)
	}
	if _, err := a.data.Stat(b.Files[0].Path); !os.IsNotExist(err) {
		t.Fatal("source cleanup did not resume", err)
	}
	if len(a.tracks()) != 1 {
		t.Fatal("cleanup changed published music")
	}
}

func TestDeletionCleanupSurvivesRestart(t *testing.T) {
	home := t.TempDir()
	a, err := newApp(home)
	if err != nil {
		t.Fatal(err)
	}
	b := addBatch(t, a, "Delete", 220)
	b.Cleanup = []string{b.Files[0].Path}
	b.Files = nil
	b.State = "deleted"
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	op, err := a.begin("draft-delete", "Delete")
	if err != nil {
		t.Fatal(err)
	}
	a.close()
	a, err = newApp(home)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	stored, _ := get[Operation](a, "operations", op.ID)
	if stored.State != "failed" || stored.Error == "" {
		t.Fatal("interrupted work reported success")
	}
	if err := a.cleanupFiles(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := get[Batch](a, "batches", b.ID); err == nil {
		t.Fatal("deleted batch retained")
	}
	if _, err := a.data.Stat(b.Cleanup[0]); !os.IsNotExist(err) {
		t.Fatal("deletion did not resume")
	}
}

func TestGroupedDeletionOnlyRemovesItsOwnFiles(t *testing.T) {
	a := testApp(t)
	parent := addBatch(t, a, "First", 230)
	addInboxFile(t, a, &parent, "Second", "Second album", 231)
	ids, err := a.splitAlbums(parent.ID)
	if err != nil || len(ids) != 2 {
		t.Fatal(ids, err)
	}
	first, _ := get[Batch](a, "batches", ids[0])
	second, _ := get[Batch](a, "batches", ids[1])
	if w := request(a, "POST", "/api/drafts/"+first.ID+"/archive", "{}", true); w.Code != 404 {
		t.Fatal("obsolete archive endpoint is still available", w.Code)
	}
	if err := a.deleteDraft(first.ID, fileIDs(first.Files)); err != nil {
		t.Fatal(err)
	}
	for _, file := range first.Files {
		if _, err := a.data.Stat(file.Path); !os.IsNotExist(err) {
			t.Fatal("deleted file exists")
		}
	}
	for _, file := range second.Files {
		if err := regular(a.data, file.Path); err != nil {
			t.Fatal("sibling file deleted", err)
		}
	}
	if err := a.importWithPolicy(second.ID, "keep", fileIDs(second.Files)); err != nil {
		t.Fatal(err)
	}
	if err := a.cleanupFiles(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if batches := all[Batch](a, "batches"); len(batches) != 0 {
		t.Fatal("group container retained", batches)
	}
	if len(a.tracks()) != 1 {
		t.Fatal("unrelated music changed")
	}
	assertNoCopies(t, a)
}

func TestCleanupKeepsLiveDraftsAndOnlyRemovesRecognizedArtifacts(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Live", 240)
	old := time.Now().Add(-365 * 24 * time.Hour)
	if err := a.data.Chtimes(b.Files[0].Path, old, old); err != nil {
		t.Fatal(err)
	}
	orphan := "uploads/" + uid() + "/orphan.flac"
	if err := copyFile(a.data, b.Files[0].Path, a.data, orphan); err != nil {
		t.Fatal(err)
	}
	if err := ensureDir(a.data, "uploads/"+uid()); err != nil {
		t.Fatal(err)
	}
	tmp := ".hifi-edit-" + uid() + ".flac"
	f, _ := a.music.Create(tmp)
	_ = f.Close()
	f, _ = a.music.Create("keep.txt")
	_ = f.Close()
	if err := a.cleanupFiles(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := regular(a.data, b.Files[0].Path); err != nil {
		t.Fatal("live draft aged out", err)
	}
	if _, err := a.data.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan retained")
	}
	if _, err := a.music.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("temporary file retained")
	}
	if err := regular(a.music, "keep.txt"); err != nil {
		t.Fatal("unknown file deleted")
	}
}

func TestAuditHistoryIsBoundedAndMonotonic(t *testing.T) {
	a := testApp(t)
	for i := 0; i < 150; i++ {
		op, err := a.begin("edit", fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		// An older unfinished summary must not bypass the storage limit.
		if i > 0 {
			if err := a.finish(&op); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := len(all[Operation](a, "operations")); got != 99 {
		t.Fatal("unbounded history", got)
	}
	if op := latestOperation(t, a); op.Sequence != 150 {
		t.Fatal("sequence reset", op.Sequence)
	}
	response := request(a, "GET", "/api/state", "", false)
	var state struct {
		Operations []Operation `json:"operations"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(state.Operations) != 99 {
		t.Fatal("incorrect visible history limit", response.Code, len(state.Operations))
	}
	if state.Operations[0].Sequence != 150 || state.Operations[98].Sequence != 52 {
		t.Fatal("oldest summaries were not evicted")
	}
}

func TestCleanupRejectsJunctionsAndForgedPaths(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Safe", 250)
	b.Cleanup = []string{"../outside.flac"}
	if err := a.cleanupBatchLocked(&b); err == nil {
		t.Fatal("escaped cleanup root")
	}
	outside := t.TempDir()
	victim := filepath.Join(outside, "keep.flac")
	if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(a.data.Name(), "uploads", b.ID, "linked")
	var err error
	if runtime.GOOS == "windows" {
		err = exec.Command("cmd", "/c", "mklink", "/J", link, outside).Run()
	} else {
		err = os.Symlink(outside, link)
	}
	if err != nil {
		t.Skip("link creation unavailable", err)
	}
	defer os.Remove(link)
	b.Cleanup = []string{"uploads/" + b.ID + "/linked/keep.flac"}
	if err := a.cleanupBatchLocked(&b); err == nil {
		t.Fatal("cleanup traversed link")
	}
	_ = a.cleanupFiles(context.Background(), true)
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" {
		t.Fatal("outside data changed")
	}
}
