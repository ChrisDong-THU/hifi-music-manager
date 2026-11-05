package main

import (
	"errors"
	"net"
	"net/netip"
)

func accessNetwork(listen, network string) (netip.Prefix, error) {
	var lan netip.Prefix
	if network != "" {
		prefix, err := netip.ParsePrefix(network)
		if err != nil {
			return lan, errors.New("局域网范围必须是私有网段 CIDR")
		}
		prefix = prefix.Masked()
		for _, block := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"} {
			private := netip.MustParsePrefix(block)
			if private.Addr().BitLen() == prefix.Addr().BitLen() && private.Bits() <= prefix.Bits() && private.Contains(prefix.Addr()) {
				lan = prefix
				break
			}
		}
		if !lan.IsValid() {
			return lan, errors.New("仅支持私有局域网范围")
		}
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return lan, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || (!ip.IsLoopback() && (!lan.IsValid() || !lan.Contains(ip))) {
		return lan, errors.New("监听地址必须为回环地址，或显式允许的局域网 IP；不支持通配监听")
	}
	return lan, nil
}

func (a *app) allowedAddress(host string) bool {
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	return ip.IsLoopback() || (a.lan.IsValid() && a.lan.Contains(ip))
}
