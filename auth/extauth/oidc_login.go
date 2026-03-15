// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file implements OIDC Authorization Code flow for obtaining ID tokens
// as part of Enterprise Managed Authorization (SEP-990).
// See https://openid.net/specs/openid-connect-core-1_0.html

//go:build mcp_go_client_oauth

package extauth

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// OIDCLoginConfig configures the OIDC Authorization Code flow for obtaining
// an ID Token. This is an OPTIONAL step before calling EnterpriseAuthFlow.
// Users can alternatively obtain ID tokens through their own methods.
type OIDCLoginConfig struct {
	// IssuerURL is the IdP's issuer URL (e.g., "https://acme.okta.com").
	// REQUIRED.
	IssuerURL string
	// ClientID is the MCP Client's ID registered at the IdP.
	// REQUIRED.
	ClientID string
	// ClientSecret is the MCP Client's secret at the IdP.
	// OPTIONAL. Only required if the client is confidential.
	ClientSecret string
	// RedirectURL is the OAuth2 redirect URI registered with the IdP.
	// This must match exactly what was registered with the IdP.
	// REQUIRED.
	RedirectURL string
	// Scopes are the OAuth2/OIDC scopes to request.
	// "openid" is REQUIRED for OIDC. Common values: ["openid", "profile", "email"]
	// REQUIRED.
	Scopes []string
	// LoginHint is a hint to the IdP about the user's identity.
	// Some IdPs may require this (e.g., as an email address for routing to SSO providers).
	// Example: "user@example.com"
	// OPTIONAL.
	LoginHint string
	// HTTPClient is the HTTP client for making requests.
	// If nil, http.DefaultClient is used.
	// OPTIONAL.
	HTTPClient *http.Client
}

// OIDCAuthorizationRequest represents the result of initiating an OIDC
// authorization code flow. Users must direct the end-user to AuthURL
// to complete authentication.
type OIDCAuthorizationRequest struct {
	// AuthURL is the URL the user should visit to authenticate.
	// This URL includes the authorization request parameters.
	AuthURL string
	// State is the OAuth2 state parameter for CSRF protection.
	// Users MUST validate that the state returned from the IdP matches this value.
	State string
	// CodeVerifier is the PKCE code verifier for secure authorization code exchange.
	// This must be provided to CompleteOIDCLogin along with the authorization code.
	CodeVerifier string
}

// OIDCTokenResponse contains the tokens returned from a successful OIDC login.
type OIDCTokenResponse struct {
	// IDToken is the OpenID Connect ID Token (JWT).
	// This can be passed to EnterpriseAuthFlow for token exchange.
	IDToken string
	// AccessToken is the OAuth2 access token (if issued by IdP).
	// This is typically not needed for SEP-990, but may be useful for other IdP APIs.
	AccessToken string
	// RefreshToken is the OAuth2 refresh token (if issued by IdP).
	RefreshToken string
	// TokenType is the token type (typically "Bearer").
	TokenType string
	// ExpiresAt is when the ID token expires.
	ExpiresAt int64
}

// InitiateOIDCLogin initiates an OIDC Authorization Code flow with PKCE.
// This is the first step for users who want to use SSO to obtain an ID token.
//
// The returned AuthURL should be presented to the user (e.g., opened in a browser).
// After the user authenticates, the IdP will redirect to the RedirectURL with
// an authorization code and state parameter.
//
// Example:
//
//	config := &OIDCLoginConfig{
//		IssuerURL:   "https://acme.okta.com",
//		ClientID:    "client-id",
//		RedirectURL: "http://localhost:8080/callback",
//		Scopes:      []string{"openid", "profile", "email"},
//	}
//
//	authReq, err := InitiateOIDCLogin(ctx, config)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	// Direct user to authReq.AuthURL
//	fmt.Printf("Visit this URL to login: %s\n", authReq.AuthURL)
//
//	// After user completes login, IdP redirects to RedirectURL with code & state
//	// Extract code and state from the redirect, then call CompleteOIDCLogin
func InitiateOIDCLogin(
	ctx context.Context,
	config *OIDCLoginConfig,
) (*OIDCAuthorizationRequest, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}
	// Validate required fields
	if config.IssuerURL == "" {
		return nil, fmt.Errorf("IssuerURL is required")
	}
	if config.ClientID == "" {
		return nil, fmt.Errorf("ClientID is required")
	}
	if config.RedirectURL == "" {
		return nil, fmt.Errorf("RedirectURL is required")
	}
	if len(config.Scopes) == 0 {
		return nil, fmt.Errorf("Scopes is required (must include 'openid')")
	}
	// Validate that "openid" scope is present (required for OIDC)
	hasOpenID := false
	for _, scope := range config.Scopes {
		if scope == "openid" {
			hasOpenID = true
			break
		}
	}
	if !hasOpenID {
		return nil, fmt.Errorf("Scopes must include 'openid' for OIDC")
	}
	// Validate URL schemes to prevent XSS attacks
	if err := oauthex.CheckURLScheme(config.IssuerURL); err != nil {
		return nil, fmt.Errorf("invalid IssuerURL: %w", err)
	}
	if err := oauthex.CheckURLScheme(config.RedirectURL); err != nil {
		return nil, fmt.Errorf("invalid RedirectURL: %w", err)
	}
	// Discover OIDC endpoints via .well-known
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	meta, err := auth.GetAuthServerMetadataForIssuer(ctx, config.IssuerURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OIDC metadata: %w", err)
	}
	if meta.AuthorizationEndpoint == "" {
		return nil, fmt.Errorf("authorization_endpoint not found in OIDC metadata")
	}

	// Generate PKCE code verifier (RFC 7636)
	codeVerifier := oauth2.GenerateVerifier()

	// Generate state for CSRF protection (RFC 6749 Section 10.12)
	state := rand.Text()

	// Build oauth2.Config to use standard library's AuthCodeURL.
	oauth2Config := &oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		RedirectURL:  config.RedirectURL,
		Scopes:       config.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  meta.AuthorizationEndpoint,
			TokenURL: meta.TokenEndpoint,
		},
	}

	// Build authorization URL using oauth2.Config.AuthCodeURL with PKCE.
	// S256ChallengeOption automatically computes the S256 challenge from the verifier.
	authURLOpts := []oauth2.AuthCodeOption{
		oauth2.S256ChallengeOption(codeVerifier),
	}
	if config.LoginHint != "" {
		authURLOpts = append(authURLOpts, oauth2.SetAuthURLParam("login_hint", config.LoginHint))
	}
	authURL := oauth2Config.AuthCodeURL(state, authURLOpts...)

	return &OIDCAuthorizationRequest{
		AuthURL:      authURL,
		State:        state,
		CodeVerifier: codeVerifier,
	}, nil
}

// CompleteOIDCLogin completes the OIDC Authorization Code flow by exchanging
// the authorization code for tokens. This is the second step after the user
// has authenticated and been redirected back to the application.
//
// The authCode and returnedState parameters should come from the redirect URL
// query parameters. The state MUST match the state from InitiateOIDCLogin
// for CSRF protection.
//
// Example:
//
//	// In your redirect handler (e.g., http://localhost:8080/callback)
//	authCode := r.URL.Query().Get("code")
//	returnedState := r.URL.Query().Get("state")
//
//	// Validate state matches what we sent
//	if returnedState != authReq.State {
//		log.Fatal("State mismatch - possible CSRF attack")
//	}
//
//	// Exchange code for tokens
//	tokens, err := CompleteOIDCLogin(ctx, config, authCode, authReq.CodeVerifier)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	// Now use tokens.IDToken with EnterpriseAuthFlow
//	accessToken, err := EnterpriseAuthFlow(ctx, enterpriseConfig, tokens.IDToken)
func CompleteOIDCLogin(
	ctx context.Context,
	config *OIDCLoginConfig,
	authCode string,
	codeVerifier string,
) (*OIDCTokenResponse, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}
	if authCode == "" {
		return nil, fmt.Errorf("authCode is required")
	}
	if codeVerifier == "" {
		return nil, fmt.Errorf("codeVerifier is required")
	}
	// Validate required fields
	if config.IssuerURL == "" {
		return nil, fmt.Errorf("IssuerURL is required")
	}
	if config.ClientID == "" {
		return nil, fmt.Errorf("ClientID is required")
	}
	if config.RedirectURL == "" {
		return nil, fmt.Errorf("RedirectURL is required")
	}
	// Discover token endpoint
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	meta, err := auth.GetAuthServerMetadataForIssuer(ctx, config.IssuerURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OIDC metadata: %w", err)
	}
	if meta.TokenEndpoint == "" {
		return nil, fmt.Errorf("token_endpoint not found in OIDC metadata")
	}

	// Build oauth2.Config for token exchange.
	oauth2Config := &oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		RedirectURL:  config.RedirectURL,
		Scopes:       config.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  meta.AuthorizationEndpoint,
			TokenURL: meta.TokenEndpoint,
		},
	}

	// Use custom HTTP client if provided
	ctxWithClient := context.WithValue(ctx, oauth2.HTTPClient, httpClient)

	// Exchange authorization code for tokens using oauth2.Config.Exchange.
	// VerifierOption provides the PKCE code_verifier for the token request.
	oauth2Token, err := oauth2Config.Exchange(
		ctxWithClient,
		authCode,
		oauth2.VerifierOption(codeVerifier),
	)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	// Extract ID Token from response.
	// oauth2.Token.Extra() provides access to additional fields like id_token.
	idToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok || idToken == "" {
		return nil, fmt.Errorf("id_token not found in token response")
	}
	return &OIDCTokenResponse{
		IDToken:      idToken,
		AccessToken:  oauth2Token.AccessToken,
		RefreshToken: oauth2Token.RefreshToken,
		TokenType:    oauth2Token.TokenType,
		ExpiresAt:    oauth2Token.Expiry.Unix(),
	}, nil
}

// OIDCAuthorizationResult contains the authorization code and state returned
// from the IdP after user authentication.
type OIDCAuthorizationResult struct {
	// Code is the authorization code returned by the IdP.
	Code string
	// State is the state parameter returned by the IdP.
	// This MUST match the state sent in the authorization request.
	State string
}

// AuthorizationCodeFetcher is a callback function that handles directing the user
// to the authorization URL and returning the authorization result.
//
// Implementations should:
// 1. Direct the user to authURL (e.g., open in browser)
// 2. Wait for the IdP redirect to the configured RedirectURL
// 3. Extract the code and state from the redirect query parameters
// 4. Return them in OIDCAuthorizationResult
//
// The expectedState parameter is provided for CSRF validation. Implementations
// MUST verify that the returned state matches expectedState.
type AuthorizationCodeFetcher func(ctx context.Context, authURL string, expectedState string) (*OIDCAuthorizationResult, error)

// PerformOIDCLogin performs the complete OIDC Authorization Code flow with PKCE
// in a single function call. This is the recommended approach for most use cases.
//
// The authCodeFetcher callback handles the user interaction:
// - Directing the user to the IdP login page
// - Waiting for the redirect with the authorization code
// - Validating CSRF state and returning the result
//
// Example:
//
//	config := &OIDCLoginConfig{
//		IssuerURL:   "https://acme.okta.com",
//		ClientID:    "client-id",
//		RedirectURL: "http://localhost:8080/callback",
//		Scopes:      []string{"openid", "profile", "email"},
//	}
//
//	tokens, err := PerformOIDCLogin(ctx, config, func(ctx context.Context, authURL, expectedState string) (*OIDCAuthorizationResult, error) {
//		// Open browser for user
//		fmt.Printf("Please visit: %s\n", authURL)
//
//		// Start local server to receive callback
//		code, state := waitForCallback(ctx)
//
//		// Validate state for CSRF protection
//		if state != expectedState {
//			return nil, fmt.Errorf("state mismatch")
//		}
//
//		return &OIDCAuthorizationResult{Code: code, State: state}, nil
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	// Use tokens.IDToken with EnterpriseHandler
func PerformOIDCLogin(
	ctx context.Context,
	config *OIDCLoginConfig,
	authCodeFetcher AuthorizationCodeFetcher,
) (*OIDCTokenResponse, error) {
	if authCodeFetcher == nil {
		return nil, fmt.Errorf("authCodeFetcher is required")
	}

	// Step 1: Initiate the OIDC flow to get the authorization URL
	authReq, err := InitiateOIDCLogin(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate OIDC login: %w", err)
	}

	// Step 2: Use callback to get authorization code from user interaction
	authResult, err := authCodeFetcher(ctx, authReq.AuthURL, authReq.State)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch authorization code: %w", err)
	}

	// Step 3: Validate state for CSRF protection
	if authResult.State != authReq.State {
		return nil, fmt.Errorf("state mismatch: expected %q, got %q", authReq.State, authResult.State)
	}

	// Step 4: Exchange authorization code for tokens
	tokens, err := CompleteOIDCLogin(ctx, config, authResult.Code, authReq.CodeVerifier)
	if err != nil {
		return nil, fmt.Errorf("failed to complete OIDC login: %w", err)
	}

	return tokens, nil
}
