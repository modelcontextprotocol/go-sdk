// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file implements Token Exchange (RFC 8693) for Enterprise Managed Authorization.
// See https://datatracker.ietf.org/doc/html/rfc8693

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

// TokenExchangeError represents an error response from a token exchange request.
type TokenExchangeError struct {
	// Error is the error code as defined in RFC 6749 Section 5.2.
	ErrorCode string `json:"error"`

	// ErrorDescription is a human-readable description of the error.
	ErrorDescription string `json:"error_description,omitempty"`

	// ErrorURI is a URI identifying a human-readable web page with information
	// about the error.
	ErrorURI string `json:"error_uri,omitempty"`
}

func (e *TokenExchangeError) Error() string {
	if e.ErrorDescription != "" {
		return fmt.Sprintf("token exchange failed: %s (%s)", e.ErrorCode, e.ErrorDescription)
	}
	return fmt.Sprintf("token exchange failed: %s", e.ErrorCode)
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

	// Build the token exchange request body per RFC 8693
	formData := url.Values{}
	formData.Set("grant_type", GrantTypeTokenExchange)
	formData.Set("requested_token_type", req.RequestedTokenType)
	formData.Set("audience", req.Audience)
	formData.Set("resource", req.Resource)
	formData.Set("subject_token", req.SubjectToken)
	formData.Set("subject_token_type", req.SubjectTokenType)

	if len(req.Scope) > 0 {
		formData.Set("scope", strings.Join(req.Scope, " "))
	}

	// Add client authentication (following OAuth 2.0 client_secret_post method)
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
		return nil, fmt.Errorf("failed to create token exchange request: %w", err)
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
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Read response body (limit to 1MB for safety, following SDK pattern)
	body, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read token exchange response: %w", err)
	}

	// Handle success response (200 OK per RFC 8693)
	if httpResp.StatusCode == http.StatusOK {
		var resp TokenExchangeResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse token exchange response: %w (body: %s)", err, string(body))
		}

		// Validate response per SEP-990 Section 4.2
		if resp.IssuedTokenType == "" {
			return nil, fmt.Errorf("response missing required field: issued_token_type")
		}
		if resp.AccessToken == "" {
			return nil, fmt.Errorf("response missing required field: access_token")
		}
		if resp.TokenType == "" {
			return nil, fmt.Errorf("response missing required field: token_type")
		}

		return &resp, nil
	}

	// Handle error response (400 Bad Request per RFC 6749)
	if httpResp.StatusCode == http.StatusBadRequest {
		var errResp TokenExchangeError
		if err := json.Unmarshal(body, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse error response: %w (body: %s)", err, string(body))
		}
		return nil, &errResp
	}

	// Handle unexpected status codes
	return nil, fmt.Errorf("unexpected status code %d: %s", httpResp.StatusCode, string(body))
}
