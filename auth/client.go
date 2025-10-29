// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package auth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// An OAuthHandler conducts an OAuth flow and returns a [oauth2.TokenSource] if the authorization
// is approved, or an error if not.
type OAuthHandler func(context.Context, OAuthHandlerArgs) (oauth2.TokenSource, error)

// OAuthHandlerArgs are arguments to an [OAuthHandler].
type OAuthHandlerArgs struct {
	// The URL to fetch protected resource metadata, extracted from the WWW-Authenticate header.
	// Empty if not present or there was an error obtaining it.
	ResourceMetadataURL string
}

// HTTPTransport is an [http.RoundTripper] that follows the MCP
// OAuth protocol when it encounters a 401 Unauthorized response.
type HTTPTransport struct {
	handler OAuthHandler
	mu      sync.Mutex // protects opts.Base
	opts    HTTPTransportOptions
}

// NewHTTPTransport returns a new [*HTTPTransport].
// The handler is invoked when an HTTP request results in a 401 Unauthorized status.
// It is called only once per transport. Once a TokenSource is obtained, it is used
// for the lifetime of the transport; subsequent 401s are not processed.
func NewHTTPTransport(handler OAuthHandler, opts *HTTPTransportOptions) (*HTTPTransport, error) {
	if handler == nil {
		return nil, errors.New("handler cannot be nil")
	}
	t := &HTTPTransport{
		handler: handler,
	}
	if opts != nil {
		t.opts = *opts
	}
	if t.opts.Base == nil {
		t.opts.Base = http.DefaultTransport
	}
	return t, nil
}

// HTTPTransportOptions are options to [NewHTTPTransport].
type HTTPTransportOptions struct {
	// Base is the [http.RoundTripper] to use.
	// If nil, [http.DefaultTransport] is used.
	Base http.RoundTripper
}

func (t *HTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	base := t.opts.Base
	t.mu.Unlock()

	var (
		// If haveBody is set, the request has a nontrivial body, and we need avoid
		// reading (or closing) it multiple times. In that case, bodyBytes is its
		// content.
		haveBody  bool
		bodyBytes []byte
	)
	if req.Body != nil && req.Body != http.NoBody {
		// if we're setting Body, we must mutate first.
		req = req.Clone(req.Context())
		haveBody = true
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		// Now that we've read the request body, http.RoundTripper requires that we
		// close it.
		req.Body.Close() // ignore error
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	resp, err := base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	if _, ok := base.(*oauth2.Transport); ok {
		// We failed to authorize even with a token source; give up.
		return resp, nil
	}

	resp.Body.Close()
	// Try to authorize.
	t.mu.Lock()
	defer t.mu.Unlock()
	// If we don't have a token source, get one by following the OAuth flow.
	// (We may have obtained one while t.mu was not held above.)
	// TODO: We hold the lock for the entire OAuth flow. This could be a long
	// time. Is there a better way?
	if _, ok := t.opts.Base.(*oauth2.Transport); !ok {
		authHeaders := resp.Header[http.CanonicalHeaderKey("WWW-Authenticate")]
		ts, err := t.handler(req.Context(), OAuthHandlerArgs{
			ResourceMetadataURL: extractResourceMetadataURL(authHeaders),
		})
		if err != nil {
			return nil, err
		}
		t.opts.Base = &oauth2.Transport{Base: t.opts.Base, Source: ts}
	}

	// If we don't have a body, the request is reusable, though it will be cloned
	// by the base. However, if we've had to read the body, we must clone.
	if haveBody {
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	return t.opts.Base.RoundTrip(req)
}

func extractResourceMetadataURL(authHeaders []string) string {
	cs, err := oauthex.ParseWWWAuthenticate(authHeaders)
	if err != nil {
		return ""
	}
	return oauthex.ResourceMetadataURL(cs)
}
