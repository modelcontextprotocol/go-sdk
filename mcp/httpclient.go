// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// transportHTTPClient returns an HTTP client for MCP transports. When the
// caller does not set CheckRedirect, redirects are limited to the endpoint
// origin and must not change the HTTP method of the original request.
func transportHTTPClient(c *http.Client, base *url.URL) *http.Client {
	if c == nil {
		c = http.DefaultClient
	}
	userCheck := c.CheckRedirect
	out := *c
	out.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if userCheck != nil {
			return userCheck(req, via)
		}
		return checkTransportRedirect(base, req, via)
	}
	return &out
}

func checkTransportRedirect(base *url.URL, req *http.Request, via []*http.Request) error {
	if !sameOrigin(base, req.URL) {
		return fmt.Errorf("mcp: redirect to %q blocked: not same origin as endpoint %q", req.URL, base)
	}
	if len(via) > 0 && req.Method != via[0].Method {
		return fmt.Errorf("mcp: redirect changed HTTP method from %s to %s", via[0].Method, req.Method)
	}
	return nil
}

func sameOrigin(base, other *url.URL) bool {
	return strings.EqualFold(base.Scheme, other.Scheme) &&
		strings.EqualFold(base.Host, other.Host)
}
