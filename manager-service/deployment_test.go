package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerPublicEndpointRetainsAuthentication(t *testing.T) {
	const domain = "manager.example.net"
	auth, err := openManagerAuth(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer auth.db.Close()
	if err := auth.registerAccount(managerCredentials{Username: "owner", Password: "A fixture password", InviteCode: "fixture"}); err != nil {
		t.Fatal(err)
	}
	gate := &app{lan: netip.MustParsePrefix("192.168.1.0/24"), csrf: "csrf-token", cookieName: "hifi_session_test"}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	health := func() managerHealth { return managerHealth{} }
	public := gate.protectManager(next, auth, health, domain, "")
	request := func(handler http.Handler, scheme, host, remote, method, path, origin string, authCookie *http.Cookie, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, scheme+"://"+host+path, strings.NewReader(`{"username":"owner","password":"A fixture password"}`))
		r.RemoteAddr = remote
		if scheme == "https" {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-Host", domain)
		r.Header.Set("X-Forwarded-Proto", "https")
		if authCookie != nil {
			r.AddCookie(authCookie)
		}
		if csrf {
			r.AddCookie(&http.Cookie{Name: gate.cookieName, Value: gate.csrf})
			r.Header.Set("X-CSRF-Token", gate.csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	const loopback = "127.0.0.1:1234"
	for _, test := range []struct {
		scheme, host, remote, method, path, origin string
		want                                       int
	}{
		{"https", domain, loopback, "GET", "/api/state", "", 401},
		{"https", domain, loopback, "GET", "/healthz", "", 404},
		{"https", "127.0.0.1:18787", loopback, "GET", "/", "", 403},
		{"https", domain, "192.168.1.4:1234", "GET", "/", "", 403},
		{"http", domain, loopback, "POST", "/api/auth/login", "", 403},
		{"https", domain, loopback, "POST", "/api/auth/login", "https://sibling.example.net", 403},
	} {
		if w := request(public, test.scheme, test.host, test.remote, test.method, test.path, test.origin, nil, false); w.Code != test.want {
			t.Fatalf("%+v: got %d", test, w.Code)
		}
	}
	if w := request(gate.protectManager(next, auth, health, "", ""), "https", domain, loopback, "GET", "/", "", nil, false); w.Code != 403 {
		t.Fatal("LAN listener accepted the public host")
	}
	lan := gate.protectManager(next, auth, health, "", "nano.local")
	for _, test := range []struct {
		scheme, host, remote, origin string
		want                         int
	}{
		{"https", "nano.local:8787", "192.168.1.4:1234", "https://nano.local:8787", 200},
		{"http", "nano.local:8787", "192.168.1.4:1234", "", 403},
		{"https", "other.local:8787", "192.168.1.4:1234", "", 403},
		{"https", "nano.local:8787", "192.168.2.4:1234", "", 403},
		{"https", "nano.local:8787", "192.168.1.4:1234", "https://other.local:8787", 403},
	} {
		if w := request(lan, test.scheme, test.host, test.remote, "POST", "/api/auth/login", test.origin, nil, false); w.Code != test.want {
			t.Fatalf("LAN %+v: got %d", test, w.Code)
		}
	}
	login := request(public, "https", domain+":443", loopback, "POST", "/api/auth/login", "https://"+domain, nil, false)
	cookies := login.Result().Cookies()
	if login.Code != 200 || len(cookies) != 1 || !cookies[0].Secure || cookies[0].Domain != "" {
		t.Fatalf("public login did not issue a secure host-only session: %d", login.Code)
	}
	for _, csrf := range []bool{false, true} {
		w := request(public, "https", domain, loopback, "POST", "/edit", "https://"+domain, cookies[0], csrf)
		if (!csrf && w.Code != 403) || (csrf && w.Code != 204) {
			t.Fatalf("public write with CSRF token %t returned %d", csrf, w.Code)
		}
	}
}

func TestLANAccessIsExplicitAndRetainsOriginAndSessionGuards(t *testing.T) {
	for _, network := range []string{"0.0.0.0/0", "8.8.8.0/24", "10.0.0.0/7", "192.168.0.0/15", "bad"} {
		if _, err := accessNetwork("192.168.1.2:8787", network); err == nil {
			t.Fatalf("unsafe network accepted: %s", network)
		}
	}
	for _, listen := range []string{"0.0.0.0:8787", "192.168.2.2:8787", "8.8.8.8:8787"} {
		if _, err := accessNetwork(listen, "192.168.1.0/24"); err == nil {
			t.Fatalf("unsafe listener: %s", listen)
		}
	}
	if _, err := accessNetwork("192.168.1.2:8787", ""); err == nil {
		t.Fatal("LAN was enabled implicitly")
	}
	if _, err := accessNetwork("127.0.0.1:8787", ""); err != nil {
		t.Fatal(err)
	}
	a := testApp(t)
	a.lan = netip.MustParsePrefix("192.168.1.0/24")
	for _, test := range []struct {
		remote, host, origin string
		token                bool
		want                 int
	}{
		{"192.168.1.4:12", "192.168.1.2:8787", "http://192.168.1.2:8787", true, 202},
		{"192.168.2.4:12", "192.168.1.2:8787", "", true, 403},
		{"192.168.1.4:12", "evil.test:8787", "", true, 403},
		{"192.168.1.4:12", "192.168.1.2:8787", "http://evil.test", true, 403},
		{"192.168.1.4:12", "192.168.1.2:8787", "", false, 403},
	} {
		r := httptest.NewRequest("POST", "http://"+test.host+"/api/scan", nil)
		r.RemoteAddr = test.remote
		r.Header.Set("Origin", test.origin)
		if test.token {
			r.Header.Set("X-CSRF-Token", a.csrf)
			r.Header.Set(librarySessionHeader, a.librarySession)
			r.AddCookie(&http.Cookie{Name: a.cookieName, Value: a.csrf})
		}
		w := httptest.NewRecorder()
		a.protect(a.routes(http.NotFoundHandler())).ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("%+v: got %d", test, w.Code)
		}
	}
}

func awaitTask(t *testing.T, a *app, id string) task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, job := range a.taskSnapshot() {
			if job.ID == id && (job.State == "done" || job.State == "failed") {
				return job
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("task did not finish")
	return task{}
}

func awaitPlaybackSync(t *testing.T, a *app) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state := a.playbackSyncState()
		if state.Requested > 0 && state.Requested == state.Delivered {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("playback notification not delivered")
}

func TestExternalStorageAndPlaybackRescanFollowCommittedImport(t *testing.T) {
	home, volume := t.TempDir(), t.TempDir()
	music, data := filepath.Join(volume, "music"), filepath.Join(volume, "manager")
	var mounted atomic.Bool
	mounted.Store(true)
	guard := func() error {
		if !mounted.Load() {
			return errors.New("music offline")
		}
		return nil
	}
	if err := prepareVolumeDirectories(volume, music, data, guard); err != nil {
		t.Fatal(err)
	}
	a, err := newAppStorage(home, music, data, guard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.close)
	w := request(a, "POST", "/api/drafts", `{"name":"USB import"}`, true)
	var batch Batch
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &batch) != nil {
		t.Fatal(w.Body.String())
	}
	w = request(a, "POST", "/api/drafts/"+batch.ID+"/files?path=one.flac", string(toneFLAC("USB", 9)), true)
	var uploaded Track
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &uploaded) != nil {
		t.Fatal(w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(data, "uploads", batch.ID, "one.flac")); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || string(body) != "rescan" {
			t.Error("invalid rescan request")
		}
		count := calls.Add(1)
		tracks, err := a.readTracks()
		if err != nil {
			t.Error(err)
		}
		if count == 2 && len(tracks) != 1 {
			t.Error("MinimServer notified before the import committed")
		}
		w.WriteHeader(200)
	}))
	defer backend.Close()
	a.rescan, err = libraryRescan(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.start(ctx)
	var initial string
	for _, job := range a.taskSnapshot() {
		if job.Kind == "scan" {
			initial = job.ID
		}
	}
	if awaitTask(t, a, initial).State != "done" {
		t.Fatal("startup scan failed")
	}
	awaitPlaybackSync(t, a)
	id, err := a.submit("import", func() error { return a.importWithPolicy(batch.ID, "reject", []string{uploaded.ID}) })
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTask(t, a, id); job.State != "done" {
		t.Fatal(job.Message)
	}
	awaitPlaybackSync(t, a)
	if calls.Load() != 2 {
		t.Fatal("import did not refresh the playback library")
	}
	id, err = a.submit("preview", func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	awaitTask(t, a, id)
	if calls.Load() != 2 {
		t.Fatal("draft-only work triggered a playback rescan")
	}
	state := request(a, "GET", "/api/state", "", false)
	if !strings.Contains(state.Body.String(), filepath.ToSlash(music)) && !strings.Contains(state.Body.String(), strings.ReplaceAll(music, `\`, `\\`)) {
		t.Fatal("external music path not reported")
	}
	mounted.Store(false)
	if w := request(a, "POST", "/api/drafts", `{"name":"must not write"}`, true); w.Code != 503 {
		t.Fatalf("offline API accepted: %d", w.Code)
	}
	var wrote atomic.Bool
	id, err = a.submit("edit", func() error { wrote.Store(true); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if awaitTask(t, a, id).State != "failed" || wrote.Load() {
		t.Fatal("queued work wrote after the USB went offline")
	}
	if calls.Load() != 2 {
		t.Fatal("offline work triggered a rescan")
	}
}

func TestRescanRejectsNonLoopbackAndRedirects(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1/", "http://192.168.1.2/", "http://user:pass@127.0.0.1/", "http://127.0.0.1/#fragment", "not a URL"} {
		if _, err := libraryRescan(endpoint); err == nil {
			t.Fatalf("unsafe rescan address: %s", endpoint)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://example.com", 302) }))
	defer server.Close()
	rescan, err := libraryRescan(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err = rescan(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
}
