package browsebridge

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func New(c Config) (*Bridge, error) {
	// Preserve the previous access boundary for existing configurations.
	if c.LANCIDR == "" {
		c.LANCIDR = "192.168.1.0/24"
	}
	lan, err := netip.ParsePrefix(c.LANCIDR)
	if err != nil || !lan.Addr().Is4() || !lan.Addr().IsPrivate() || lan.Bits() < 8 {
		return nil, errors.New("browse LAN must be a private IPv4 subnet")
	}
	lan = lan.Masked()
	last := lan.Addr().As4()
	for bit := lan.Bits(); bit < 32; bit++ {
		last[bit/8] |= 1 << (7 - bit%8)
	}
	if !lan.Addr().IsPrivate() || !netip.AddrFrom4(last).IsPrivate() {
		return nil, errors.New("browse LAN must stay inside a private IPv4 subnet")
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return nil, err
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, errors.New("invalid browse TCP port")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !lan.Contains(ip) {
		return nil, errors.New("browse server must bind a LAN IP")
	}
	backend, err := url.Parse(c.BackendDescription)
	if err != nil || backend.Scheme != "http" || backend.Hostname() == "" || backend.User != nil || backend.Fragment != "" {
		return nil, errors.New("invalid upstream description URL")
	}
	backendIP, err := netip.ParseAddr(backend.Hostname())
	if err != nil || !backendIP.IsLoopback() {
		return nil, errors.New("upstream description must use a literal loopback address")
	}
	if port := backend.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("invalid upstream TCP port")
		}
	}
	if !strings.HasPrefix(c.UDN, "uuid:") || !validUUID(strings.TrimPrefix(c.UDN, "uuid:")) {
		return nil, errors.New("invalid browse UDN")
	}
	if c.Name == "" {
		c.Name = "hifi-music-nano"
	}
	return &Bridge{
		cfg: c, lan: lan, base: "http://" + c.Listen,
		client: &http.Client{
			Timeout:       12 * time.Second,
			Transport:     &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 4},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		subs:        make(map[string]*subscription),
		backendGate: make(requestGate, 1), artistsGate: make(requestGate, 1),
	}, nil
}
