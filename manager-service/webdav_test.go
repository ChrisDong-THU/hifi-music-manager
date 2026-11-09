package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebDAVBrowseStreamAndAuthenticationBoundaries(t *testing.T) {
	a := testApp(t)
	if err := a.music.Mkdir("测试 #1", 0700); err != nil {
		t.Fatal(err)
	}
	const name = "测试 #1/曲目.flac"
	for _, file := range []string{name, "manager.db", ".private.flac", "测试 #1/.pending.flac"} {
		if err := a.music.WriteFile(filepath.FromSlash(file), []byte("fLaC audio"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	linked := os.Symlink(filepath.Join(a.music.Name(), filepath.FromSlash(name)), filepath.Join(a.music.Name(), "link.flac")) == nil
	home := t.TempDir()
	auth, err := openManagerAuth(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { auth.db.Close() })
	dav, err := loadMusicDAV(home)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := loadMusicDAV(home)
	if err != nil || reopened.password != dav.password {
		t.Fatal("WebDAV credentials did not survive reopening", err)
	}
	controller := &diskController{security: a, site: http.NotFoundHandler(), active: a, volumeID: "selected"}
	lan := a.protectManagerDAV(controller, auth, controller.health, "", "", dav)
	public := a.protectManagerDAV(controller, auth, controller.health, "music.example.net", "", nil)
	request := func(handler http.Handler, host, method, path, depth, password string, headers http.Header) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://"+host+path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Depth", depth)
		for k, values := range headers {
			r.Header[k] = values
		}
		if password != "" {
			r.SetBasicAuth(davUser, password)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	const host = "127.0.0.1:8787"
	if w := request(lan, host, "OPTIONS", "/webdav/", "", "", nil); w.Code != 200 || w.Header().Get("Allow") != davMethods {
		t.Fatalf("invalid WebDAV capabilities: %d %v", w.Code, w.Header())
	}
	for _, password := range []string{"", "wrong"} {
		if w := request(lan, host, "PROPFIND", "/webdav/", "1", password, nil); w.Code != 401 || !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Basic ") {
			t.Fatalf("WebDAV did not challenge an unauthorized request: %d", w.Code)
		}
	}
	collection := (&url.URL{Path: "/webdav/测试 #1/"}).EscapedPath()
	file := (&url.URL{Path: "/webdav/" + name}).EscapedPath()
	if w := request(lan, host, "PROPFIND", "/webdav/", "1", dav.password, nil); w.Code != 207 ||
		!strings.Contains(w.Body.String(), collection) || strings.Contains(w.Body.String(), "manager.db") ||
		strings.Contains(w.Body.String(), "private.flac") || strings.Contains(w.Body.String(), "link.flac") {
		t.Fatalf("unexpected root listing: %d %s", w.Code, w.Body.String())
	}
	if w := request(lan, host, "PROPFIND", collection, "1", dav.password, nil); w.Code != 207 ||
		!strings.Contains(w.Body.String(), file) || strings.Contains(w.Body.String(), ".pending.flac") {
		t.Fatalf("unexpected album listing: %d %s", w.Code, w.Body.String())
	}
	if w := request(lan, host, "GET", file, "", dav.password, http.Header{"Range": {"bytes=0-3"}}); w.Code != 206 || w.Body.String() != "fLaC" {
		t.Fatalf("audio range: %d %q", w.Code, w.Body.String())
	}
	if w := request(lan, host, "HEAD", file, "", dav.password, nil); w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "10" {
		t.Fatalf("audio metadata: %d %v", w.Code, w.Header())
	}
	for _, method := range []string{"PUT", "DELETE", "MOVE", "COPY", "LOCK", "MKCOL", "PROPPATCH"} {
		if w := request(lan, host, method, file, "", dav.password, nil); w.Code != 405 {
			t.Fatalf("WebDAV accepted write method %s: %d", method, w.Code)
		}
	}
	for _, hidden := range []string{"manager.db", ".private.flac", "%2e%2e/manager/manager.db", "测试%20%231/.pending.flac"} {
		if w := request(lan, host, "GET", "/webdav/"+hidden, "", dav.password, nil); w.Code != 404 {
			t.Fatalf("WebDAV exposed a private path: %s, %d", hidden, w.Code)
		}
	}
	if linked {
		if w := request(lan, host, "GET", "/webdav/link.flac", "", dav.password, nil); w.Code != 404 {
			t.Fatalf("WebDAV followed a symlink: %d", w.Code)
		}
	}
	if w := request(lan, host, "PROPFIND", "/webdav/", "infinity", dav.password, nil); w.Code != 403 {
		t.Fatalf("WebDAV accepted an unbounded listing: %d", w.Code)
	}
	if w := request(lan, host, "GET", file, "", dav.password, http.Header{"Origin": {"https://unrelated.example.net"}}); w.Code != 403 {
		t.Fatalf("WebDAV accepted cross-origin credentials: %d", w.Code)
	}
	if w := request(public, "music.example.net", "PROPFIND", "/webdav/", "0", dav.password, nil); w.Code != 404 {
		t.Fatalf("WebDAV is enabled on an unconfigured public listener: %d", w.Code)
	}
	for _, path := range []string{"/api/state", davSettings} {
		if w := request(lan, host, "GET", path, "", dav.password, nil); w.Code != 401 || strings.Contains(w.Body.String(), dav.password) {
			t.Fatalf("WebDAV credentials opened a management API: %d", w.Code)
		}
	}
	login := httptest.NewRecorder()
	auth.startSession(login, httptest.NewRequest("GET", "https://"+host, nil), "hifi_auth_"+strings.TrimPrefix(a.cookieName, "hifi_session_"), time.Now())
	cookie := http.Header{"Cookie": {login.Result().Cookies()[0].String()}}
	if w := request(lan, host, "GET", davSettings, "", "", cookie); w.Code != 200 ||
		!strings.Contains(w.Body.String(), dav.password) || !strings.Contains(w.Body.String(), "https://"+host+"/webdav/") {
		t.Fatalf("management settings missing: %d", w.Code)
	}
	controller.targetID = "new-volume"
	if w := request(lan, host, "GET", file, "", dav.password, nil); w.Code != 503 {
		t.Fatalf("WebDAV exposed old disk during selection: %d", w.Code)
	}
}
