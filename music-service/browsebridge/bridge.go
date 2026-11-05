package browsebridge

import (
	"context"
	"encoding/base64"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Listen, BackendDescription, UDN, Name, LANCIDR string
	Notices                                        fs.FS
	Resources                                      func() (*os.Root, error)
	ResourceKey                                    func() string
}
type upstream struct{ Content, Connection, ContentSCPD, ConnectionSCPD string }
type Bridge struct {
	cfg           Config
	lan           netip.Prefix
	base          string
	client        *http.Client
	ctx           context.Context
	notifyMu      sync.Mutex
	notifyWorkers sync.WaitGroup
	stopping      bool
	backendGate   requestGate
	mu            sync.Mutex
	backend       upstream
	backendUntil  time.Time
	subs          map[string]*subscription
	updateID      string
	artistsGate   requestGate
	artistVersion string
	artistGroups  map[string][]*node
}

// Serializing slow upstream work must not make a canceled request wait for
// another request's discovery or full-library traversal to finish.
type requestGate chan struct{}

func (g requestGate) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case g <- struct{}{}:
		if err := ctx.Err(); err != nil {
			g.unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g requestGate) unlock() { <-g }

type category struct{ ID, Native, Title, Icon string }

var categories = []category{{"albums", "0$albums", "专辑", "albums"}, {"tracks", "0$items", "曲目", "tracks"}, {"artists", "0$=Artist", "歌手", "artists"}, {"playlists", "0$playlists", "我的播放列表", "playlists"}}

func mapped(id string) string {
	if id == "0" || id == "-1" {
		return id
	}
	for _, c := range categories {
		if id == c.Native {
			return c.ID
		}
	}
	return "m:" + base64.RawURLEncoding.EncodeToString([]byte(id))
}
func native(id string) (string, error) {
	if id == "0" {
		return id, nil
	}
	for _, c := range categories {
		if id == c.ID {
			return c.Native, nil
		}
	}
	if !strings.HasPrefix(id, "m:") || len(id) > 8192 {
		return "", errors.New("unknown object")
	}
	v, err := base64.RawURLEncoding.DecodeString(id[2:])
	if err != nil || len(v) == 0 || strings.ContainsRune(string(v), '\x00') {
		return "", errors.New("invalid object")
	}
	return string(v), nil
}
func (b *Bridge) category(c category) *node {
	n := element(didlNS, "container", "")
	n.setAttr("id", c.ID)
	n.setAttr("parentID", "0")
	n.setAttr("restricted", "1")
	n.setAttr("searchable", "1")
	n.Children = []*node{element(dcNS, "title", c.Title), element(upnpNS, "class", "object.container"), element(upnpNS, "albumArtURI", b.base+"/icons/"+c.Icon+".png?v="+iconRevision)}
	return n
}
func (b *Bridge) root(id string) *node {
	n := element(didlNS, "DIDL-Lite", "")
	if id == "0" {
		root := element(didlNS, "container", "")
		root.setAttr("id", "0")
		root.setAttr("parentID", "-1")
		root.setAttr("restricted", "1")
		root.setAttr("childCount", "4")
		root.Children = []*node{element(dcNS, "title", b.cfg.Name), element(upnpNS, "class", "object.container")}
		n.Children = append(n.Children, root)
		return n
	}
	for _, c := range categories {
		if c.ID == id {
			n.Children = append(n.Children, b.category(c))
		}
	}
	return n
}
func fault(w http.ResponseWriter, code int, description string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(500)
	_, _ = w.Write([]byte(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>` + strconv.Itoa(code) + `</errorCode><errorDescription>` + escaped(description) + `</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`))
}
func (b *Bridge) control(w http.ResponseWriter, r *http.Request, ns string) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	data, err := boundedRead(r.Body)
	if err != nil {
		fault(w, 402, "Invalid Args")
		return
	}
	action, err := soapBody(data)
	if err != nil || action.Name.Space != ns {
		fault(w, 402, "Invalid Args")
		return
	}
	allowed := map[string][]string{"Browse": {"ObjectID", "BrowseFlag", "Filter", "StartingIndex", "RequestedCount", "SortCriteria"}, "Search": {"ContainerID", "SearchCriteria", "Filter", "StartingIndex", "RequestedCount", "SortCriteria"}, "GetSearchCapabilities": {}, "GetSortCapabilities": {}, "GetSystemUpdateID": {}}
	if ns == connectionNS {
		allowed = map[string][]string{"GetProtocolInfo": {}, "GetCurrentConnectionIDs": {}, "GetCurrentConnectionInfo": {"ConnectionID"}}
	}
	keys, ok := allowed[action.Name.Local]
	if !ok {
		fault(w, 401, "Invalid Action")
		return
	}
	fields := [][2]string{}
	for _, k := range keys {
		if action.child(k) == nil {
			fault(w, 402, "Invalid Args")
			return
		}
		fields = append(fields, [2]string{k, action.text(k)})
	}
	if action.Name.Local == "Browse" {
		id := action.text("ObjectID")
		flag := action.text("BrowseFlag")
		if flag != "BrowseMetadata" && flag != "BrowseDirectChildren" {
			fault(w, 402, "Invalid Args")
			return
		}
		start, e1 := strconv.ParseUint(action.text("StartingIndex"), 10, 32)
		count, e2 := strconv.ParseUint(action.text("RequestedCount"), 10, 32)
		if e1 != nil || e2 != nil {
			fault(w, 402, "Invalid Args")
			return
		}
		if strings.HasPrefix(id, "a:") {
			b.fallbackArtists(w, r, id, flag, start, count)
			return
		}
		if id == "0" || flag == "BrowseMetadata" && !strings.HasPrefix(id, "m:") {
			tree := b.root(id)
			if id == "0" && flag == "BrowseDirectChildren" {
				tree.Children = nil
				for _, c := range categories {
					tree.Children = append(tree.Children, b.category(c))
				}
			}
			if len(tree.Children) == 0 {
				fault(w, 701, "No Such Object")
				return
			}
			total := len(tree.Children)
			if flag == "BrowseDirectChildren" {
				if start >= uint64(total) {
					tree.Children = nil
				} else {
					end := uint64(total)
					if count > 0 && count < end-start {
						end = start + count
					}
					tree.Children = tree.Children[start:end]
				}
			}
			update := "0"
			if n, e := b.call(r.Context(), contentNS, "GetSystemUpdateID", nil); e == nil {
				update = n.text("Id")
			}
			b.browseResponse(w, tree, total, update)
			return
		}
		actual, e := native(id)
		if e != nil {
			fault(w, 701, "No Such Object")
			return
		}
		fields[0][1] = actual
		// MinimServer omits tag-based Artist and playlist containers when the
		// library is empty. Our stable virtual categories must still open.
		if id == "playlists" || id == "artists" {
			root, update, e := b.browseNative(r.Context(), "0")
			if e != nil {
				fault(w, 501, "Music library offline")
				return
			}
			exists := false
			for _, c := range root.Children {
				if c.attr("id") == actual {
					exists = true
				}
			}
			if !exists {
				if id == "artists" {
					b.fallbackArtists(w, r, id, flag, start, count)
					return
				}
				b.browseResponse(w, element(didlNS, "DIDL-Lite", ""), 0, update)
				return
			}
		}
	} else if action.Name.Local == "Search" {
		if _, err := strconv.ParseUint(action.text("StartingIndex"), 10, 32); err != nil {
			fault(w, 402, "Invalid Args")
			return
		}
		if _, err := strconv.ParseUint(action.text("RequestedCount"), 10, 32); err != nil {
			fault(w, 402, "Invalid Args")
			return
		}
		actual, e := native(action.text("ContainerID"))
		if e != nil {
			fault(w, 701, "No Such Object")
			return
		}
		fields[0][1] = actual
	}
	response, e := b.call(r.Context(), ns, action.Name.Local, fields)
	if e != nil {
		var upstream *upstreamFault
		if errors.As(e, &upstream) {
			fault(w, upstream.code, upstream.description)
			return
		}
		fault(w, 501, "Music library temporarily unavailable")
		return
	}
	if action.Name.Local == "Browse" || action.Name.Local == "Search" {
		result := response.child("Result")
		if result == nil {
			fault(w, 501, "Missing upstream metadata")
			return
		}
		tree, e := parseDIDL([]byte(result.Text))
		if e != nil {
			fault(w, 501, "Invalid upstream metadata")
			return
		}
		artistNodes := []*node{}
		for _, n := range tree.Children {
			if n.Name.Local == "container" && (strings.Contains(n.text("class"), "musicArtist") || n.attr("parentID") == "0$=Artist" || action.text("ObjectID") == "artists") {
				artistNodes = append(artistNodes, n)
			}
			n.setAttr("id", mapped(n.attr("id")))
			n.setAttr("parentID", mapped(n.attr("parentID")))
			if ref := n.attr("refID"); ref != "" {
				n.setAttr("refID", mapped(ref))
			}
			b.publicMediaURLs(n)
		}
		if len(artistNodes) > 0 {
			b.decorateArtists(artistNodes)
		}
		result.Text = string(tree.encode())
	}
	if action.Name.Local == "GetSystemUpdateID" {
		if id := response.child("Id"); id != nil {
			id.Text = b.combinedUpdate(id.Text)
		}
	}
	out := [][2]string{}
	if id := response.child("UpdateID"); id != nil {
		id.Text = b.combinedUpdate(id.Text)
	}
	for _, n := range response.Children {
		out = append(out, [2]string{n.Name.Local, n.Text})
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	_, _ = w.Write(soap(ns, action.Name.Local+"Response", out))
}

// A loopback-only UPnP backend must not hand a phone/speaker localhost URLs.
// MinimServer's HTTP media listener remains on the LAN; only its discovery and
// SOAP interface are internal. Preserve paths, queries and protocol metadata.
func (b *Bridge) publicMediaURLs(n *node) {
	if n.Name.Local == "res" || n.Name.Local == "albumArtURI" || n.Name.Local == "icon" {
		if u, err := url.Parse(n.Text); err == nil && u.Scheme == "http" {
			if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() && (u.Port() == "9790" || u.Port() == "9791") {
				host, _, _ := net.SplitHostPort(b.cfg.Listen)
				u.Host = net.JoinHostPort(host, u.Port())
				n.Text = u.String()
			}
		}
	}
	for _, child := range n.Children {
		b.publicMediaURLs(child)
	}
}
func (b *Bridge) browseResponse(w http.ResponseWriter, tree *node, total int, update string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	_, _ = w.Write(soap(contentNS, "BrowseResponse", [][2]string{{"Result", string(tree.encode())}, {"NumberReturned", strconv.Itoa(len(tree.Children))}, {"TotalMatches", strconv.Itoa(total)}, {"UpdateID", b.combinedUpdate(update)}}))
}

func (b *Bridge) description() []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?><root xmlns="urn:schemas-upnp-org:device-1-0"><specVersion><major>1</major><minor>0</minor></specVersion><device><deviceType>urn:schemas-upnp-org:device:MediaServer:1</deviceType><friendlyName>` + escaped(b.cfg.Name) + `</friendlyName><manufacturer>HiFi Library</manufacturer><modelName>HiFi Browse Bridge</modelName><UDN>` + escaped(b.cfg.UDN) + `</UDN><iconList><icon><mimetype>image/png</mimetype><width>96</width><height>96</height><depth>32</depth><url>/icons/albums.png?v=` + iconRevision + `</url></icon></iconList><serviceList><service><serviceType>` + contentNS + `</serviceType><serviceId>urn:upnp-org:serviceId:ContentDirectory</serviceId><SCPDURL>/upnp/content.xml</SCPDURL><controlURL>/upnp/content/control</controlURL><eventSubURL>/upnp/content/event</eventSubURL></service><service><serviceType>` + connectionNS + `</serviceType><serviceId>urn:upnp-org:serviceId:ConnectionManager</serviceId><SCPDURL>/upnp/connection.xml</SCPDURL><controlURL>/upnp/connection/control</controlURL><eventSubURL>/upnp/connection/event</eventSubURL></service></serviceList></device></root>`)
}
