// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package oauthex

import "testing"

func TestResourceMatches(t *testing.T) {
	const otterEndpoint = "https://mcp.otter.ai/mcp"
	tests := []struct {
		name     string
		expected string
		actual   string
		want     bool
	}{
		// The Otter case: an origin-form resource for an /mcp endpoint.
		{"origin with trailing slash", otterEndpoint, "https://mcp.otter.ai/", true},
		{"origin without trailing slash", otterEndpoint, "https://mcp.otter.ai", true},
		{"exact endpoint", otterEndpoint, otterEndpoint, true},

		{"parent path", "https://mcp.otter.ai/mcp/tools", "https://mcp.otter.ai/mcp", true},
		{"parent path with trailing slash", "https://mcp.otter.ai/mcp/tools", "https://mcp.otter.ai/mcp/", true},
		{"child path", otterEndpoint, "https://mcp.otter.ai/mcp/tools", false},
		{"sibling path", otterEndpoint, "https://mcp.otter.ai/other", false},
		{"segment boundary", "https://mcp.otter.ai/api", "https://mcp.otter.ai/api123", false},
		{"endpoint with trailing slash is not a parent", "https://h.example/mcp", "https://h.example/mcp/", false},

		{"different host", otterEndpoint, "https://evil.example/", false},
		{"host substring", otterEndpoint, "https://mcp.otter.ai.evil.example/", false},
		{"different scheme", otterEndpoint, "http://mcp.otter.ai/", false},
		{"different port", "https://mcp.otter.ai:8443/mcp", "https://mcp.otter.ai/", false},
		{"default https port normalized", "https://mcp.otter.ai:443/mcp", "https://mcp.otter.ai/", true},

		{"fragment in actual", otterEndpoint, "https://mcp.otter.ai/#frag", false},
		{"fragment in expected", "https://mcp.otter.ai/mcp#frag", "https://mcp.otter.ai/", false},
		{"bare hash", otterEndpoint, "https://mcp.otter.ai/#", false},

		{"query ignored", otterEndpoint, "https://mcp.otter.ai/?x=1", true},
		{"host case-insensitive", "https://MCP.Otter.AI/mcp", "https://mcp.otter.ai/", true},
		{"path case-sensitive", "https://mcp.otter.ai/MCP", "https://mcp.otter.ai/mcp", false},
		{"relative actual", otterEndpoint, "/mcp", false},
		{"empty actual", otterEndpoint, "", false},
		{"userinfo rejected", otterEndpoint, "https://user@mcp.otter.ai/", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resourceMatches(tt.expected, tt.actual); got != tt.want {
				t.Errorf("resourceMatches(%q, %q) = %v, want %v", tt.expected, tt.actual, got, tt.want)
			}
		})
	}
}
