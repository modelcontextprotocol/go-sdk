// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.
package util

import (
	"net/netip"
	"testing"
)

// TestIsLoopback tests the IsLoopback helper function.
func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"localhost", true},
		{"localhost:3000", true},
		{"127.0.0.1", true},
		{"127.0.0.1:3000", true},
		{"[::1]", true},
		{"[::1]:3000", true},
		{"::1", true},
		{"", false},
		{"evil.com", false},
		{"evil.com:80", false},
		{"localhost.evil.com", false},
		{"127.0.0.1.evil.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := IsLoopback(tt.addr); got != tt.want {
				t.Errorf("IsLoopback(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestIsPrivateOrReserved(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		// Private (RFC 1918) and IPv6 ULA (RFC 4193).
		{"10.0.0.1", true},
		{"172.16.5.4", true},
		{"192.168.1.1", true},
		{"fd00::1", true},
		// Link-local, including the cloud metadata endpoint.
		{"169.254.169.254", true},
		{"169.254.0.1", true},
		{"fe80::1", true},
		// Carrier-grade NAT (RFC 6598).
		{"100.64.0.1", true},
		{"100.127.255.255", true},
		// Multicast and unspecified.
		{"224.0.0.1", true},
		{"ff02::1", true},
		{"0.0.0.0", true},
		{"::", true},
		// IPv4-mapped IPv6 of a private address must also be caught.
		{"::ffff:10.0.0.1", true},
		{"::ffff:192.168.0.1", true},
		// Loopback is intentionally NOT reserved here (policy handled elsewhere).
		{"127.0.0.1", false},
		{"::1", false},
		// Public addresses are allowed.
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
		// Just outside CGNAT.
		{"100.63.255.255", false},
		{"100.128.0.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip, err := netip.ParseAddr(tt.ip)
			if err != nil {
				t.Fatalf("ParseAddr(%q): %v", tt.ip, err)
			}
			if got := IsPrivateOrReserved(ip); got != tt.want {
				t.Errorf("IsPrivateOrReserved(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}
