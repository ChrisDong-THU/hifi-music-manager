package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCancelledInspectionAndHashStopReading(t *testing.T) {
	a := testApp(t)
	track := addTrack(t, a, "cancel.flac", "Keep", 1)
	ctx, cancel := context.WithCancel(context.Background())
	f, err := a.music.Open(track.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	reader := operationReadSeeker{ReadSeeker: f, ctx: ctx}
	if _, err := reader.Read(make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	position, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := reader.Read(make([]byte, 4)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled metadata reader continued", err)
	}
	if _, err := reader.Seek(0, io.SeekStart); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled metadata reader sought", err)
	}
	if _, _, err := hashTrack(ctx, f, track.Size, 0, track.Size); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled audio digest continued", err)
	}
	if after, err := f.Seek(0, io.SeekCurrent); err != nil || after != position {
		t.Fatal("cancelled reads advanced the file", after, err)
	}
	if _, err := inspectContext(ctx, a.music, track.Path); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled inspection continued", err)
	}
}

func TestSlowStreamDoesNotHoldDiskControlLockAndCloseWaitsForItsLease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &app{ctx: ctx}
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c := &diskController{active: a, volumeID: "disk", cancel: cancel,
		security: &app{csrf: "token", cookieName: "session"}, status: "请选择数据盘",
		handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-r.Context().Done()
			close(canceled)
			<-release
		})}
	served, closed := make(chan struct{}), make(chan struct{})
	go func() {
		c.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/audio", nil))
		close(served)
	}()
	<-entered
	go func() { c.detach(); close(closed) }()
	defer func() { close(release); <-served; <-closed }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("disk detach cannot cancel a slow audio request")
	}
	select {
	case <-closed:
		t.Fatal("app closed while its request still held a resource lease")
	default:
	}
	responded := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		c.ServeHTTP(w, httptest.NewRequest("GET", "/api/state", nil))
		responded <- w.Code
	}()
	select {
	case code := <-responded:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("closing a streamed resource blocked the settings UI")
	}
}

func TestClosedAppRefusesNewRequestsAndMutationLeasesAreCounted(t *testing.T) {
	a := &app{}
	if !a.beginRequest(true) || a.mutations.Load() != 1 {
		t.Fatal("mutation not registered")
	}
	a.serveRequest(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/test", nil), http.NotFoundHandler(), true)
	if a.mutations.Load() != 0 {
		t.Fatal("mutation lease leaked")
	}
	a.close()
	if a.beginRequest(false) {
		t.Fatal("closed app accepted a new reader")
	}
}
