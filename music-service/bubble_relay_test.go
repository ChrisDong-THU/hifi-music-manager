package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type bubbleTestTransport func(*http.Request) (*http.Response, error)

func (transport bubbleTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func bubbleTestResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}
}

func TestBubbleRelayRestrictsPublicRoutes(t *testing.T) {
	upstream := &url.URL{Scheme: "http", Host: "192.168.18.2:80"}
	transport := bubbleTestTransport(func(r *http.Request) (*http.Response, error) {
		t.Errorf("blocked request reached upstream: %s %s", r.Method, r.URL.Path)
		return bubbleTestResponse(r, http.StatusOK, ""), nil
	})
	for _, test := range []struct {
		secure       bool
		method, path string
		host         string
		status       int
	}{
		{false, "GET", "/res/connectiontest", "music.example.test", 404},
		{false, "GET", "/stream/unsigned", "music.example.test", 404},
		{false, "GET", "/stream/a/b", "other.example.test", 421},
		{true, "GET", "/", "music.example.test", 404},
		{true, "GET", "/index.html", "music.example.test", 404},
		{true, "GET", "/DeviceDescription.xml", "music.example.test", 404},
		{true, "GET", "/SuperMediaServer/DeviceDescription.xml", "music.example.test", 404},
		{true, "GET", "/dev/other/desc", "music.example.test", 404},
		{true, "POST", "/gwtRequest", "music.example.test", 404},
		{true, "GET", "/proxy", "music.example.test", 404},
		{true, "GET", "/stream/a%2Fb/token", "music.example.test", 404},
		{true, "GET", "/stream/a/%252e%252e", "music.example.test", 404},
		{true, "GET", "/stream/a/..;x", "music.example.test", 404},
		{true, "POST", "/res/dev/id/svc/upnp-org/ContentDirectory/action/DestroyObject", "music.example.test", 404},
		{true, "POST", "/res/dev/other/svc/upnp-org/ContentDirectory/action/Browse", "music.example.test", 404},
		{true, "GET", "/res/link/id/proxy/other", "music.example.test", 404},
		{true, "PUT", "/res/link/id/proxy/device", "music.example.test", 404},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Host = test.host
		response := httptest.NewRecorder()
		newBubbleRelayHandler("music.example.test", "id", test.secure, upstream, transport).ServeHTTP(response, request)
		if response.Code != test.status {
			t.Errorf("%s %s host %s: got %d want %d", test.method, test.path, test.host, response.Code, test.status)
		}
	}
}

func TestBubbleRelayServerInfo(t *testing.T) {
	const domain = "music.example.test"
	const digest = `Digest username="owner", response="fixture"`
	const infoCall = `<?xml version="1.0" encoding="UTF-8"?><methodCall><methodName>upnpbridge.getServerInfo</methodName><params></params></methodCall>`
	upstream := &url.URL{Scheme: "http", Host: "192.168.18.2:80"}
	var forwarded int
	transport := bubbleTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == bubbleRPCPath {
			forwarded++
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != bubbleInfoCall || r.ContentLength != int64(len(body)) ||
				r.Header.Get("Content-Type") != "text/xml; charset=UTF-8" {
				t.Fatalf("unexpected upstream XML-RPC request: %s (%v)", body, err)
			}
			if r.Header.Get("Authorization") == digest {
				return bubbleTestResponse(r, 200, `<methodResponse/>`), nil
			}
		}
		response := bubbleTestResponse(r, 401, "")
		response.Header.Set("WWW-Authenticate", `Digest realm="BubbleUPnP Server", nonce="test"`)
		return response, nil
	})
	for _, test := range []struct {
		name, method, path, body, auth string
		secure, forward                bool
		status                         int
	}{
		{"authentication challenge", "POST", bubbleRPCPath, infoCall, "", true, true, 401},
		{"server information", "POST", bubbleRPCPath, infoCall, digest, true, true, 200},
		{"plaintext", "POST", bubbleRPCPath, infoCall, digest, false, false, 404},
		{"GET", "GET", bubbleRPCPath, "", digest, true, false, 404},
		{"other method", "POST", bubbleRPCPath, strings.Replace(infoCall, bubbleInfoMethod, "system.listMethods", 1), digest, true, false, 404},
		{"duplicate method", "POST", bubbleRPCPath, strings.Replace(infoCall, "<params>", "<methodName>other</methodName><params>", 1), digest, true, false, 404},
		{"unexpected argument", "POST", bubbleRPCPath, strings.Replace(infoCall, "</params>", "<param><value>x</value></param></params>", 1), digest, true, false, 404},
		{"oversized XML", "POST", bubbleRPCPath, infoCall + strings.Repeat(" ", bubbleInfoLimit), digest, true, false, 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := forwarded
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Host = domain
			request.Header.Set("Authorization", test.auth)
			response := httptest.NewRecorder()
			newBubbleRelayHandler(domain, "id", test.secure, upstream, transport).ServeHTTP(response, request)
			if response.Code != test.status || (forwarded > before) != test.forward {
				t.Fatalf("got status %d and %d upstream calls", response.Code, forwarded-before)
			}
			if test.status == 401 && !bubbleHasChallenge(response.Header(), "Digest") {
				t.Fatal("missing Digest challenge")
			}
		})
	}
}

func TestBubbleRelayPreservesWANAuthenticationAndRange(t *testing.T) {
	const domain = "music.example.test"
	const digest = `Digest username="owner", response="fixture"`
	upstream := &url.URL{Scheme: "http", Host: "192.168.18.2:80"}
	wan := true
	var requests int
	transport := bubbleTestTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Host != upstream.Host || (r.Host != domain && r.Host != domain+":80") {
			t.Errorf("unexpected target %s (Host %s)", r.URL, r.Host)
		}
		for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Port", "X-Real-IP", "Cookie"} {
			if r.Header.Get(name) != "" {
				t.Errorf("%s reached upstream", name)
			}
		}
		switch r.URL.Path {
		case bubblePingPath:
			if !wan {
				return bubbleTestResponse(r, http.StatusOK, "unsafe LAN response"), nil
			}
			response := bubbleTestResponse(r, http.StatusUnauthorized, "")
			response.Header.Set("WWW-Authenticate", `Digest realm="BubbleUPnP Server", nonce="test"`)
			return response, nil
		case "/res/dev/id/svc/upnp-org/ContentDirectory/action/Browse":
			if r.Header.Get("Authorization") != digest {
				return bubbleTestResponse(r, http.StatusOK, "unexpected anonymous data"), nil
			}
			return bubbleTestResponse(r, http.StatusOK, "result"), nil
		case "/res/link/fixture":
			if r.Header.Get("Accept-Encoding") != "" {
				t.Error("discovery response must not be compressed by the upstream")
			}
			return bubbleTestResponse(r, http.StatusCreated, `{"devices":[{"udn":"uuid:id","friendlyName":"HiFi"},{"udn":"uuid:other","friendlyName":"Other library"}]}`), nil
		case "/stream/艺术家 - 歌曲.flac/token":
			if r.URL.EscapedPath() != "/stream/%E8%89%BA%E6%9C%AF%E5%AE%B6%20-%20%E6%AD%8C%E6%9B%B2.flac/token" ||
				r.URL.RawQuery != "format=original" || r.Header.Get("Authorization") != "" ||
				r.Header.Get("Range") != "bytes=1-2" {
				t.Errorf("unexpected upstream media request: %s %v", r.URL, r.Header)
			}
			response := bubbleTestResponse(r, http.StatusPartialContent, "bc")
			response.Header.Set("Content-Range", "bytes 1-2/4")
			return response, nil
		default:
			t.Errorf("unexpected upstream request %s", r.URL.Path)
			return bubbleTestResponse(r, http.StatusNotFound, ""), nil
		}
	})
	secure := newBubbleRelayHandler(domain, "id", true, upstream, transport)
	plain := newBubbleRelayHandler(domain, "id", false, upstream, transport)
	request := func(handler http.Handler, host, method, path, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Host = host
		r.Header.Set("Authorization", auth)
		r.Header.Set("Forwarded", "for=127.0.0.1")
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r.Header.Set("X-Forwarded-Port", "58050")
		r.Header.Set("X-Real-IP", "127.0.0.1")
		r.Header.Set("Cookie", "fixture=1")
		r.Header.Set("Range", "bytes=1-2")
		r.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request(secure, domain, "GET", bubblePingPath, ""); got.Code != 401 || !bubbleHasChallenge(got.Header(), "Digest") {
		t.Fatalf("Digest challenge was not relayed: %d %v", got.Code, got.Header())
	}
	const browse = "/res/dev/id/svc/upnp-org/ContentDirectory/action/Browse"
	if got := request(secure, domain, "POST", browse, ""); got.Code != 502 || strings.Contains(got.Body.String(), "unexpected anonymous data") {
		t.Fatalf("anonymous upstream data reached the client: %d %s", got.Code, got.Body.String())
	}
	if got := request(secure, domain, "POST", browse, digest); got.Code != 200 || got.Body.String() != "result" {
		t.Fatalf("authenticated browse failed: %d %s", got.Code, got.Body.String())
	}
	if got := request(secure, domain, "GET", "/res/link/fixture", digest); got.Code != 201 ||
		got.Body.String() != `{"devices":[{"udn":"uuid:id","friendlyName":"HiFi"}]}` {
		t.Fatalf("unrelated library leaked in discovery: %d %s", got.Code, got.Body.String())
	}
	const media = "/stream/%E8%89%BA%E6%9C%AF%E5%AE%B6%20-%20%E6%AD%8C%E6%9B%B2.flac/token?format=original"
	if got := request(plain, domain+":80", "GET", media, digest); got.Code != 206 ||
		got.Body.String() != "bc" || got.Header().Get("Content-Range") != "bytes 1-2/4" {
		t.Fatalf("media range failed: %d %v %s", got.Code, got.Header(), got.Body.String())
	}
	wan = false
	before := requests
	if got := request(secure, domain, "POST", browse, digest); got.Code != 502 || requests != before+1 {
		t.Fatalf("credentials went upstream without WAN authentication: %d (%d requests)", got.Code, requests-before)
	}
	if got := request(secure, domain, "GET", bubblePingPath, ""); got.Code != 502 || strings.Contains(got.Body.String(), "unsafe LAN response") {
		t.Fatalf("unexpected anonymous ping was published: %d %s", got.Code, got.Body.String())
	}
}
