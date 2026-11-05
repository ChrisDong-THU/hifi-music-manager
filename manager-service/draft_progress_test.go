package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPreparationFailureSettlesIncompleteStages(t *testing.T) {
	t.Run("before grouping", func(t *testing.T) {
		a := testApp(t)
		b := addBatch(t, a, "Invalid owner", 341)
		b.UploadOwner = "invalid"
		b.ReadState, b.QualityState = "queued", "queued"
		if err := a.put("batches", b.ID, b); err != nil {
			t.Fatal(err)
		}
		if err := a.prepare(b.ID); err == nil {
			t.Fatal("invalid ownership accepted")
		}
		fresh, err := get[Batch](a, "batches", b.ID)
		if err != nil || fresh.ReadState != "failed" || fresh.QualityState != "failed" || fresh.ReadError == "" || fresh.QualityError == "" {
			t.Fatal("failed preparation left waiting stages", fresh, err)
		}
	})
	t.Run("grouped children", func(t *testing.T) {
		a := testApp(t)
		b := addBatch(t, a, "First", 342)
		missing := b.Files[0].Path
		addInboxFile(t, a, &b, "Second", "Other album", 343)
		if err := a.data.Remove(missing); err != nil {
			t.Fatal(err)
		}
		if err := a.prepare(b.ID); err == nil {
			t.Fatal("missing source accepted")
		}
		parent, _ := get[Batch](a, "batches", b.ID)
		if len(parent.Children) != 2 {
			t.Fatal("grouping was not exercised")
		}
		for _, id := range parent.Children {
			child, err := get[Batch](a, "batches", id)
			if err != nil || len(child.Files) != 1 {
				t.Fatal("source ownership lost", child, err)
			}
			if child.Files[0].Path == missing {
				if child.ReadState != "failed" || child.QualityState != "failed" || child.QualityError == "" {
					t.Fatal("blocked child still waiting", child)
				}
			} else if child.ReadState != "done" || child.QualityState != "done" || len(child.Quality) != 1 {
				t.Fatal("successful child was reset by another child's failure", child)
			}
		}
	})
}

func TestDraftMutationsRejectQueuedProcessingWithoutWaitingForFileLock(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Busy", 344)
	if _, err := a.submitDraftTask(b.ID, "quality"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	finished := make(chan error, 1)
	go func() {
		for _, route := range []string{"tags", "files?path=again.flac"} {
			w := request(a, "POST", "/api/drafts/"+b.ID+"/"+route, "{}", true)
			if w.Code != 409 || !strings.Contains(w.Body.String(), "正在处理") {
				finished <- fmt.Errorf("%s accepted a conflicting mutation: %d %s", route, w.Code, w.Body.String())
				return
			}
		}
		finished <- nil
	}()
	select {
	case err := <-finished:
		a.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		a.mu.Unlock()
		<-finished
		t.Fatal("conflicting request waited for the long-running worker")
	}
}

func TestDraftQueuePublishesWaitingStateWithoutWaitingForFileLock(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Queued", 331)
	if err := a.prepare(b.ID); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	type result struct {
		id  string
		err error
	}
	accepted := make(chan result, 1)
	go func() { id, err := a.submitDraftTask(b.ID, "quality"); accepted <- result{id, err} }()
	var submitted result
	select {
	case submitted = <-accepted:
		a.mu.Unlock()
	case <-time.After(time.Second):
		a.mu.Unlock()
		t.Fatal("queue request waited on the file worker")
	}
	if submitted.err != nil {
		t.Fatal(submitted.err)
	}
	fresh, _ := get[Batch](a, "batches", b.ID)
	if fresh.QualityState != "queued" || fresh.ReadState != "done" {
		t.Fatal("independent waiting state not published", fresh.QualityState, fresh.ReadState)
	}
	if _, err := a.submitDraftTask(b.ID, "quality"); err == nil {
		t.Fatal("duplicate task accepted")
	}
	job := a.taskSnapshot()
	found := false
	for _, j := range job {
		if j.ID == submitted.id && j.BatchID == b.ID && j.State == "queued" {
			found = true
		}
	}
	if !found {
		t.Fatal("queued task did not identify its draft")
	}
}

func TestReadingAndDetectionCanBeRepeatedIndependently(t *testing.T) {
	a := testApp(t)
	b := addBatch(t, a, "Independent", 332)
	if err := a.prepare(b.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := get[Batch](a, "batches", b.ID)
	if err := a.localPreview(b.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := get[Batch](a, "batches", b.ID)
	if after.Quality[b.Files[0].ID].CheckedAt != before.Quality[b.Files[0].ID].CheckedAt || after.QualityState != "done" || after.ReadState != "done" {
		t.Fatal("rereading tags changed the completed detection")
	}
	manual := originalCandidate(after)
	manual.ID, manual.Source = "manual", "manual"
	manual.Tags[0].Title = "Saved manual draft"
	after.Candidates, after.ChosenCandidate = []Candidate{manual}, manual.ID
	if err := a.put("batches", b.ID, after); err != nil {
		t.Fatal(err)
	}
	if err := a.analyzeDraftQuality(b.ID); err != nil {
		t.Fatal(err)
	}
	final, _ := get[Batch](a, "batches", b.ID)
	if final.Candidates[0].Tags[0].Title != "Saved manual draft" || final.ReadState != "done" {
		t.Fatal("detection reread draft tags")
	}
}
