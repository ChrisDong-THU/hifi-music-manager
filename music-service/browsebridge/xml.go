// Package browsebridge presents a small, predictable UPnP browse tree while
// leaving scanning, tags and media delivery with the existing MinimServer.
package browsebridge

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"sort"
	"strings"
)

const (
	didlNS       = "urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"
	dcNS         = "http://purl.org/dc/elements/1.1/"
	upnpNS       = "urn:schemas-upnp-org:metadata-1-0/upnp/"
	contentNS    = "urn:schemas-upnp-org:service:ContentDirectory:1"
	connectionNS = "urn:schemas-upnp-org:service:ConnectionManager:1"
	soapNS       = "http://schemas.xmlsoap.org/soap/envelope/"
	deviceNS     = "urn:schemas-upnp-org:device-1-0"
)

type node struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Text     string
	Children []*node
}

func (n *node) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	count := 0
	return n.readXML(d, start, 0, &count)
}

func (n *node) readXML(d *xml.Decoder, start xml.StartElement, depth int, count *int) error {
	*count += 1
	if depth >= 128 || *count > 100000 {
		return errors.New("XML structure too complex")
	}
	n.Name, n.Attrs = start.Name, start.Attr
	var text strings.Builder
	for {
		t, err := d.Token()
		if err != nil {
			return err
		}
		switch t := t.(type) {
		case xml.StartElement:
			child := new(node)
			if err := child.readXML(d, t, depth+1, count); err != nil {
				return err
			}
			n.Children = append(n.Children, child)
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			n.Text = text.String()
			return nil
		}
	}
}
func parse(data []byte) (*node, error) {
	if len(data) > 8<<20 {
		return nil, errors.New("XML document too large")
	}
	n := new(node)
	d := xml.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(n); err != nil {
		return nil, err
	}
	for {
		token, err := d.Token()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return nil, err
		}
		switch token := token.(type) {
		case xml.Comment, xml.ProcInst:
			continue
		case xml.CharData:
			if strings.TrimSpace(string(token)) == "" {
				continue
			}
		}
		return nil, errors.New("unexpected content after XML root")
	}
}

func parseDIDL(data []byte) (*node, error) {
	tree, err := parse(data)
	if err != nil {
		return nil, err
	}
	if tree.Name.Space != didlNS || tree.Name.Local != "DIDL-Lite" {
		return nil, errors.New("invalid DIDL metadata root")
	}
	return tree, nil
}
func (n *node) child(name string) *node {
	for _, c := range n.Children {
		if c.Name.Local == name {
			return c
		}
	}
	return nil
}
func (n *node) text(name string) string {
	if c := n.child(name); c != nil {
		return c.Text
	}
	return ""
}
func (n *node) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name && a.Name.Space == "" {
			return a.Value
		}
	}
	return ""
}
func (n *node) setAttr(name, value string) {
	for i := range n.Attrs {
		if n.Attrs[i].Name.Local == name && n.Attrs[i].Name.Space == "" {
			n.Attrs[i].Value = value
			return
		}
	}
	n.Attrs = append(n.Attrs, xml.Attr{Name: xml.Name{Local: name}, Value: value})
}
func element(space, name, text string) *node {
	return &node{Name: xml.Name{Space: space, Local: name}, Text: text}
}
func escaped(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// Preserve namespace meaning for all upstream metadata, including protocolInfo,
// custom DLNA attributes, cover URLs and non-ASCII/multi-valued artist tags.
func (n *node) encode() []byte {
	prefixes := map[string]string{didlNS: "", dcNS: "dc", upnpNS: "upnp", "urn:schemas-dlna-org:metadata-1-0/": "dlna", "http://www.w3.org/XML/1998/namespace": "xml"}
	var collect func(*node)
	add := func(ns string) {
		if ns != "" && ns != "xmlns" && ns != "http://www.w3.org/2000/xmlns/" {
			if _, ok := prefixes[ns]; !ok {
				prefixes[ns] = "ns" + strings.Repeat("x", len(prefixes))
			}
		}
	}
	collect = func(n *node) {
		add(n.Name.Space)
		for _, a := range n.Attrs {
			add(a.Name.Space)
		}
		for _, c := range n.Children {
			collect(c)
		}
	}
	collect(n)
	name := func(v xml.Name) string {
		if v.Space == "" || prefixes[v.Space] == "" {
			return v.Local
		}
		return prefixes[v.Space] + ":" + v.Local
	}
	var b bytes.Buffer
	var write func(*node, bool)
	write = func(n *node, root bool) {
		b.WriteString("<" + name(n.Name))
		if root {
			keys := []string{}
			for ns := range prefixes {
				keys = append(keys, ns)
			}
			sort.Strings(keys)
			for _, ns := range keys {
				prefix := prefixes[ns]
				if prefix == "xml" {
					continue
				}
				attr := "xmlns"
				if prefix != "" {
					attr += ":" + prefix
				}
				b.WriteString(" " + attr + "=\"" + escaped(ns) + "\"")
			}
		}
		for _, a := range n.Attrs {
			if a.Name.Space == "xmlns" || a.Name.Space == "http://www.w3.org/2000/xmlns/" || a.Name.Local == "xmlns" {
				continue
			}
			b.WriteString(" " + name(a.Name) + "=\"" + escaped(a.Value) + "\"")
		}
		b.WriteString(">")
		b.WriteString(escaped(n.Text))
		for _, c := range n.Children {
			write(c, false)
		}
		b.WriteString("</" + name(n.Name) + ">")
	}
	write(n, true)
	return b.Bytes()
}
func soap(ns, action string, values [][2]string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:` + action + ` xmlns:u="` + ns + `">`)
	for _, v := range values {
		b.WriteString("<" + v[0] + ">" + escaped(v[1]) + "</" + v[0] + ">")
	}
	b.WriteString("</u:" + action + "></s:Body></s:Envelope>")
	return []byte(b.String())
}
func soapBody(data []byte) (*node, error) {
	root, err := parse(data)
	if err != nil {
		return nil, err
	}
	if root.Name.Space != soapNS || root.Name.Local != "Envelope" {
		return nil, errors.New("invalid SOAP envelope")
	}
	body := root.child("Body")
	if body == nil || body.Name.Space != soapNS || len(body.Children) != 1 {
		return nil, errors.New("invalid SOAP body")
	}
	return body.Children[0], nil
}
func boundedRead(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, (8<<20)+1))
	if len(b) > 8<<20 {
		return nil, errors.New("UPnP response too large")
	}
	return b, err
}
