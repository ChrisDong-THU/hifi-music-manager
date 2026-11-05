package browsebridge

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServeCancelsBackendWaitAndClosesItsListener(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer upstream.Close()
	b := fixture(t)
	b.cfg.BackendDescription = upstream.URL
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.Serve(ctx, listener) }()
	r, _ := http.NewRequest("GET", "http://"+listener.Addr().String()+"/device.xml", nil)
	r.Host = "192.168.1.5:9792"
	client := http.Client{Timeout: time.Second}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("service did not join background work")
	}
	if connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
		connection.Close()
		t.Fatal("listener retained after shutdown")
	}
}

func TestConfigurationRejectsMalformedIdentityPortsAndRemoteBackends(t *testing.T) {
	base := Config{Listen: "192.168.1.5:9792", BackendDescription: "http://127.0.0.1:9791/device.xml", UDN: "uuid:12345678-1234-1234-1234-123456789012"}
	for _, id := range []string{"uuid:../../../../../../../../../../../../", "uuid:xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", "uuid:123456781234-1234-1234-1234-56789012"} {
		c := base
		c.UDN = id
		if _, err := New(c); err == nil {
			t.Fatal("malformed identity accepted", id)
		}
	}
	for _, listen := range []string{"192.168.1.5:0", "192.168.1.5:65536", "192.168.1.5:-1"} {
		c := base
		c.Listen = listen
		if _, err := New(c); err == nil {
			t.Fatal("invalid listen port accepted")
		}
	}
	for _, target := range []string{"http://203.0.113.1/device.xml", "http://localhost/device.xml", "http://192.168.1.5/device.xml"} {
		c := base
		c.BackendDescription = target
		if _, err := New(c); err == nil {
			t.Fatal("non-loopback backend accepted")
		}
	}
}

func TestDiscoveryCannotRedirectItsOriginOrUseIncompleteServices(t *testing.T) {
	for _, extra := range []string{"<URLBase>http://203.0.113.1/</URLBase>", "missing-scpd"} {
		t.Run(extra, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				scpd := "/content.xml"
				prefix := extra
				if extra == "missing-scpd" {
					scpd = ""
					prefix = ""
				}
				fmt.Fprint(w, `<root xmlns="`+deviceNS+`">`+prefix+`<device><serviceList><service><serviceType>`+contentNS+`</serviceType><controlURL>/content</controlURL><SCPDURL>`+scpd+`</SCPDURL></service><service><serviceType>`+connectionNS+`</serviceType><controlURL>/connection</controlURL><SCPDURL>/connection.xml</SCPDURL></service></serviceList></device></root>`)
			}))
			defer server.Close()
			b := fixture(t)
			b.cfg.BackendDescription = server.URL + "/device.xml"
			if _, err := b.discover(context.Background()); err == nil {
				t.Fatal("unsafe or incomplete upstream accepted")
			}
		})
	}
}

func TestUpstreamLANAliasIsNormalizedWithoutAllowingOtherPeersOrPorts(t *testing.T) {
	b := fixture(t)
	origin, _ := url.Parse("http://127.0.0.1:9791/device.xml")
	for _, target := range []string{"http://192.168.1.5:9791/path?x=1", "http://127.0.0.1:9791/path?x=1"} {
		u, _ := url.Parse(target)
		if !b.localUpstreamURL(u, origin) || u.String() != "http://127.0.0.1:9791/path?x=1" {
			t.Fatal("local alias did not remain on loopback")
		}
	}
	for _, target := range []string{"http://192.168.1.6:9791/path", "http://192.168.1.5:9790/path", "http://user@192.168.1.5:9791/path", "http://192.168.1.5:9791/path#fragment"} {
		u, _ := url.Parse(target)
		if b.localUpstreamURL(u, origin) {
			t.Fatal("unsafe upstream origin accepted")
		}
	}
}

func TestSlowDiscoveryDoesNotBlockEventRenewal(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(503) }))
	defer server.Close()
	b := fixture(t)
	b.cfg.BackendDescription = server.URL
	b.subs["session"] = &subscription{SID: "session", NS: contentNS, Expires: time.Now().Add(time.Minute)}
	done := make(chan struct{})
	go func() { b.discover(context.Background()); close(done) }()
	defer func() { close(release); <-done }()
	<-entered
	renewed := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("SUBSCRIBE", b.base+"/upnp/content/event", nil)
		r.RemoteAddr = "192.168.1.3:1234"
		r.Header.Set("SID", "session")
		w := httptest.NewRecorder()
		b.Handler().ServeHTTP(w, r)
		renewed <- w.Code
	}()
	select {
	case code := <-renewed:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("vendor discovery holds event lock")
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() { _, err := b.discover(ctx); canceled <- err }()
	cancel()
	select {
	case err := <-canceled:
		if err != context.Canceled {
			t.Fatal("discovery waiter lost cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled request still waits for slow discovery")
	}
}

func TestUpstreamFaultsRetainTheirCodeAndSearchRejectsInvalidPagination(t *testing.T) {
	b := fixture(t)
	b.backend = upstream{Content: "http://127.0.0.1/content"}
	b.backendUntil = time.Now().Add(time.Hour)
	b.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		fault(w, 701, "No Such Object")
		return w.Result(), nil
	})}
	w := request(b, "/upnp/content/control", contentNS, "Browse", [][2]string{{"ObjectID", mapped("missing")}, {"BrowseFlag", "BrowseMetadata"}, {"Filter", "*"}, {"StartingIndex", "0"}, {"RequestedCount", "1"}, {"SortCriteria", ""}})
	if w.Code != 500 || !strings.Contains(w.Body.String(), "<errorCode>701</errorCode>") {
		t.Fatal("native fault was lost", w.Body.String())
	}
	w = request(b, "/upnp/content/control", contentNS, "Search", [][2]string{{"ContainerID", "0"}, {"SearchCriteria", "*"}, {"Filter", "*"}, {"StartingIndex", "-1"}, {"RequestedCount", "1"}, {"SortCriteria", ""}})
	if w.Code != 500 || !strings.Contains(w.Body.String(), "<errorCode>402</errorCode>") {
		t.Fatal("invalid search pagination was forwarded")
	}
}

func TestXMLBoundsAndSOAPEnvelopeValidation(t *testing.T) {
	for _, value := range []string{strings.Repeat("<n>", 130) + strings.Repeat("</n>", 130), "<a/> <b/>", "<a/>trailing", "<root>" + strings.Repeat("<n/>", 100001) + "</root>"} {
		if _, err := parse([]byte(value)); err == nil {
			t.Fatal("invalid or unbounded XML accepted")
		}
	}
	if _, err := soapBody([]byte(`<Envelope xmlns="wrong"><Body><a/></Body></Envelope>`)); err == nil {
		t.Fatal("wrong SOAP namespace accepted")
	}
	n, err := parse([]byte("<a>" + strings.Repeat("x<!-- comment -->", 4000) + "</a>"))
	if err != nil || n.Text != strings.Repeat("x", 4000) {
		t.Fatal("fragmented text lost", err)
	}
}

func TestDiscoveryAndBrowseRejectWrongOrTrailingXMLRoots(t *testing.T) {
	b := fixture(t)
	for _, data := range []string{
		`<root/>`, `<root xmlns="` + deviceNS + `"/> <extra/>`,
		strings.Repeat("<n>", 130) + strings.Repeat("</n>", 130),
	} {
		b.client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data)), Header: make(http.Header)}, nil
		})}
		if _, err := b.discover(context.Background()); err == nil {
			t.Fatal("invalid device description cached")
		}
	}
	for _, data := range []string{`<root/>`, `<DIDL-Lite xmlns="wrong"/>`} {
		b := groupFixture(t, func(action *node) [][2]string {
			return [][2]string{{"Result", data}, {"NumberReturned", "0"}, {"TotalMatches", "0"}, {"UpdateID", "27"}}
		})
		w := request(b, "/upnp/content/control", contentNS, "Browse", [][2]string{{"ObjectID", "tracks"}, {"BrowseFlag", "BrowseDirectChildren"}, {"Filter", "*"}, {"StartingIndex", "0"}, {"RequestedCount", "1"}, {"SortCriteria", ""}})
		if w.Code != 500 || !strings.Contains(w.Body.String(), "Invalid upstream metadata") {
			t.Fatal("invalid DIDL returned to control point", w.Body.String())
		}
	}
}

func TestNotificationsSerializeCoalesceAndWrapSequence(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	sequences := make(chan string, 4)
	var count, active, maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		if n > maximum.Load() {
			maximum.Store(n)
		}
		io.Copy(io.Discard, r.Body)
		sequences <- r.Header.Get("SEQ")
		if count.Add(1) == 1 {
			close(entered)
			<-release
		}
	}))
	defer server.Close()
	b := fixture(t)
	b.updateID = "27"
	b.subs["session"] = &subscription{SID: "session", NS: contentNS, Callback: server.URL, Expires: time.Now().Add(time.Minute), Seq: ^uint32(0)}
	done := make(chan struct{})
	go func() { b.notify("session"); close(done) }()
	<-entered
	var callers sync.WaitGroup
	for i := 0; i < 10; i++ {
		callers.Add(1)
		go func() { defer callers.Done(); b.notify("session") }()
	}
	callers.Wait()
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("notification worker did not finish")
	}
	if count.Load() != 2 || maximum.Load() != 1 || <-sequences != "4294967295" || <-sequences != "1" {
		t.Fatal("event ordering/coalescing/wrap broken")
	}
}

func TestNotificationRequestUsesServiceCancellation(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	b := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.ctx = ctx
	b.subs["session"] = &subscription{SID: "session", NS: contentNS, Callback: server.URL, Expires: time.Now().Add(time.Minute)}
	done := make(chan struct{})
	go func() { b.notify("session"); close(done) }()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown left an outbound notification running")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func groupFixture(t *testing.T, handler func(*node) [][2]string) *Bridge {
	t.Helper()
	b := fixture(t)
	b.backend = upstream{Content: "http://127.0.0.1/content"}
	b.backendUntil = time.Now().Add(time.Hour)
	b.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		action, err := soapBody(data)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(soap(contentNS, action.Name.Local+"Response", handler(action)))), Header: make(http.Header)}, nil
	})}
	return b
}

func TestArtistGroupingRejectsMixedVersionsAndInvalidCounts(t *testing.T) {
	for _, mode := range []string{"version", "negative", "count", "disk"} {
		t.Run(mode, func(t *testing.T) {
			key := "disk-a"
			b := groupFixture(t, func(action *node) [][2]string {
				if action.Name.Local == "GetSystemUpdateID" {
					return [][2]string{{"Id", "27"}}
				}
				update, total, returned := "27", "0", "0"
				switch mode {
				case "version":
					update = "28"
				case "negative":
					total = "-1"
				case "count":
					returned = "1"
				case "disk":
					key = "disk-b"
				}
				return [][2]string{{"Result", `<DIDL-Lite xmlns="` + didlNS + `"/>`}, {"TotalMatches", total}, {"NumberReturned", returned}, {"UpdateID", update}}
			})
			b.cfg.ResourceKey = func() string { return key }
			if _, _, err := b.groupedArtists(context.Background()); err == nil || b.artistGroups != nil {
				t.Fatal("unstable group committed to cache")
			}
		})
	}
}

func TestArtistPaginationDecoratesOnlyTheRequestedPage(t *testing.T) {
	var didl strings.Builder
	didl.WriteString(`<DIDL-Lite xmlns="` + didlNS + `" xmlns:dc="` + dcNS + `" xmlns:upnp="` + upnpNS + `">`)
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&didl, `<item id="track-%d"><dc:title>Track</dc:title><upnp:artist>Artist %02d</upnp:artist></item>`, i, i)
	}
	didl.WriteString(`</DIDL-Lite>`)
	b := groupFixture(t, func(action *node) [][2]string {
		if action.Name.Local == "GetSystemUpdateID" {
			return [][2]string{{"Id", "27"}}
		}
		return [][2]string{{"Result", didl.String()}, {"TotalMatches", "50"}, {"NumberReturned", "50"}, {"UpdateID", "27"}}
	})
	var opens atomic.Int32
	b.cfg.Resources = func() (*os.Root, error) { opens.Add(1); return nil, os.ErrNotExist }
	w := httptest.NewRecorder()
	b.fallbackArtists(w, httptest.NewRequest("POST", "/", nil), "artists", "BrowseDirectChildren", 10, 1)
	response, err := soapBody(w.Body.Bytes())
	if err != nil || response.text("NumberReturned") != "1" || response.text("TotalMatches") != "50" || !strings.Contains(response.text("Result"), "Artist 10") {
		t.Fatal("incorrect page", err)
	}
	if opens.Load() > 5 {
		t.Fatal("off-page artists loaded public resources", opens.Load())
	}
}
