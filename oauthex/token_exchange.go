// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file implements Token Exchange (RFC 8693) for Enterprise Managed Authorization.
// See https://datatracker.ietf.org/doc/html/rfc8693

//go:build mcp_go_client_oauth

package oauthex

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
)

// Token type identifiers defined by RFC 8693 and SEP-990.
const (
	// TokenTypeIDToken is the URN for OpenID Connect ID Tokens.
	TokenTypeIDToken = "urn:ietf:params:oauth:token-type:id_token"

	// TokenTypeSAML2 is the URN for SAML 2.0 assertions.
	TokenTypeSAML2 = "urn:ietf:params:oauth:token-type:saml2"

	// TokenTypeIDJAG is the URN for Identity Assertion JWT Authorization Grants.
	// This is the token type returned by IdP during token exchange for SEP-990.
	TokenTypeIDJAG = "urn:ietf:params:oauth:token-type:id-jag"

	// GrantTypeTokenExchange is the grant type for RFC 8693 token exchange.
	GrantTypeTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange"
)

// TokenExchangeRequest represents a Token Exchange request per RFC 8693.
// This is used for Enterprise Managed Authorization (SEP-990) where an MCP Client
// exchanges an ID Token from an enterprise IdP for an ID-JAG that can be used
// to obtain an access token from an MCP Server's authorization server.
type TokenExchangeRequest struct {
	// RequestedTokenType indicates the type of security token being requested.
	// For SEP-990, this MUST be TokenTypeIDJAG.
	RequestedTokenType string

	// Audience is the logical name of the target service where the client
	// intends to use the requested token. For SEP-990, this MUST be the
	// Issuer URL of the MCP Server's authorization server.
	Audience string

	// Resource is the physical location or identifier of the target resource.
	// For SEP-990, this MUST be the RFC9728 Resource Identifier of the MCP Server.
	Resource string

	// Scope is a list of space-separated scopes for the requested token.
	// This is OPTIONAL per RFC 8693 but commonly used in SEP-990.
	Scope []string

	// SubjectToken is the security token that represents the identity of the
	// party on behalf of whom the request is being made. For SEP-990, this is
	// typically an OpenID Connect ID Token.
	SubjectToken string

	// SubjectTokenType is the type of the security token in SubjectToken.
	// For SEP-990 with OIDC, this MUST be TokenTypeIDToken.
	SubjectTokenType string
}

// TokenExchangeResponse represents the response from a token exchange request
// per RFC 8693 Section 2.2.
type TokenExchangeResponse struct {
	// IssuedTokenType is the type of the security token in AccessToken.
	// For SEP-990, this MUST be TokenTypeIDJAG.
	IssuedTokenType string `json:"issued_token_type"`

	// AccessToken is the security token issued by the authorization server.
	// Despite the name "access_token" (required by RFC 8693), for SEP-990
	// this contains an ID-JAG JWT, not an OAuth access token.
	AccessToken string `json:"access_token"`

	// TokenType indicates the type of token returned. For SEP-990, this is "N_A"
	// because the issued token is not an OAuth access token.
	TokenType string `json:"token_type"`

	// Scope is the scope of the issued token, if the issued token scope is
	// different from the requested scope. Per RFC 8693, this SHOULD be included
	// if the scope differs from the request.
	Scope string `json:"scope,omitempty"`

	// ExpiresIn is the lifetime in seconds of the issued token.
	ExpiresIn int `json:"expires_in,omitempty"`
}

// ExchangeToken performs a token exchange request per RFC 8693 for Enterprise
// Managed Authorization (SEP-990). It exchanges an identity assertion (typically
// an ID Token) for an Identity Assertion JWT Authorization Grant (ID-JAG) that
// can be used to obtain an access token from an MCP Server.
//
// The tokenEndpoint parameter should be the IdP's token endpoint (typically
// obtained from the IdP's authorization server metadata).
//
// Client authentication must be performed by the caller by including appropriate
// credentials in the request (e.g., using Basic auth via the Authorization header,
// or including client_id and client_secret in the form data).
//
// Example:
//
//	req := &TokenExchangeRequest{
//		RequestedTokenType: TokenTypeIDJAG,
//		Audience:          "https://auth.mcpserver.example/",
//		Resource:          "https://mcp.mcpserver.example/",
//		Scope:             []string{"read", "write"},
//		SubjectToken:      idToken,
//		SubjectTokenType:  TokenTypeIDToken,
//	}
//
//	resp, err := ExchangeToken(ctx, idpTokenEndpoint, req, clientID, clientSecret, nil)
func ExchangeToken(
	ctx context.Context,
	tokenEndpoint string,
	req *TokenExchangeRequest,
	clientID string,
	clientSecret string,
	httpClient *http.Client,
) (*TokenExchangeResponse, error) {
	if tokenEndpoint == "" {
		return nil, fmt.Errorf("token endpoint is required")
	}
	if req == nil {
		return nil, fmt.Errorf("token exchange request is required")
	}

	// Validate required fields per SEP-990 Section 4
	if req.RequestedTokenType == "" {
		return nil, fmt.Errorf("requested_token_type is required")
	}
	if req.Audience == "" {
		return nil, fmt.Errorf("audience is required")
	}
	if req.Resource == "" {
		return nil, fmt.Errorf("resource is required")
	}
	if req.SubjectToken == "" {
		return nil, fmt.Errorf("subject_token is required")
	}
	if req.SubjectTokenType == "" {
		return nil, fmt.Errorf("subject_token_type is required")
	}

	// Validate URL schemes to prevent XSS attacks (see #526)
	if err := checkURLScheme(tokenEndpoint); err != nil {
		return nil, fmt.Errorf("invalid token endpoint: %w", err)
	}
	if err := checkURLScheme(req.Audience); err != nil {
		return nil, fmt.Errorf("invalid audience: %w", err)
	}
	if err := checkURLScheme(req.Resource); err != nil {
		return nil, fmt.Errorf("invalid resource: %w", err)
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

	// Build token exchange parameters per RFC 8693
	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("grant_type", GrantTypeTokenExchange),
		oauth2.SetAuthURLParam("requested_token_type", req.RequestedTokenType),
		oauth2.SetAuthURLParam("audience", req.Audience),
		oauth2.SetAuthURLParam("resource", req.Resource),
		oauth2.SetAuthURLParam("subject_token", req.SubjectToken),
		oauth2.SetAuthURLParam("subject_token_type", req.SubjectTokenType),
	}
	if len(req.Scope) > 0 {
		opts = append(opts, oauth2.SetAuthURLParam("scope", strings.Join(req.Scope, " ")))
	}

	// Exchange with token exchange grant type.
	// SetAuthURLParam overrides the default grant_type and adds all required parameters.
	token, err := cfg.Exchange(
		ctxWithClient,
		"", // empty code - per RFC 6749 Section 3.2, empty params should be ignored
		opts...,
	)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}

	// Extract issued_token_type from Token.Extra().
	// The oauth2 library stores additional response fields in Extra.
	issuedTokenType, _ := token.Extra("issued_token_type").(string)
	if issuedTokenType == "" {
		return nil, fmt.Errorf("response missing required field: issued_token_type")
	}

	// Build TokenExchangeResponse from oauth2.Token
	resp := &TokenExchangeResponse{
		IssuedTokenType: issuedTokenType,
		AccessToken:     token.AccessToken,
		TokenType:       token.TokenType,
	}

	// Extract optional fields from Extra
	if scope, ok := token.Extra("scope").(string); ok {
		resp.Scope = scope
	}

	// Calculate expires_in from token.Expiry if available
	if !token.Expiry.IsZero() {
		resp.ExpiresIn = int(token.Expiry.Sub(token.Expiry).Seconds()) // This would be 0
		// Actually get the raw expires_in if available
		if expiresIn, ok := token.Extra("expires_in").(float64); ok {
			resp.ExpiresIn = int(expiresIn)
		}
	}

	return resp, nil
}
