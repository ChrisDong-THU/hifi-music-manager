package browsebridge

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeviceWaitsForBackendAndStopsWaitingOnShutdown(t *testing.T) {
	var ready atomic.Bool
	var services atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path != "/device.xml" {
			if !services.Load() {
				w.WriteHeader(503)
				return
			}
			io.WriteString(w, `<scpd xmlns="urn:schemas-upnp-org:service-1-0"><specVersion><major>1</major><minor>0</minor></specVersion></scpd>`)
			return
		}
		io.WriteString(w, `<root xmlns="`+deviceNS+`"><device><serviceList><service><serviceType>`+contentNS+`</serviceType><controlURL>/content</controlURL><SCPDURL>/content.xml</SCPDURL></service><service><serviceType>`+connectionNS+`</serviceType><controlURL>/connection</controlURL><SCPDURL>/connection.xml</SCPDURL></service></serviceList></device></root>`)
	}))
	defer server.Close()
	b, err := New(Config{Listen: "192.168.1.5:9792", BackendDescription: server.URL + "/device.xml", UDN: "uuid:12345678-1234-1234-1234-123456789012"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.waitForBackend(ctx) }()
	select {
	case <-done:
		t.Fatal("device ready while SCPD backend is unavailable")
	case <-time.After(50 * time.Millisecond):
	}
	ready.Store(true)
	select {
	case <-done:
		t.Fatal("advertised before SCPD endpoints became ready")
	case <-time.After(75 * time.Millisecond):
	}
	services.Store(true)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	b.backendUntil = time.Time{}
	ready.Store(false)
	stopped, stop := context.WithCancel(context.Background())
	stop()
	if err := b.waitForBackend(stopped); err != context.Canceled {
		t.Fatalf("shutdown did not cancel readiness wait: %v", err)
	}
}
