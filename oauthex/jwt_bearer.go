// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file implements JWT Bearer Authorization Grant (RFC 7523) for Enterprise Managed Authorization.
// See https://datatracker.ietf.org/doc/html/rfc7523

//go:build mcp_go_client_oauth

package oauthex

import (
	"context"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
)

// GrantTypeJWTBearer is the grant type for RFC 7523 JWT Bearer authorization grant.
// This is used in SEP-990 to exchange an ID-JAG for an access token at the MCP Server.
const GrantTypeJWTBearer = "urn:ietf:params:oauth:grant-type:jwt-bearer"

// ExchangeJWTBearer exchanges an Identity Assertion JWT Authorization Grant (ID-JAG)
// for an access token using JWT Bearer Grant per RFC 7523. This is the second step
// in Enterprise Managed Authorization (SEP-990) after obtaining the ID-JAG from the
// IdP via token exchange.
//
// The tokenEndpoint parameter should be the MCP Server's token endpoint (typically
// obtained from the MCP Server's authorization server metadata).
//
// The assertion parameter should be the ID-JAG JWT obtained from the token exchange
// step with the enterprise IdP.
//
// Client authentication must be performed by the caller by including appropriate
// credentials in the request (e.g., using Basic auth via the Authorization header,
// or including client_id and client_secret in the form data).
//
// Example:
//
//	// First, get ID-JAG via token exchange
//	idJAG := tokenExchangeResp.AccessToken
//
//	// Then exchange ID-JAG for access token
//	token, err := ExchangeJWTBearer(
//		ctx,
//		"https://auth.mcpserver.example/oauth2/token",
//		idJAG,
//		"mcp-client-id",
//		"mcp-client-secret",
//		nil,
//	)
func ExchangeJWTBearer(
	ctx context.Context,
	tokenEndpoint string,
	assertion string,
	clientID string,
	clientSecret string,
	httpClient *http.Client,
) (*oauth2.Token, error) {
	if tokenEndpoint == "" {
		return nil, fmt.Errorf("token endpoint is required")
	}
	if assertion == "" {
		return nil, fmt.Errorf("assertion is required")
	}
	// Validate URL scheme to prevent XSS attacks (see #526)
	if err := CheckURLScheme(tokenEndpoint); err != nil {
		return nil, fmt.Errorf("invalid token endpoint: %w", err)
	}

	// Per RFC 6749 Section 3.2, parameters sent without a value (like the empty
	// "code" parameter) MUST be treated as if they were omitted from the request.
	// The oauth2 library's Exchange method sends an empty code, but compliant
	// servers should ignore it.
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL:  tokenEndpoint,
			AuthStyle: oauth2.AuthStyleInParams, // Use POST body auth per SEP-990
		},
	}

	// Use custom HTTP client if provided
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	ctxWithClient := context.WithValue(ctx, oauth2.HTTPClient, httpClient)

	// Exchange with JWT Bearer grant type and assertion.
	// SetAuthURLParam overrides the default grant_type and adds the assertion parameter.
	token, err := cfg.Exchange(
		ctxWithClient,
		"", // empty code - per RFC 6749 Section 3.2, empty params should be ignored
		oauth2.SetAuthURLParam("grant_type", GrantTypeJWTBearer),
		oauth2.SetAuthURLParam("assertion", assertion),
	)
	if err != nil {
		return nil, fmt.Errorf("JWT bearer grant request failed: %w", err)
	}

	return token, nil
}
