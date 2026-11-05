package browsebridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	rand2 "math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The protocol requires product tokens; dev is a marker, not a release number.
var ssdpServer = runtime.GOOS + "/unknown UPnP/1.0 HiFiBrowse/dev"

type subscription struct {
	SID, Callback, NS string
	Expires           time.Time
	Seq               uint32
	sending, pending  bool
}

func (b *Bridge) event(w http.ResponseWriter, r *http.Request, ns string) {
	if r.Method != "SUBSCRIBE" && r.Method != "UNSUBSCRIBE" {
		w.WriteHeader(405)
		return
	}
	b.mu.Lock()
	now := time.Now()
	for id, s := range b.subs {
		if now.After(s.Expires) {
			delete(b.subs, id)
		}
	}
	defer b.mu.Unlock()
	sid := r.Header.Get("SID")
	if r.Method == "UNSUBSCRIBE" {
		s := b.subs[sid]
		if s == nil || s.NS != ns {
			w.WriteHeader(412)
			return
		}
		delete(b.subs, sid)
		return
	}
	if sid != "" {
		s := b.subs[sid]
		if s == nil || s.NS != ns || r.Header.Get("CALLBACK") != "" || r.Header.Get("NT") != "" {
			w.WriteHeader(412)
			return
		}
		s.Expires = now.Add(30 * time.Minute)
		w.Header().Set("SID", sid)
		w.Header().Set("TIMEOUT", "Second-1800")
		return
	}
	callback := r.Header.Get("CALLBACK")
	if len(b.subs) >= 32 || r.Header.Get("NT") != "upnp:event" || !strings.HasPrefix(callback, "<") || !strings.HasSuffix(callback, ">") {
		w.WriteHeader(412)
		return
	}
	u, err := url.Parse(strings.TrimSuffix(strings.TrimPrefix(callback, "<"), ">"))
	remote, _, _ := net.SplitHostPort(r.RemoteAddr)
	// Only notify a literal address belonging to the subscribing LAN peer.
	// No DNS, redirects, credentials, other LAN destinations or loopback SSRF.
	ip, e := netip.ParseAddr(uHostname(u))
	if err != nil || e != nil || !b.lan.Contains(ip) || ip.String() != remote || u.Scheme != "http" || u.User != nil || u.Fragment != "" || u.Port() == "" {
		w.WriteHeader(412)
		return
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 {
		w.WriteHeader(412)
		return
	}
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		w.WriteHeader(500)
		return
	}
	value := hex.EncodeToString(nonce[:])
	sid = "uuid:" + value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:]
	b.subs[sid] = &subscription{SID: sid, Callback: u.String(), NS: ns, Expires: now.Add(30 * time.Minute)}
	w.Header().Set("SID", sid)
	w.Header().Set("TIMEOUT", "Second-1800")
	b.launchNotification(sid)
}

func (b *Bridge) launchNotification(sid string) {
	b.notifyMu.Lock()
	defer b.notifyMu.Unlock()
	if b.stopping {
		return
	}
	b.notifyWorkers.Add(1)
	go func() { defer b.notifyWorkers.Done(); b.notify(sid) }()
}
func uHostname(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Hostname()
}
func (b *Bridge) notify(sid string) {
	b.mu.Lock()
	sub := b.subs[sid]
	if sub == nil || time.Now().After(sub.Expires) {
		b.mu.Unlock()
		return
	}
	if sub.sending {
		sub.pending = true
		b.mu.Unlock()
		return
	}
	sub.sending = true
	for {
		if b.subs[sid] != sub || time.Now().After(sub.Expires) {
			sub.sending = false
			b.mu.Unlock()
			return
		}
		sub.pending = false
		s, update := *sub, b.updateID
		if sub.Seq == ^uint32(0) {
			sub.Seq = 1
		} else {
			sub.Seq++
		}
		b.mu.Unlock()
		ctx := b.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		b.sendNotification(ctx, s, update)
		b.mu.Lock()
		if !sub.pending {
			sub.sending = false
			b.mu.Unlock()
			return
		}
	}
}

func (b *Bridge) sendNotification(ctx context.Context, s subscription, update string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if update == "" {
		update = "0"
	}
	fields := [][2]string{{"SystemUpdateID", update}, {"ContainerUpdateIDs", "0," + update}}
	if s.NS == connectionNS {
		n, e := b.call(ctx, connectionNS, "GetProtocolInfo", nil)
		if e != nil {
			return
		}
		fields = [][2]string{{"SourceProtocolInfo", n.text("Source")}, {"SinkProtocolInfo", n.text("Sink")}, {"CurrentConnectionIDs", ""}}
	}
	var body strings.Builder
	body.WriteString(`<?xml version="1.0"?><e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0">`)
	for _, v := range fields {
		body.WriteString("<e:property><" + v[0] + ">" + escaped(v[1]) + "</" + v[0] + "></e:property>")
	}
	body.WriteString("</e:propertyset>")
	req, err := http.NewRequestWithContext(ctx, "NOTIFY", s.Callback, strings.NewReader(body.String()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("NTS", "upnp:propchange")
	req.Header.Set("SID", s.SID)
	req.Header.Set("SEQ", strconv.FormatUint(uint64(s.Seq), 10))
	resp, e := b.client.Do(req)
	if e == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}
}
func (b *Bridge) events(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, e := b.call(ctx, contentNS, "GetSystemUpdateID", nil)
			if e != nil {
				continue
			}
			update := b.combinedUpdate(n.text("Id"))
			b.mu.Lock()
			changed := update != b.updateID
			b.updateID = update
			ids := []string{}
			for id, s := range b.subs {
				if time.Now().After(s.Expires) {
					delete(b.subs, id)
				} else if changed && s.NS == contentNS {
					ids = append(ids, id)
				}
			}
			b.mu.Unlock()
			for _, id := range ids {
				b.launchNotification(id)
			}
		}
	}
}

func (b *Bridge) ssdpRecords() [][2]string {
	u := b.cfg.UDN
	return [][2]string{{"upnp:rootdevice", u + "::upnp:rootdevice"}, {u, u}, {"urn:schemas-upnp-org:device:MediaServer:1", u + "::urn:schemas-upnp-org:device:MediaServer:1"}, {contentNS, u + "::" + contentNS}, {connectionNS, u + "::" + connectionNS}}
}
func (b *Bridge) ssdp(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	host, _, _ := net.SplitHostPort(b.cfg.Listen)
	localIP := net.ParseIP(host)
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	var selected *net.Interface
	for i := range interfaces {
		addrs, _ := interfaces[i].Addrs()
		for _, a := range addrs {
			address, _, _ := strings.Cut(a.String(), "/")
			if address == host {
				selected = &interfaces[i]
			}
		}
	}
	if selected == nil {
		return fmt.Errorf("LAN interface not found")
	}
	group := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	input, err := net.ListenMulticastUDP("udp4", selected, group)
	if err != nil {
		return err
	}
	defer input.Close()
	_ = input.SetReadBuffer(64 << 10)
	output, err := net.ListenUDP("udp4", &net.UDPAddr{IP: localIP, Port: 0})
	if err != nil {
		return err
	}
	defer output.Close()
	announce := func(state string) {
		for _, record := range b.ssdpRecords() {
			headers := ""
			if state == "ssdp:alive" {
				headers = "CACHE-CONTROL: max-age=1800\r\nLOCATION: " + b.base + "/device.xml\r\nSERVER: " + ssdpServer + "\r\n"
			}
			msg := "NOTIFY * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\n" + headers + "NT: " + record[0] + "\r\nNTS: " + state + "\r\nUSN: " + record[1] + "\r\n\r\n"
			_, _ = output.WriteToUDP([]byte(msg), group)
		}
	}
	announce("ssdp:alive")
	var workers sync.WaitGroup
	workers.Add(2)
	defer func() { cancel(); _ = input.Close(); workers.Wait(); announce("ssdp:byebye") }()
	go func() { defer workers.Done(); <-ctx.Done(); _ = input.Close() }()
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	go func() {
		defer workers.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				announce("ssdp:alive")
			}
		}
	}()
	buffer := make([]byte, 8192)
	responses := make(chan struct{}, 32)
	for {
		n, peer, e := input.ReadFromUDP(buffer)
		if e != nil {
			return e
		}
		ip, valid := netip.AddrFromSlice(peer.IP)
		if !valid || !b.lan.Contains(ip.Unmap()) {
			continue
		}
		req, e := http.ReadRequest(bufio.NewReader(bytes.NewReader(buffer[:n])))
		if e != nil {
			continue
		}
		req.Body.Close()
		if req.Method != "M-SEARCH" || req.Header.Get("MAN") != `"ssdp:discover"` {
			continue
		}
		st := req.Header.Get("ST")
		matched := st == "ssdp:all"
		for _, record := range b.ssdpRecords() {
			matched = matched || st == record[0]
		}
		if !matched {
			continue
		}
		mx, _ := strconv.Atoi(req.Header.Get("MX"))
		if mx < 1 {
			mx = 1
		}
		if mx > 5 {
			mx = 5
		}
		select {
		case responses <- struct{}{}:
		default:
			continue
		}
		workers.Add(1)
		go func(destination net.UDPAddr, target string, delay int) {
			defer workers.Done()
			defer func() { <-responses }()
			timer := time.NewTimer(time.Duration(rand2.IntN(delay*1000)) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			for _, record := range b.ssdpRecords() {
				if target != "ssdp:all" && target != record[0] {
					continue
				}
				response := "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nDATE: " + time.Now().UTC().Format(http.TimeFormat) + "\r\nEXT:\r\nLOCATION: " + b.base + "/device.xml\r\nSERVER: " + ssdpServer + "\r\nST: " + record[0] + "\r\nUSN: " + record[1] + "\r\n\r\n"
				_, _ = output.WriteToUDP([]byte(response), &destination)
			}
		}(*peer, st, mx)
	}
}
