package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	bubbleHTTPListen  = "127.0.0.1:18080"
	bubbleHTTPSListen = "127.0.0.1:18443"
	bubbleWANSource   = "192.0.2.1"
	bubblePingPath    = "/res/connectiontest"
	bubbleRPCPath     = "/xmlrpc"
	bubbleInfoMethod  = "upnpbridge.getServerInfo"
	bubbleInfoCall    = `<?xml version="1.0"?><methodCall><methodName>` + bubbleInfoMethod + `</methodName><params/></methodCall>`
	bubbleInfoLimit   = 4 << 10
	bubbleBodyLimit   = 1 << 20
)

var (
	bubbleDomainPattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$`)
	bubbleEscapedPath   = regexp.MustCompile(`%[a-fA-F0-9]{2}`)
)

type bubbleRoute int

const (
	bubbleBlocked bubbleRoute = iota
	bubbleStream
	bubbleDigest
	bubbleBasic
)

func (route bubbleRoute) authScheme() string {
	switch route {
	case bubbleDigest:
		return "Digest"
	case bubbleBasic:
		return "Basic"
	default:
		return ""
	}
}

func bubbleReadAction(service, action string) bool {
	switch service {
	case "ContentDirectory":
		switch action {
		case "Browse", "Search", "GetSearchCapabilities", "GetSortCapabilities", "GetSystemUpdateID":
			return true
		}
	case "ConnectionManager":
		switch action {
		case "GetProtocolInfo", "GetCurrentConnectionIDs", "GetCurrentConnectionInfo":
			return true
		}
	case "BubbleUPnPServer", "MainService":
		return action == "GetBaseLanURL" || action == "GetVersionInfo"
	}
	return false
}

func bubbleLinkRequest(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/res/link/") && strings.Count(r.URL.Path, "/") == 3
}

func bubbleRequestRoute(r *http.Request, libraryID string) bubbleRoute {
	p := r.URL.Path
	// Keep the original wire spelling for Digest and media tokens. Reject
	// alternate path separators and ambiguous normalization before routing.
	if p == "" || path.Clean(p) != p || strings.ContainsAny(p, `\;`) ||
		strings.ContainsFunc(p, unicode.IsControl) || bubbleEscapedPath.MatchString(p) ||
		strings.Count(p, "/") != strings.Count(r.URL.EscapedPath(), "/") {
		return bubbleBlocked
	}
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	switch parts[0] {
	case "stream":
		// The vendor requires a filename and a signed token in separate segments.
		if read && len(parts) == 3 {
			return bubbleStream
		}
	case "xmlrpc":
		if r.Method == http.MethodPost && r.URL.EscapedPath() == bubbleRPCPath && r.URL.RawQuery == "" && !r.URL.ForceQuery {
			return bubbleDigest
		}
	case "res":
		if read && (p == bubblePingPath ||
			len(parts) == 3 && parts[1] == "link" ||
			len(parts) == 5 && parts[1] == "link" && parts[3] == "proxy" && strings.TrimPrefix(parts[4], "uuid:") == libraryID) {
			return bubbleDigest
		}
		if r.Method == http.MethodPost && len(parts) == 8 && parts[1] == "dev" &&
			strings.TrimPrefix(parts[2], "uuid:") == libraryID &&
			parts[3] == "svc" && parts[6] == "action" && bubbleReadAction(parts[5], parts[7]) {
			return bubbleDigest
		}
	case "dev":
		if len(parts) < 2 || strings.TrimPrefix(parts[1], "uuid:") != libraryID {
			return bubbleBlocked
		}
		if read {
			return bubbleBasic
		}
		if r.Method == http.MethodPost && len(parts) == 6 && parts[2] == "svc" && parts[5] == "action" {
			_, action, ok := strings.Cut(strings.Trim(r.Header.Get("SOAPAction"), `"`), "#")
			if ok && bubbleReadAction(parts[4], action) {
				return bubbleBasic
			}
		}
	}
	return bubbleBlocked
}

// Forward only the parameterless server information call used during app setup.
// Send a fixed XML document so the upstream cannot interpret any other call.
func prepareBubbleInfoCall(w http.ResponseWriter, r *http.Request) bool {
	body := http.MaxBytesReader(w, r.Body, bubbleInfoLimit)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "invalid BubbleUPnP server information request", http.StatusBadRequest)
		}
		return false
	}
	var call struct {
		XMLName xml.Name   `xml:"methodCall"`
		Methods []string   `xml:"methodName"`
		Params  []struct{} `xml:"params>param"`
	}
	if xml.Unmarshal(data, &call) != nil || len(call.Methods) != 1 || call.Methods[0] != bubbleInfoMethod || len(call.Params) != 0 {
		http.NotFound(w, r)
		return false
	}
	r.Body = io.NopCloser(strings.NewReader(bubbleInfoCall))
	r.ContentLength = int64(len(bubbleInfoCall))
	r.TransferEncoding = nil
	r.Trailer = nil
	r.Header.Set("Content-Type", "text/xml; charset=UTF-8")
	r.Header.Del("Content-Encoding")
	return true
}

// BubbleUPnP exports newly discovered devices by default. Limit the public
// discovery response to the same library as the public control routes.
func limitBubbleDevices(response *http.Response, libraryID string) error {
	body, err := io.ReadAll(io.LimitReader(response.Body, bubbleBodyLimit+1))
	if err != nil || len(body) > bubbleBodyLimit {
		return errors.New("invalid BubbleUPnP discovery response size")
	}
	var listing map[string]json.RawMessage
	var devices []json.RawMessage
	if json.Unmarshal(body, &listing) != nil || json.Unmarshal(listing["devices"], &devices) != nil {
		return errors.New("invalid BubbleUPnP discovery response")
	}
	selected := make([]json.RawMessage, 0, 1)
	for _, device := range devices {
		var identity struct {
			UDN string `json:"udn"`
		}
		if json.Unmarshal(device, &identity) != nil || identity.UDN == "" {
			return errors.New("invalid BubbleUPnP device identity")
		}
		if strings.TrimPrefix(identity.UDN, "uuid:") == libraryID {
			selected = append(selected, device)
		}
	}
	listing["devices"], err = json.Marshal(selected)
	if err != nil {
		return err
	}
	body, err = json.Marshal(listing)
	if err != nil {
		return err
	}
	response.Body.Close()
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	response.Header.Set("Content-Length", strconv.Itoa(len(body)))
	response.Header.Del("ETag")
	return nil
}

func bubbleHasScheme(value, scheme string) bool {
	name, credentials, ok := strings.Cut(value, " ")
	return ok && strings.EqualFold(name, scheme) && strings.TrimSpace(credentials) != ""
}

func bubbleHasChallenge(header http.Header, scheme string) bool {
	for _, value := range header.Values("WWW-Authenticate") {
		if bubbleHasScheme(value, scheme) {
			return true
		}
	}
	return false
}

func newBubbleRelayHandler(domain, libraryID string, secure bool, upstream *url.URL, transport http.RoundTripper) http.Handler {
	port := ":80"
	if secure {
		port = ":443"
	}
	unavailable := func(w http.ResponseWriter) {
		http.Error(w, "BubbleUPnP unavailable", http.StatusBadGateway)
	}
	probe := func(ctx context.Context) bool {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		target := *upstream
		target.Path = bubblePingPath
		request := &http.Request{Method: http.MethodGet, URL: &target, Host: domain, Header: make(http.Header)}
		response, err := transport.RoundTrip(request.WithContext(ctx))
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusUnauthorized && bubbleHasChallenge(response.Header, "Digest")
	}
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			pr.Out.Host = pr.In.Host
			for header := range pr.Out.Header {
				if strings.HasPrefix(strings.ToLower(header), "x-forwarded-") {
					pr.Out.Header.Del(header)
				}
			}
			for _, header := range []string{"X-Real-IP", "X-Original-URL", "X-Rewrite-URL", "Cookie"} {
				pr.Out.Header.Del(header)
			}
			if bubbleLinkRequest(pr.In) {
				pr.Out.Header.Del("Accept-Encoding")
				pr.Out.Header.Del("If-None-Match")
				pr.Out.Header.Del("If-Modified-Since")
			}
			if bubbleRequestRoute(pr.In, libraryID) == bubbleStream {
				pr.Out.Header.Del("Authorization")
			}
		},
		ModifyResponse: func(response *http.Response) error {
			request := response.Request
			if request == nil {
				return errors.New("BubbleUPnP response has no request")
			}
			scheme := bubbleRequestRoute(request, libraryID).authScheme()
			if scheme != "" && !bubbleHasScheme(request.Header.Get("Authorization"), scheme) &&
				(response.StatusCode != http.StatusUnauthorized || !bubbleHasChallenge(response.Header, scheme)) {
				return errors.New("BubbleUPnP did not require the expected authentication")
			}
			response.Header.Del("Set-Cookie")
			response.Header.Set("Cache-Control", "private, no-store")
			if bubbleLinkRequest(request) && response.StatusCode >= 200 && response.StatusCode < 300 {
				return limitBubbleDevices(response, libraryID)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			unavailable(w)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		if !strings.EqualFold(r.Host, domain) && !strings.EqualFold(r.Host, domain+port) {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		route := bubbleRequestRoute(r, libraryID)
		if route == bubbleBlocked || (!secure && route != bubbleStream) {
			http.NotFound(w, r)
			return
		}
		if route != bubbleStream {
			scheme := route.authScheme()
			if authorization := r.Header.Get("Authorization"); authorization != "" && !bubbleHasScheme(authorization, scheme) {
				http.Error(w, "unsupported authorization scheme", http.StatusBadRequest)
				return
			}
		}
		if r.Method == http.MethodPost {
			if r.ContentLength > bubbleBodyLimit {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, bubbleBodyLimit)
			controller := http.NewResponseController(w)
			_ = controller.SetReadDeadline(time.Now().Add(30 * time.Second))
			defer controller.SetReadDeadline(time.Time{})
			if r.URL.Path == bubbleRPCPath && !prepareBubbleInfoCall(w, r) {
				return
			}
		} else if r.ContentLength != 0 {
			http.Error(w, "unexpected request body", http.StatusBadRequest)
			return
		}
		// Check that the upstream treats the connection as WAN before
		// forwarding credentials or protocol requests. ModifyResponse checks
		// the anonymous ping directly.
		if route != bubbleStream && (r.URL.Path != bubblePingPath || r.Header.Get("Authorization") != "") && !probe(r.Context()) {
			unavailable(w)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func runBubbleRelay(ctx context.Context, home, domain, upstreamIP string) error {
	_, domainIsIP := netip.ParseAddr(domain)
	if len(domain) > 253 || !bubbleDomainPattern.MatchString(domain) || domainIsIP == nil {
		return errors.New("BubbleUPnP relay requires a DNS domain")
	}
	domain = strings.ToLower(domain)
	ip, err := netip.ParseAddr(upstreamIP)
	if err != nil || !ip.Is4() || !ip.IsPrivate() {
		return errors.New("BubbleUPnP relay upstream must be a private IPv4 address")
	}
	library, err := readServiceConfig(filepath.Join(home, "music-service", "config.json"))
	if err != nil {
		return err
	}
	libraryID, ok := strings.CutPrefix(library.BrowseUDN, "uuid:")
	if !ok || libraryID == "" {
		return errors.New("BubbleUPnP relay requires the HiFi library UDN")
	}
	config := filepath.Join(home, "config")
	pair, err := tls.LoadX509KeyPair(filepath.Join(config, "bubble-public-tls-cert.pem"), filepath.Join(config, "bubble-public-tls-key.pem"))
	if err != nil {
		return fmt.Errorf("load BubbleUPnP TLS certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if err := leaf.VerifyHostname(domain); err != nil {
		return err
	}

	dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(bubbleWANSource)}, Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: dialer.DialContext, DisableCompression: true, ResponseHeaderTimeout: 30 * time.Second,
		MaxResponseHeaderBytes: 32 << 10, MaxIdleConnsPerHost: 8, IdleConnTimeout: 60 * time.Second,
	}
	defer transport.CloseIdleConnections()
	upstream := &url.URL{Scheme: "http", Host: net.JoinHostPort(ip.String(), "80")}
	plain, err := net.Listen("tcp4", bubbleHTTPListen)
	if err != nil {
		return err
	}
	defer plain.Close()
	secure, err := net.Listen("tcp4", bubbleHTTPSListen)
	if err != nil {
		return err
	}
	defer secure.Close()

	makeServer := func(secure bool) *http.Server {
		return &http.Server{
			Handler: newBubbleRelayHandler(domain, libraryID, secure, upstream, transport), ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10,
		}
	}
	httpServer, tlsServer := makeServer(false), makeServer(true)
	tlsServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	served := make(chan error, 2)
	go func() { served <- httpServer.Serve(plain) }()
	go func() { served <- tlsServer.ServeTLS(secure, "", "") }()
	log.Printf("BubbleUPnP relay · %s · %s / %s", domain, plain.Addr(), secure.Addr())
	select {
	case <-ctx.Done():
	case err = <-served:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var group sync.WaitGroup
	for _, server := range []*http.Server{httpServer, tlsServer} {
		group.Go(func() {
			if server.Shutdown(shutdown) != nil {
				_ = server.Close()
			}
		})
	}
	group.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
