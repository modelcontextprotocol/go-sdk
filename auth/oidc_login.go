// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file implements OIDC Authorization Code flow for obtaining ID tokens
// as part of Enterprise Managed Authorization (SEP-990).
// See https://openid.net/specs/openid-connect-core-1_0.html

//go:build mcp_go_client_oauth

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// OIDCLoginConfig configures the OIDC Authorization Code flow for obtaining
// an ID Token. This is an OPTIONAL step before calling EnterpriseAuthFlow.
// Users can alternatively obtain ID tokens through their own methods.
type OIDCLoginConfig struct {
	// IssuerURL is the IdP's issuer URL (e.g., "https://acme.okta.com").
	IssuerURL string
	// ClientID is the MCP Client's ID registered at the IdP.
	ClientID string
	// ClientSecret is the MCP Client's secret at the IdP.
	// This is OPTIONAL and only used if the client is confidential.
	ClientSecret string
	// RedirectURL is the OAuth2 redirect URI registered with the IdP.
	// This must match exactly what was registered with the IdP.
	RedirectURL string
	// Scopes are the OAuth2/OIDC scopes to request.
	// "openid" is REQUIRED for OIDC. Common values: ["openid", "profile", "email"]
	Scopes []string
	// LoginHint is an OPTIONAL hint to the IdP about the user's identity.
	// Some IdPs may require this (e.g., as an email address for routing to SSO providers).
	// Example: "user@example.com"
	LoginHint string
	// HTTPClient is the HTTP client for making requests.
	// If nil, http.DefaultClient is used.
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
	meta, err := oauthex.GetAuthServerMeta(ctx, config.IssuerURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OIDC metadata: %w", err)
	}
	if meta.AuthorizationEndpoint == "" {
		return nil, fmt.Errorf("authorization_endpoint not found in OIDC metadata")
	}
	// Generate PKCE code verifier and challenge (RFC 7636)
	codeVerifier, err := generateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PKCE verifier: %w", err)
	}
	codeChallenge := generateCodeChallenge(codeVerifier)
	// Generate state for CSRF protection (RFC 6749 Section 10.12)
	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}
	// Build authorization URL per OIDC Core Section 3.1.2.1
	authURL, err := buildAuthorizationURL(
		meta.AuthorizationEndpoint,
		config.ClientID,
		config.RedirectURL,
		config.Scopes,
		state,
		codeChallenge,
		config.LoginHint,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build authorization URL: %w", err)
	}
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
	meta, err := oauthex.GetAuthServerMeta(ctx, config.IssuerURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OIDC metadata: %w", err)
	}
	if meta.TokenEndpoint == "" {
		return nil, fmt.Errorf("token_endpoint not found in OIDC metadata")
	}
	// Build token request per OIDC Core Section 3.1.3.1
	formData := url.Values{}
	formData.Set("grant_type", "authorization_code")
	formData.Set("code", authCode)
	formData.Set("redirect_uri", config.RedirectURL)
	formData.Set("client_id", config.ClientID)
	formData.Set("code_verifier", codeVerifier)
	// Add client_secret if provided (confidential client)
	if config.ClientSecret != "" {
		formData.Set("client_secret", config.ClientSecret)
	}
	// Exchange authorization code for tokens
	oauth2Token, err := exchangeAuthorizationCode(
		ctx,
		meta.TokenEndpoint,
		formData,
		httpClient,
	)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	// Extract ID Token from response
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

// generateCodeVerifier generates a cryptographically random code verifier
// for PKCE per RFC 7636 Section 4.1.
func generateCodeVerifier() (string, error) {
	// Per RFC 7636: code verifier is 43-128 characters from [A-Z] / [a-z] / [0-9] / "-" / "." / "_" / "~"
	// We use 32 random bytes (256 bits) base64url-encoded = 43 characters
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

// generateCodeChallenge generates the PKCE code challenge from the verifier
// using SHA256 per RFC 7636 Section 4.2.
func generateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// generateState generates a cryptographically random state parameter
// for CSRF protection per RFC 6749 Section 10.12.
func generateState() (string, error) {
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

// buildAuthorizationURL constructs the OIDC authorization URL.
func buildAuthorizationURL(
	authEndpoint string,
	clientID string,
	redirectURL string,
	scopes []string,
	state string,
	codeChallenge string,
	loginHint string,
) (string, error) {
	u, err := url.Parse(authEndpoint)
	if err != nil {
		return "", fmt.Errorf("invalid authorization endpoint: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURL)
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	// Add login_hint if provided (optional per OIDC spec, but some IdPs may require it)
	if loginHint != "" {
		q.Set("login_hint", loginHint)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// exchangeAuthorizationCode exchanges the authorization code for tokens.
func exchangeAuthorizationCode(
	ctx context.Context,
	tokenEndpoint string,
	formData url.Values,
	httpClient *http.Client,
) (*oauth2.Token, error) {
	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		tokenEndpoint,
		strings.NewReader(formData.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	// Execute request
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Read response body (limit to 1MB for safety)
	body, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	// Handle success response (200 OK)
	if httpResp.StatusCode == http.StatusOK {
		// Parse token response manually (following jwt_bearer.go pattern)
		var tokenResp struct {
			AccessToken  string `json:"access_token"`
			TokenType    string `json:"token_type"`
			ExpiresIn    int    `json:"expires_in,omitempty"`
			RefreshToken string `json:"refresh_token,omitempty"`
			IDToken      string `json:"id_token,omitempty"`
			Scope        string `json:"scope,omitempty"`
		}
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			return nil, fmt.Errorf("failed to parse token response: %w (body: %s)", err, string(body))
		}

		// Validate required fields
		if tokenResp.AccessToken == "" {
			return nil, fmt.Errorf("response missing required field: access_token")
		}
		if tokenResp.TokenType == "" {
			return nil, fmt.Errorf("response missing required field: token_type")
		}

		// Convert to oauth2.Token
		token := &oauth2.Token{
			AccessToken:  tokenResp.AccessToken,
			TokenType:    tokenResp.TokenType,
			RefreshToken: tokenResp.RefreshToken,
		}

		// Set expiration if provided
		if tokenResp.ExpiresIn > 0 {
			token.Expiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		}

		// Add extra fields (id_token, scope)
		extra := make(map[string]interface{})
		if tokenResp.IDToken != "" {
			extra["id_token"] = tokenResp.IDToken
		}
		if tokenResp.Scope != "" {
			extra["scope"] = tokenResp.Scope
		}
		if len(extra) > 0 {
			token = token.WithExtra(extra)
		}

		return token, nil
	}

	// Handle error response (400 Bad Request)
	if httpResp.StatusCode == http.StatusBadRequest {
		var errResp struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description,omitempty"`
			ErrorURI         string `json:"error_uri,omitempty"`
		}
		if err := json.Unmarshal(body, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse error response: %w (body: %s)", err, string(body))
		}
		if errResp.ErrorDescription != "" {
			return nil, fmt.Errorf("token request failed: %s (%s)", errResp.Error, errResp.ErrorDescription)
		}
		return nil, fmt.Errorf("token request failed: %s", errResp.Error)
	}

	// Handle unexpected status codes
	return nil, fmt.Errorf("unexpected status code %d: %s", httpResp.StatusCode, string(body))
}
