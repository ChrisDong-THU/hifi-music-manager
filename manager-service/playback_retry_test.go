package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRepeatedPlaybackFailureDoesNotInvalidateLibraryPolls(t *testing.T) {
	a := testApp(t)
	a.rescan = func(context.Context) error { return errors.New("offline") }
	if err := a.markPlaybackDirty(); err != nil {
		t.Fatal(err)
	}
	if err := a.syncPlayback(context.Background()); err == nil {
		t.Fatal("expected delivery failure")
	}
	first := conditionalState(a, "")
	if first.Code != 200 {
		t.Fatal(first.Code)
	}
	tag := first.Header().Get("ETag")
	for range 3 {
		if err := a.syncPlayback(context.Background()); err == nil {
			t.Fatal("retry must still report delivery failure")
		}
		if response := conditionalState(a, tag); response.Code != 304 {
			t.Fatal("unchanged retry invalidated the library snapshot", response.Code)
		}
	}
	// A newer resource commit must remain pending and recovery must acknowledge it.
	if err := a.markPlaybackDirty(); err != nil {
		t.Fatal(err)
	}
	a.rescan = func(context.Context) error { return nil }
	if err := a.syncPlayback(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := a.playbackSyncState()
	if state.Requested != 2 || state.Delivered != 2 || state.Error != "" {
		t.Fatalf("recovery lost a resource commit: %+v", state)
	}
	if response := conditionalState(a, tag); response.Code != 200 {
		t.Fatal("delivery recovery did not invalidate the response")
	}
}

func TestTaskSnapshotDoesNotLockQueueDuringExternalReads(t *testing.T) {
	a := testApp(t)
	reading, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a.externalTasks = func() []task {
		close(reading)
		<-release
		return nil
	}
	go func() { defer close(finished); a.taskSnapshot() }()
	<-reading
	queued := make(chan error, 1)
	go func() { _, err := a.submit("edit", func() error { return nil }); queued <- err }()
	select {
	case err := <-queued:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("external snapshot blocked unrelated task submission")
	}
	close(release)
	<-finished
}
