package main

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func fileIDs(files []Track) []string {
	ids := make([]string, len(files))
	for i, file := range files {
		ids[i] = file.ID
	}
	return ids
}

func TestImportSelectedSongsPreservesRemainingTagsQualityAndCover(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "First", 601)
	addInboxFile(t, a, &b, "Second", "", 602)
	addInboxFile(t, a, &b, "Third", "", 603)
	manual := originalCandidate(b)
	manual.ID, manual.Source = "manual", "manual"
	for i := range manual.Tags {
		manual.Tags[i].Title = "Saved " + b.Files[i].Tags.Title
	}
	b.Candidates = append(b.Candidates, manual)
	b.ChosenCandidate = manual.ID
	cover := testAlbumCover(t, false)
	if err := a.putDraft(b, cover, false); err != nil {
		t.Fatal(err)
	}
	if err := a.analyzeDraftQuality(b.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := get[Batch](a, "batches", b.ID)
	// Import non-adjacent positions with saved tags. The middle file stays put.
	if err := a.importWithPolicy(b.ID, "keep", []string{b.Files[0].ID, b.Files[2].ID}); err != nil {
		t.Fatal(err)
	}
	after, err := get[Batch](a, "batches", b.ID)
	if err != nil || after.State != "ready" || len(after.Files) != 1 || after.Files[0] != b.Files[1] {
		t.Fatalf("wrong remaining draft: %+v %v", after, err)
	}
	if len(after.Quality) != 1 || !reflect.DeepEqual(after.Quality[b.Files[1].ID], before.Quality[b.Files[1].ID]) || after.CoverHash != before.CoverHash {
		t.Fatal("remaining analysis or artwork changed")
	}
	for _, c := range after.Candidates {
		if len(c.Tags) != 1 || (c.ID == "manual" && c.Tags[0] != manual.Tags[1]) {
			t.Fatal("remaining saved tags lost alignment", c)
		}
	}
	if hash, err := fileHash(a.data, b.Files[1].Path); err != nil || hash != b.Files[1].Hash {
		t.Fatal("unselected source changed", err)
	}
	for _, track := range a.tracks() {
		checkTrackCover(t, a, track, cover)
		if track.Tags.Title != manual.Tags[0].Title && track.Tags.Title != manual.Tags[2].Title {
			t.Fatal("wrong tags imported", track.Tags)
		}
	}
	if len(a.tracks()) != 2 || latestOperation(t, a).Affected != 2 {
		t.Fatal("wrong imported count")
	}
	if err := a.importWithPolicy(b.ID, "keep", fileIDs(after.Files)); err != nil {
		t.Fatal(err)
	}
	if len(a.tracks()) != 3 {
		t.Fatal("remaining import failed")
	}
	if _, err := get[Batch](a, "batches", b.ID); !os.IsNotExist(err) {
		t.Fatal("fully imported draft retained", err)
	}
}

func TestDeleteSelectedSongPreservesOtherSourcesAndMetadata(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "First", 611)
	addInboxFile(t, a, &b, "Second", "", 612)
	addInboxFile(t, a, &b, "Third", "", 613)
	cover := testAlbumCover(t, false)
	if err := a.putDraft(b, cover, false); err != nil {
		t.Fatal(err)
	}
	if err := a.analyzeDraftQuality(b.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := get[Batch](a, "batches", b.ID)
	if err := a.deleteDraft(b.ID, []string{b.Files[1].ID}); err != nil {
		t.Fatal(err)
	}
	after, err := get[Batch](a, "batches", b.ID)
	if err != nil || after.State != "ready" || len(after.Files) != 2 || after.CoverHash != before.CoverHash || len(after.Quality) != 2 {
		t.Fatalf("remaining draft lost: %+v %v", after, err)
	}
	if after.Candidates[0].Tags[0] != b.Files[0].Tags || after.Candidates[0].Tags[1] != b.Files[2].Tags {
		t.Fatal("tags lost alignment")
	}
	if err := a.cleanupFiles(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	for _, file := range after.Files {
		if hash, err := fileHash(a.data, file.Path); err != nil || hash != file.Hash {
			t.Fatal("unselected file changed", err)
		}
	}
	if _, err := a.data.Stat(b.Files[1].Path); !os.IsNotExist(err) {
		t.Fatal("selected source retained", err)
	}
	if latestOperation(t, a).Affected != 1 || len(a.tracks()) != 0 {
		t.Fatal("delete affected unrelated music")
	}
	if err := a.deleteDraft(b.ID, fileIDs(after.Files)); err != nil {
		t.Fatal(err)
	}
	if _, err := get[Batch](a, "batches", b.ID); !os.IsNotExist(err) {
		t.Fatal("empty draft retained", err)
	}
}

func TestDraftSelectionRejectsInvalidIDsBeforeAnyMutation(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Local", 621)
	other := addBatch(t, a, "Other", 622)
	for _, ids := range [][]string{nil, {other.Files[0].ID}, {b.Files[0].ID, b.Files[0].ID}, {b.Files[0].ID, "missing"}} {
		if err := a.importWithPolicy(b.ID, "keep", ids); err == nil {
			t.Fatal("invalid import accepted", ids)
		}
		if err := a.deleteDraft(b.ID, ids); err == nil {
			t.Fatal("invalid deletion accepted", ids)
		}
	}
	current, _ := get[Batch](a, "batches", b.ID)
	if !reflect.DeepEqual(current, b) || len(a.tracks()) != 0 || len(all[Operation](a, "operations")) != 0 {
		t.Fatal("invalid selection changed data")
	}
	for _, route := range []string{"import", "delete"} {
		for _, body := range []string{`{}`, `{"ids":[]}`, `{"ids":["same","same"]}`} {
			if w := request(a, "POST", "/api/drafts/"+b.ID+"/"+route, body, true); w.Code != 400 {
				t.Fatal("invalid API selection accepted", route, w.Code)
			}
		}
	}
}

func TestImportSelectionIgnoresUnselectedDuplicatesAndAPISubmitsIDs(t *testing.T) {
	a := testApp(t)
	addTrack(t, a, "existing.flac", "Existing", 631)
	b := addBatch(t, a, "Duplicate", 631)
	addInboxFile(t, a, &b, "New", "", 632)
	body, _ := json.Marshal(map[string]any{"duplicate_policy": "reject", "ids": []string{b.Files[1].ID}})
	w := request(a, "POST", "/api/drafts/"+b.ID+"/import", string(body), true)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Execute the queued request without starting the unrelated startup scan.
	work := <-a.queue
	if err := work.fn(); err != nil {
		t.Fatal(err)
	}
	remaining, _ := get[Batch](a, "batches", b.ID)
	if len(remaining.Files) != 1 || remaining.Files[0].ID != b.Files[0].ID || len(a.tracks()) != 2 {
		t.Fatal("API ignored selected IDs")
	}
}

func TestDraftDuplicatesArePerFileAndRecomputedAfterLibraryChanges(t *testing.T) {
	b := Batch{ID: "draft", Files: []Track{{ID: "a", Hash: "file-a", AudioHash: "audio"}, {ID: "b", Hash: "file-b", AudioHash: "audio"}, {ID: "unknown"}}}
	result := projectDraft(b, contentIndex{"file-a": 0, "audio": 0}, false)
	if !reflect.DeepEqual(result.Duplicates, []string{"a", "b"}) {
		t.Fatal("incorrect duplicate files", result.Duplicates)
	}
	result = projectDraft(b, nil, true)
	if !reflect.DeepEqual(result.Duplicates, []string{"b"}) {
		t.Fatal("stale duplicates retained", result.Duplicates)
	}
}
