// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package oauthex implements extensions to OAuth2.

package oauthex

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/internal/json"
	"github.com/modelcontextprotocol/go-sdk/internal/util"
)

const maxDiscoveryRedirects = 10

var defaultDiscoveryTransport = newDiscoveryTransport(http.DefaultTransport)

type httpStatusError struct {
	StatusCode int
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("bad status %d", e.StatusCode)
}

// getJSON retrieves JSON and unmarshals JSON from the URL, as specified in both
// RFC 9728 and RFC 8414.
// It will not read more than limit bytes from the body.
func getJSON[T any](ctx context.Context, c *http.Client, url string, limit int64) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := newDiscoveryClient(c).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, &httpStatusError{StatusCode: res.StatusCode}
	}
	ct := res.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil || mediaType != "application/json" {
		return nil, fmt.Errorf("bad content type %q", ct)
	}

	var t T
	dec := json.NewDecoder(io.LimitReader(res.Body, limit))
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// checkURLScheme ensures that its argument is a valid URL with a scheme
// that prevents XSS attacks.
// See #526.
// Note: a copy of this function exists in auth/extauth/oidc_login.go; keep these in sync.
func checkURLScheme(u string) error {
	if u == "" {
		return nil
	}
	uu, err := url.Parse(u)
	if err != nil {
		return err
	}
	scheme := strings.ToLower(uu.Scheme)
	if scheme == "javascript" || scheme == "data" || scheme == "vbscript" {
		return fmt.Errorf("URL has disallowed scheme %q", scheme)
	}
	return nil
}

func checkHTTPSOrLoopback(addr string) error {
	if addr == "" {
		return nil
	}
	u, err := url.Parse(addr)
	if err != nil {
		return err
	}
	if !util.IsLoopback(u.Host) && u.Scheme != "https" {
		return fmt.Errorf("URL %q does not use HTTPS or is not a loopback address", addr)
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && util.IsPrivateOrReserved(ip) {
		return fmt.Errorf("URL %q refers to a non-public address", addr)
	}
	return nil
}

func newDiscoveryClient(c *http.Client) *http.Client {
	transport := defaultDiscoveryTransport
	if c != nil && c.Transport != nil {
		// a caller that has taken over dialing or TLS opts out of the checks
		if t, ok := c.Transport.(*http.Transport); ok && t.DialContext == nil && t.DialTLSContext == nil {
			transport = newDiscoveryTransport(t)
		} else {
			transport = c.Transport
		}
	}
	discoveryClient := &http.Client{
		Transport: transport,

		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if c != nil && c.CheckRedirect != nil {
				return c.CheckRedirect(req, via)
			}
			if len(via) >= maxDiscoveryRedirects {
				return fmt.Errorf("max redirects exceeded (%d)", maxDiscoveryRedirects)
			}
			if prev := via[len(via)-1]; prev.URL.Scheme == "https" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to non-HTTPS scheme %q is a security downgrade", req.URL.Scheme)
			}
			return checkLoopbackRedirect(req.Context(), req.URL.Hostname())
		},
	}
	if c != nil {
		discoveryClient.Timeout = c.Timeout
		discoveryClient.Jar = c.Jar
	}
	return discoveryClient
}

// newDiscoveryTransport creates an http.RoundTripper with extra protections enabled,
// unless the provided RoundTripper is not *http.Transport.
func newDiscoveryTransport(rt http.RoundTripper) http.RoundTripper {
	base, ok := rt.(*http.Transport)
	if !ok {
		return rt
	}
	// If a proxy is configured Control checks should be disabled, because they will
	// basically be running for proxy dials.
	if proxyConfigured(base) {
		return rt
	}
	result := base.Clone()
	result.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				host = address
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("non-ip address: %w", err)
			}
			if util.IsPrivateOrReserved(ip) {
				return fmt.Errorf("non-public ip address: %q", ip)
			}
			return nil
		},
	}).DialContext
	return result
}

func proxyConfigured(t *http.Transport) bool {
	if t.Proxy == nil {
		return false
	}
	req, err := http.NewRequest(http.MethodGet, "https://check-proxy-existence.com", nil)
	if err != nil {
		return false
	}
	u, err := t.Proxy(req)
	return err == nil && u != nil
}

func checkLoopbackRedirect(ctx context.Context, host string) error {
	doCheck := func(ip netip.Addr) error {
		if ip.Unmap().IsLoopback() {
			return fmt.Errorf("redirect into loopback address %q", host)
		}
		if util.IsPrivateOrReserved(ip) {
			return fmt.Errorf("redirect into non-public address %q", host)
		}
		return nil
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return doCheck(ip)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("cannot resolve redirect host %q: %w", host, err)
	}
	for _, ip := range ips {
		if err := doCheck(ip); err != nil {
			return err
		}
	}
	return nil
}
