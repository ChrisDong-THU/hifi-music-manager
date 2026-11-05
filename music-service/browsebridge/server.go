package browsebridge

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

func (b *Bridge) Handler() http.Handler {
	mux := http.NewServeMux()
	if b.cfg.Notices != nil {
		legal := http.FileServerFS(b.cfg.Notices)
		mux.Handle("GET /THIRD_PARTY_NOTICES.txt", legal)
		mux.Handle("GET /licenses/", legal)
	}
	mux.HandleFunc("GET /art/artist", b.artistPicture)
	mux.HandleFunc("GET /device.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write(b.description())
	})
	for _, kind := range []string{"content", "connection"} {
		ns := contentNS
		if kind == "connection" {
			ns = connectionNS
		}
		mux.HandleFunc("/upnp/"+kind+"/control", func(w http.ResponseWriter, r *http.Request) { b.control(w, r, ns) })
		mux.HandleFunc("/upnp/"+kind+"/event", func(w http.ResponseWriter, r *http.Request) { b.event(w, r, ns) })
		mux.HandleFunc("GET /upnp/"+kind+".xml", func(w http.ResponseWriter, r *http.Request) {
			u, e := b.discover(r.Context())
			if e != nil {
				http.Error(w, "Music library offline", 503)
				return
			}
			target := u.ContentSCPD
			if ns == connectionNS {
				target = u.ConnectionSCPD
			}
			data, e := b.readSCPD(r.Context(), target)
			if e != nil {
				http.Error(w, "Music library offline", 503)
				return
			}
			w.Header().Set("Content-Type", "text/xml; charset=utf-8")
			_, _ = w.Write(data)
		})
	}
	mux.HandleFunc("GET /icons/{name}", b.icon)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.Host)
		listenHost, _, _ := net.SplitHostPort(b.cfg.Listen)
		remote, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip, e := netip.ParseAddr(remote)
		if e != nil || !(ip.IsLoopback() || b.lan.Contains(ip)) || host != listenHost {
			http.Error(w, "LAN only", 403)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}
func (b *Bridge) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", b.cfg.Listen)
	if err != nil {
		return err
	}
	return b.Serve(ctx, listener)
}

func (b *Bridge) Serve(ctx context.Context, listener net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer b.client.CloseIdleConnections()
	b.ctx = ctx
	server := &http.Server{Handler: b.Handler(), BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if server.Shutdown(c) != nil {
			_ = server.Close()
		}
	}()
	go func() { defer workers.Done(); b.events(ctx) }()
	go func() {
		defer workers.Done()
		if err := b.waitForBackend(ctx); err != nil {
			return
		}
		lastError := ""
		for ctx.Err() == nil {
			if e := b.ssdp(ctx); e != nil && ctx.Err() == nil && e.Error() != lastError {
				log.Printf("Browse SSDP unavailable, retrying: %v", e)
				lastError = e.Error()
			}
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	log.Printf("UPnP browse view listening on %s", b.cfg.Listen)
	err := server.Serve(listener)
	cancel()
	b.notifyMu.Lock()
	b.stopping = true
	b.notifyMu.Unlock()
	workers.Wait()
	b.notifyWorkers.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Advertise only after service URLs are available. A control point can reject
// and cache a device as unusable if its first SCPD request receives a 503.
func (b *Bridge) waitForBackend(ctx context.Context) error {
	for {
		if u, err := b.discover(ctx); err == nil {
			if _, err := b.readSCPD(ctx, u.ContentSCPD); err == nil {
				if _, err := b.readSCPD(ctx, u.ConnectionSCPD); err == nil {
					return nil
				}
			}
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
