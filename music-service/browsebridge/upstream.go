package browsebridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type upstreamFault struct {
	code        int
	description string
}

// A description fetched on loopback may advertise this device's LAN address.
// Normalize that one known alias back to loopback; never follow another peer,
// port, credential or redirect supplied by the description.
func (b *Bridge) localUpstreamURL(value, origin *url.URL) bool {
	if value.Scheme != "http" || value.User != nil || value.Fragment != "" {
		return false
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		return "80"
	}
	lan, _, _ := net.SplitHostPort(b.cfg.Listen)
	if port(value) != port(origin) || (value.Hostname() != origin.Hostname() && value.Hostname() != lan) {
		return false
	}
	value.Host = origin.Host
	return true
}

func (e *upstreamFault) Error() string { return e.description }

func (b *Bridge) discover(ctx context.Context) (upstream, error) {
	if err := b.backendGate.lock(ctx); err != nil {
		return upstream{}, err
	}
	defer b.backendGate.unlock()
	if time.Now().Before(b.backendUntil) {
		return b.backend, nil
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", b.cfg.BackendDescription, nil)
	resp, err := b.client.Do(req)
	if err != nil {
		return upstream{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return upstream{}, errors.New("MinimServer offline")
	}
	data, err := boundedRead(resp.Body)
	if err != nil {
		return upstream{}, err
	}
	desc, err := parse(data)
	if err != nil {
		return upstream{}, err
	}
	if desc.Name.Local != "root" || desc.Name.Space != deviceNS {
		return upstream{}, errors.New("invalid upstream device description")
	}
	device := desc.child("device")
	if device == nil || device.child("serviceList") == nil {
		return upstream{}, errors.New("missing upstream services")
	}
	base, _ := url.Parse(b.cfg.BackendDescription)
	origin := *base
	if value := desc.text("URLBase"); value != "" {
		ref, err := url.Parse(value)
		if err != nil {
			return upstream{}, err
		}
		resolved := base.ResolveReference(ref)
		if !b.localUpstreamURL(resolved, &origin) {
			return upstream{}, errors.New("upstream URLBase changed origin")
		}
		base = resolved
	}
	resolve := func(v string) string {
		if v == "" {
			return ""
		}
		ref, e := url.Parse(v)
		if e != nil {
			return ""
		}
		u := base.ResolveReference(ref)
		if !b.localUpstreamURL(u, &origin) {
			return ""
		}
		return u.String()
	}
	u := upstream{}
	for _, s := range device.child("serviceList").Children {
		if s.Name.Local != "service" || s.Name.Space != deviceNS {
			continue
		}
		switch s.text("serviceType") {
		case contentNS:
			u.Content = resolve(s.text("controlURL"))
			u.ContentSCPD = resolve(s.text("SCPDURL"))
		case connectionNS:
			u.Connection = resolve(s.text("controlURL"))
			u.ConnectionSCPD = resolve(s.text("SCPDURL"))
		}
	}
	if u.Content == "" || u.Connection == "" || u.ContentSCPD == "" || u.ConnectionSCPD == "" {
		return upstream{}, errors.New("missing upstream services")
	}
	b.backend = u
	b.backendUntil = time.Now().Add(30 * time.Second)
	return u, nil
}
func (b *Bridge) call(ctx context.Context, ns, action string, fields [][2]string) (*node, error) {
	u, err := b.discover(ctx)
	if err != nil {
		return nil, err
	}
	target := u.Content
	if ns == connectionNS {
		target = u.Connection
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(soap(ns, action, fields)))
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `"`+ns+`#`+action+`"`)
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := boundedRead(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		if response, err := soapBody(data); err == nil && response.Name.Space == soapNS && response.Name.Local == "Fault" {
			if detail := response.child("detail"); detail != nil {
				if failure := detail.child("UPnPError"); failure != nil && failure.Name.Space == "urn:schemas-upnp-org:control-1-0" {
					if code, err := strconv.Atoi(failure.text("errorCode")); err == nil && code >= 400 && code <= 899 {
						return nil, &upstreamFault{code: code, description: failure.text("errorDescription")}
					}
				}
			}
		}
		return nil, fmt.Errorf("upstream SOAP status %d", resp.StatusCode)
	}
	response, err := soapBody(data)
	if err != nil {
		return nil, err
	}
	if response.Name.Space != ns || response.Name.Local != action+"Response" {
		return nil, errors.New("unexpected upstream SOAP action")
	}
	return response, nil
}
func (b *Bridge) browseNative(ctx context.Context, id string) (*node, string, error) {
	n, err := b.call(ctx, contentNS, "Browse", [][2]string{{"ObjectID", id}, {"BrowseFlag", "BrowseDirectChildren"}, {"Filter", "*"}, {"StartingIndex", "0"}, {"RequestedCount", "0"}, {"SortCriteria", ""}})
	if err != nil {
		return nil, "", err
	}
	tree, err := parseDIDL([]byte(n.text("Result")))
	return tree, n.text("UpdateID"), err
}

func (b *Bridge) readSCPD(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return nil, err
	}
	response, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("upstream service description unavailable")
	}
	data, err := boundedRead(response.Body)
	if err != nil {
		return nil, err
	}
	root, err := parse(data)
	if err != nil {
		return nil, err
	}
	if root.Name.Local != "scpd" || root.Name.Space != "urn:schemas-upnp-org:service-1-0" {
		return nil, errors.New("invalid upstream service description")
	}
	return data, nil
}
