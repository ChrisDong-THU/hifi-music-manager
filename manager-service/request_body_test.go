package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestStalledBodyReleasesMutationLockAndHealthyRequestCanFollow(t *testing.T) {
	var lock sync.Mutex
	entered, released := make(chan struct{}), make(chan struct{})
	s := httptest.NewServer(requestBodyTimeout(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()
		if r.URL.Path == "/stalled" {
			close(entered)
			_, err := io.Copy(io.Discard, r.Body)
			if err == nil {
				t.Error("incomplete body was accepted")
			}
			close(released)
			return
		}
		if _, err := io.Copy(w, r.Body); err != nil {
			t.Error(err)
		}
	}), 100*time.Millisecond))
	defer s.Close()
	conn, err := net.Dial("tcp", s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = fmt.Fprint(conn, "POST /stalled HTTP/1.1\r\nHost: localhost\r\nContent-Length: 20\r\n\r\nx")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("stalled body kept the mutation lock")
	}
	// A complete body on a persistent connection must still work after a previous
	// response's deadline has elapsed; no request inherits another's timeout.
	healthy, err := net.Dial("tcp", s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer healthy.Close()
	reader := bufio.NewReader(healthy)
	for i := range 2 {
		if i == 1 {
			time.Sleep(150 * time.Millisecond)
		}
		_ = healthy.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = fmt.Fprint(healthy, "POST /healthy HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\n\r\nok")
		resp, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || string(body) != "ok" {
			t.Fatalf("healthy body: %q %v", body, err)
		}
	}
}
