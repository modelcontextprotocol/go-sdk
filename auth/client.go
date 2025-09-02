// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package auth

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/authhandler"
)

// HTTPTransport is an [http.RoundTripper] that follows the MCP
// OAuth protocol when it encounters a 401 Unauthorized response.
type HTTPTransport struct {
	mu   sync.Mutex
	opts HTTPTransportConfig
}

func NewHTTPTransport(opts *HTTPTransportConfig) (*HTTPTransport, error) {
	t := &HTTPTransport{}
	if opts != nil {
		t.opts = *opts
	}
	if t.opts.Base == nil {
		t.opts.Base = http.DefaultTransport
	}
	if t.opts.OAuthClient == nil {
		t.opts.OAuthClient = http.DefaultClient
	}
	return t, nil
}

type HTTPTransportConfig struct {
	AuthHandler authhandler.AuthorizationHandler
	// Base is the [http.RoundTripper] to use initially, before credentials are obtained.
	// (After the OAuth flow is completed, an [oauth2.Transport] with the resulting
	// [oauth2.TokenSource] is used.)
	// If nil, [http.DefaultTransport] is used.
	Base http.RoundTripper
	// OAuth is used for HTTP requests that are part of the OAuth protocol,
	// such as requests to the authorization server. If nil, http.DefaultClient
	// is used.
	OAuthClient *http.Client
}

func (t *HTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// baseRoundTrip calls RoundTrip on the base transport.
	// If we should do OAuth to fix a 401 Unauthorized, it returns nil, nil.
	baseRoundTrip := func() (*http.Response, error) {
		t.mu.Lock()
		base := t.opts.Base
		_, haveTokenSource := base.(*oauth2.Transport)
		t.mu.Unlock()

		resp, err := base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusUnauthorized {
			return resp, nil
		}
		if haveTokenSource {
			// We failed to authorize even with a token source; give up.
			return resp, nil
		}
		return nil, nil
	}

	resp, err := baseRoundTrip()
	if resp != nil || err != nil {
		return resp, err
	}

	// Try to authorize.
	t.mu.Lock()
	// If we don't have a token source, get one by following the OAuth flow.
	// (We may have obtained one while t.mu was not held above.)
	if _, ok := t.opts.Base.(*oauth2.Transport); !ok {
		ts, err := t.doOauth(req.Context(), resp.Header)
		if err != nil {
			t.mu.Unlock()
			return nil, err
		}
		t.opts.Base = &oauth2.Transport{Base: t.opts.Base, Source: ts}
	}
	t.mu.Unlock()
	// This will not return (nil, nil), because once we have a TokenSource we never lose it.
	return baseRoundTrip()
}

// doOauth runs the OAuth 2.1 flow for MCP as described in
// https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization.
// It returns the resulting TokenSource.
func (t *HTTPTransport) doOauth(ctx context.Context, header http.Header) (oauth2.TokenSource, error) {
	prm, err := oauthex.GetProtectedResourceMetadataFromHeader(ctx, header, c)
	if err != nil {
		return nil, err
	}
	if len(prm.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("resource %s provided no authorization servers", prm.Resource)
	}
	// TODO: try more than one?
	authServer := prm.AuthorizationServers[0]
	// TODO: which scopes to ask for? All of them?
	scopes := prm.ScopesSupported
	asm, err := oauthex.GetAuthServerMeta(ctx, authServer, c)
	if err != nil {
		return nil, err
	}
	// TODO: register the client with the auth server if not registered yet,
	// or find another way to get the client ID and secret.

	// Get an access token from the auth server.
	config := &oauth2.Config{
		ClientID:     "TODO: from registration",
		ClientSecret: "TODO: from registration",
		Endpoint: oauth2.Endpoint{
			AuthURL:  asm.AuthorizationEndpoint,
			TokenURL: asm.TokenEndpoint,
			// DeviceAuthURL: "",
			// AuthStyle: "from auth meta?",
		},
		RedirectURL: "", // ???
		Scopes:      scopes,
	}
	v := oauth2.GenerateVerifier()
	pkceParams := authhandler.PKCEParams{
		ChallengeMethod: "S256",
		Challenge:       oauth2.S256ChallengeFromVerifier(v),
		Verifier:        v,
	}
	state := randText()
	return authhandler.TokenSourceWithPKCE(ctx, config, state, oauthHandler, &pkceParams), nil
}
