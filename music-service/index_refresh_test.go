package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndexRefreshDoesNotBlockSupervisionAndIsCanceledOnDetach(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	r := indexRefresh{request: func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	}}
	returned := make(chan struct{})
	go func() { r.start(context.Background()); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("refresh blocked disk supervision")
	}
	<-entered
	r.stop()
	select {
	case <-exited:
	default:
		t.Fatal("old disk refresh was not canceled and joined")
	}
	r.stop()
}

func TestIndexRefreshRetriesUntilSuccessWithoutAnotherDiskEvent(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{})
	r := indexRefresh{request: func(context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("vendor is starting")
		}
		close(done)
		return nil
	}}
	r.start(context.Background())
	defer r.stop()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("refresh did not retry")
	}
	r.stop()
	if calls.Load() != 2 {
		t.Fatal("successful refresh was repeated")
	}
}

func TestOfflineStopsPlayerEvenWhenStatePublicationFails(t *testing.T) {
	w, drives, events := watcherFixture(t)
	*drives = []USBDrive{markedUSB(t, "disk")}
	if err := w.step(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.a.home, "logs")
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	canceled := false
	w.cancelIndex = func() { canceled = true }
	if err := w.offline(context.Background(), LibraryState{}); err == nil {
		t.Fatal("missing state publication error")
	}
	if !canceled || w.current != nil || (*events)[len(*events)-1] != "stop" {
		t.Fatal("state error left the player or refresh running")
	}
}
