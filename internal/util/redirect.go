// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package util

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/internal/mcpgodebug"
)

// followallredirects is a compatibility parameter that restores the previous
// behavior of following every redirect that net/http follows by default.
// See the documentation for the mcpgodebug package for instructions how to enable it.
// The option will be removed in the 1.11.0 version of the SDK.
//
// Mutable for testing.
var followallredirects = mcpgodebug.Value("followallredirects")

// maxRedirects is the limit that net/http applies when CheckRedirect is nil.
const maxRedirects = 10

// RestrictRedirects returns a copy of c, or of [http.DefaultClient] if c is
// nil, that stops after 10 requests, as net/http does by default, and follows
// only redirects that stay within the origin of the request they redirect and
// keep the method of the original request. Otherwise a server could reroute a request,
// with its headers and body, to another origin, or turn a POST into a
// bodyless GET. An upgrade from http to https on the same host, with default
// ports, stays within the origin.
//
// It returns c (or [http.DefaultClient]) unchanged if c sets its own
// CheckRedirect, which then decides alone, or if the followallredirects
// MCPGODEBUG option is set.
func RestrictRedirects(c *http.Client) *http.Client {
	if c == nil {
		c = http.DefaultClient
	}
	if c.CheckRedirect != nil || followallredirects == "1" {
		return c
	}
	restricted := *c
	restricted.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		return checkRedirectWithinOrigin(req, via)
	}
	return &restricted
}

func checkRedirectWithinOrigin(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if prev := via[len(via)-1].URL; !withinOrigin(prev, req.URL) {
		return fmt.Errorf("redirect not followed: target is outside the origin %s://%s of the request", prev.Scheme, prev.Host)
	}
	if req.Method != via[0].Method {
		return fmt.Errorf("redirect not followed: it changes the method from %s to %s", via[0].Method, req.Method)
	}
	if req.URL.User != nil && req.URL.User.String() != via[len(via)-1].URL.User.String() {
		return fmt.Errorf("redirect not followed: target adds or changes userinfo")
	}
	return nil
}

func withinOrigin(from, to *url.URL) bool {
	if !strings.EqualFold(from.Hostname(), to.Hostname()) {
		return false
	}
	fromPort, toPort := portOrDefault(from), portOrDefault(to)
	if from.Scheme == to.Scheme && fromPort == toPort {
		return true
	}
	return from.Scheme == "http" && fromPort == "80" && to.Scheme == "https" && toPort == "443"
}

func portOrDefault(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch u.Scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}
