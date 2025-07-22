// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/internal/oauthex"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/authhandler"
)

// newAuthClient returns a shallow copy of c with its tranport replaced by one that
// authorizes with the token source.
func newAuthClient(c *http.Client, ts oauth2.TokenSource) *http.Client {
	c2 := *c
	c2.Transport = &oauth2.Transport{
		Base:   c.Transport,
		Source: ts,
	}
	return &c2
}

// doOauth runs the OAuth 2.1 flow for MCP as described in
// https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization.
// It returns the resulting TokenSource.
func doOauth(ctx context.Context, header http.Header, c *http.Client, oauthHandler authhandler.AuthorizationHandler) (oauth2.TokenSource, error) {
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
