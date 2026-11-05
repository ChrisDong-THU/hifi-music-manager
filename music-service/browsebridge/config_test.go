package browsebridge

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewRejectsInvalidBackendBeforeServingRequests(t *testing.T) {
	for _, backend := range []string{"", "/device.xml", "://broken", "http://", "file:///device.xml", "http://127.0.0.1:bad/device.xml", "http://user:pass@127.0.0.1/device.xml", "http://127.0.0.1/device.xml#fragment"} {
		t.Run(backend, func(t *testing.T) {
			if _, err := New(Config{Listen: "192.168.1.5:9792", BackendDescription: backend, UDN: "uuid:12345678-1234-1234-1234-123456789012"}); err == nil {
				t.Fatal("invalid upstream configuration accepted")
			}
		})
	}
}

func TestDeviceOnAnotherPrivateNetwork(t *testing.T) {
	b, err := New(Config{Listen: "10.42.7.8:9792", LANCIDR: "10.42.7.0/24", Name: "Living room",
		BackendDescription: "http://127.0.0.1:9791/device.xml", UDN: "uuid:12345678-1234-1234-1234-123456789012"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		peer string
		want int
	}{{"10.42.7.9:1234", 200}, {"10.42.8.9:1234", 403}, {"192.168.1.3:1234", 403}, {"203.0.113.2:1234", 403}} {
		r := httptest.NewRequest("GET", "http://10.42.7.8:9792/device.xml", nil)
		r.RemoteAddr = test.peer
		w := httptest.NewRecorder()
		b.Handler().ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("%s: status %d, want %d", test.peer, w.Code, test.want)
		}
		if w.Code == 200 && !strings.Contains(w.Body.String(), "Living room") {
			t.Fatal("configured device name was not used")
		}
	}
	for _, callback := range []string{"http://10.42.8.9:1234/", "http://192.168.1.3:1234/", "http://127.0.0.1:1234/", "http://10.42.7.10:1234/"} {
		r := httptest.NewRequest("SUBSCRIBE", "http://10.42.7.8:9792/upnp/content/event", nil)
		r.RemoteAddr = "10.42.7.9:1234"
		r.Header.Set("NT", "upnp:event")
		r.Header.Set("CALLBACK", "<"+callback+">")
		w := httptest.NewRecorder()
		b.Handler().ServeHTTP(w, r)
		if w.Code != 412 {
			t.Fatalf("unsafe callback %s accepted: %d", callback, w.Code)
		}
	}
}

func TestDeviceRejectsNonPrivateOrOversizedNetworks(t *testing.T) {
	for _, network := range []string{"0.0.0.0/0", "172.16.0.0/11", "192.168.0.0/15", "203.0.113.0/24", "127.0.0.0/8", "fc00::/7", "invalid"} {
		if _, err := New(Config{Listen: "192.168.1.5:9792", LANCIDR: network,
			BackendDescription: "http://127.0.0.1:9791/device.xml", UDN: "uuid:12345678-1234-1234-1234-123456789012"}); err == nil {
			t.Fatalf("unsafe LAN accepted: %s", network)
		}
	}
}
