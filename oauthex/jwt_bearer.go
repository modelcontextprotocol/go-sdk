// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file implements JWT Bearer Authorization Grant (RFC 7523) for Enterprise Managed Authorization.
// See https://datatracker.ietf.org/doc/html/rfc7523

//go:build mcp_go_client_oauth

package oauthex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

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
	if err := checkURLScheme(tokenEndpoint); err != nil {
		return nil, fmt.Errorf("invalid token endpoint: %w", err)
	}
	// Build the JWT Bearer grant request per RFC 7523 Section 2.1
	formData := url.Values{}
	formData.Set("grant_type", GrantTypeJWTBearer)
	formData.Set("assertion", assertion)
	// Add client authentication (following OAuth 2.0 client_secret_post method)
	// Note: Per SEP-990 Section 5.1, the client_id in the assertion must match
	// the authenticated client
	if clientID != "" {
		formData.Set("client_id", clientID)
	}
	if clientSecret != "" {
		formData.Set("client_secret", clientSecret)
	}
	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		tokenEndpoint,
		strings.NewReader(formData.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWT bearer grant request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")
	// Use provided client or default
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	// Execute the request
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("JWT bearer grant request failed: %w", err)
	}
	defer httpResp.Body.Close()
	// Read response body (limit to 1MB for safety, following SDK pattern)
	body, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read JWT bearer grant response: %w", err)
	}
	// Handle success response (200 OK per OAuth 2.0)
	if httpResp.StatusCode == http.StatusOK {
		var resp struct {
			AccessToken  string `json:"access_token"`
			TokenType    string `json:"token_type"`
			ExpiresIn    int    `json:"expires_in,omitempty`
			RefreshToken string `json:"refresh_token,omitempty"`
			Scope        string `json:"scope,omitempty"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse JWT bearer grant response: %w (body: %s)", err, string(body))
		}
		// Validate response per OAuth 2.0
		if resp.AccessToken == "" {
			return nil, fmt.Errorf("response missing required field: access_token")
		}
		if resp.TokenType == "" {
			return nil, fmt.Errorf("response missing required field: token_type")
		}
		// Convert to golang.org/x/oauth2.Token
		token := &oauth2.Token{
			AccessToken:  resp.AccessToken,
			TokenType:    resp.TokenType,
			RefreshToken: resp.RefreshToken,
		}
		// Set expiration if provided
		if resp.ExpiresIn > 0 {
			token.Expiry = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
		}
		// Add scope to extra data if provided
		if resp.Scope != "" {
			token = token.WithExtra(map[string]interface{}{
				"scope": resp.Scope,
			})
		}
		return token, nil
	}
	// Handle error response (400 Bad Request per RFC 6749)
	if httpResp.StatusCode == http.StatusBadRequest {
		var errResp struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description,omitempty"`
			ErrorURI         string `json:"error_uri,omitempty"`
		}
		if err := json.Unmarshal(body, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse error response: %w (body: %s)", err, string(body))
		}
		return nil, &oauth2.RetrieveError{
			Response:         httpResp,
			Body:             body,
			ErrorCode:        errResp.Error,
			ErrorDescription: errResp.ErrorDescription,
			ErrorURI:         errResp.ErrorURI,
		}
	}
	// Handle unexpected status codes
	return nil, &oauth2.RetrieveError{
		Response: httpResp,
		Body:     body,
	}
}
