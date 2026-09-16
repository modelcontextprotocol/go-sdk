// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.
package util

import (
	"net"
	"net/netip"
	"strings"
)

func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// If SplitHostPort fails, it might be just a host without a port.
		host = strings.Trim(addr, "[]")
	}
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return ip.IsLoopback()
}

// cgnat is the RFC 6598 carrier-grade NAT range (100.64.0.0/10). netip's
// IsPrivate does not report it, but it is shared ISP space that is not publicly
// routable and could point at another tenant behind the same NAT.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// IsPrivateOrReserved reports whether ip belongs to a range that must not be
// reachable through a server-controlled URL, as a defense against SSRF: private
// (RFC 1918 / RFC 4193 ULA), link-local (including the 169.254.169.254 cloud
// metadata endpoint), carrier-grade NAT, multicast, and the unspecified address.
func IsPrivateOrReserved(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return true
	}
	if ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return true
	}
	return cgnat.Contains(ip)
}
