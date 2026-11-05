package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestDraftQualityReadsFLACAndPreservesSourcesAndSavedTags(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Analyze", 321)
	before, err := os.ReadFile(a.data.Name() + "/" + b.Files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.analyzeDraftQuality(b.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := get[Batch](a, "batches", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := fresh.Quality[b.Files[0].ID]
	if !ok || r.Status != "analyzed" || r.DeclaredBits != 16 || r.SampleRate != 44100 || r.SourceHash != b.Files[0].Hash {
		t.Fatalf("invalid analysis: %+v", r)
	}
	if fresh.Candidates[0].Tags[0] != b.Candidates[0].Tags[0] || fresh.Files[0].Hash != b.Files[0].Hash {
		t.Fatal("analysis changed the draft")
	}
	after, _ := os.ReadFile(a.data.Name() + "/" + b.Files[0].Path)
	if string(before) != string(after) {
		t.Fatal("source was modified")
	}
	w := request(a, "GET", "/api/drafts", "", false)
	var state struct {
		Batches []draftView `json:"drafts"`
	}
	if json.Unmarshal(w.Body.Bytes(), &state) != nil || len(state.Batches) != 1 || len(state.Batches[0].Quality) != 1 {
		t.Fatal(w.Body.String())
	}
	if w := request(a, "POST", "/api/drafts/"+b.ID+"/quality", "{}", false); w.Code != 403 {
		t.Fatal("missing session accepted", w.Code)
	}
	if w := request(a, "POST", "/api/drafts/"+b.ID+"/quality", "{}", true); w.Code != 202 {
		t.Fatal("quality task not accepted", w.Code, w.Body.String())
	}
}

func TestQualityResultsAreInvalidatedBySourceChangesAndFollowGrouping(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "First", 322)
	addInboxFile(t, a, &b, "Second", "Other album", 323)
	if err := a.analyzeDraftQuality(b.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := a.splitAlbums(b.ID)
	if err != nil || len(ids) != 2 {
		t.Fatal(ids, err)
	}
	for _, id := range ids {
		child, err := get[Batch](a, "batches", id)
		if err != nil || len(child.Quality) != len(child.Files) {
			t.Fatal("grouping lost results", err)
		}
	}
	child, _ := get[Batch](a, "batches", ids[0])
	file := child.Files[0]
	f, err := a.data.OpenFile(file.Path, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(toneFLAC("Changed", 324))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.analyzeDraftQuality(child.ID); err == nil {
		t.Fatal("changed upload accepted")
	}
	fresh, _ := get[Batch](a, "batches", child.ID)
	if fresh.Quality[file.ID].Status != "error" {
		t.Fatal("stale result still marked as measured")
	}
	if err := a.localPreview(child.ID); err != nil {
		t.Fatal(err)
	}
	fresh, _ = get[Batch](a, "batches", child.ID)
	if len(fresh.Quality) != 0 {
		t.Fatal("reread kept stale measurement")
	}
}

func TestCancelledQualityDoesNotPublishResults(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Cancel", 325)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.ctx = ctx
	if err := a.analyzeDraftQuality(b.ID); err == nil {
		t.Fatal("cancellation ignored")
	}
	fresh, _ := get[Batch](a, "batches", b.ID)
	if len(fresh.Quality) != 0 {
		t.Fatal("cancelled result committed")
	}
}

func TestPrepareAutomaticallyReadsThenAnalyzesEveryAlbum(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "First", 326)
	addInboxFile(t, a, &b, "Second", "Other album", 327)
	if err := a.prepare(b.ID); err != nil {
		t.Fatal(err)
	}
	parent, err := get[Batch](a, "batches", b.ID)
	if err != nil || len(parent.Children) != 2 {
		t.Fatal("albums were not split", err)
	}
	for _, id := range parent.Children {
		child, err := get[Batch](a, "batches", id)
		if err != nil || !child.Previewed || child.QualityState != "done" || len(child.Quality) != len(child.Files) {
			t.Fatalf("automatic preparation incomplete: %+v %v", child, err)
		}
		for _, file := range child.Files {
			result := child.Quality[file.ID]
			if result.Status != "analyzed" || result.SourceHash != file.Hash {
				t.Fatal("measurement does not match the freshly read file", result)
			}
		}
	}
}
