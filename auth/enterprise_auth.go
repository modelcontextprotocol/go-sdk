// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file implements the client-side Enterprise Managed Authorization flow
// for MCP as specified in SEP-990.

//go:build mcp_go_client_oauth

package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// EnterpriseAuthConfig contains configuration for Enterprise Managed Authorization
// (SEP-990). This configures both the IdP (for token exchange) and the MCP Server
// (for JWT Bearer grant).
type EnterpriseAuthConfig struct {
	// IdP configuration (where the user authenticates)
	IdPIssuerURL    string // e.g., "https://acme.okta.com"
	IdPClientID     string // MCP Client's ID at the IdP
	IdPClientSecret string // MCP Client's secret at the IdP

	// MCP Server configuration (the resource being accessed)
	MCPAuthServerURL string   // MCP Server's auth server issuer URL
	MCPResourceURI   string   // MCP Server's resource identifier
	MCPClientID      string   // MCP Client's ID at the MCP Server
	MCPClientSecret  string   // MCP Client's secret at the MCP Server
	MCPScopes        []string // Requested scopes at the MCP Server

	// Optional HTTP client for customization
	HTTPClient *http.Client
}

// EnterpriseAuthFlow performs the complete Enterprise Managed Authorization flow:
// 1. Token Exchange: ID Token → ID-JAG at IdP
// 2. JWT Bearer: ID-JAG → Access Token at MCP Server
//
// This function takes an ID Token that was obtained via SSO (e.g., OIDC login)
// and exchanges it for an access token that can be used to call the MCP Server.
//
// There are two ways to obtain an ID Token for use with this function:
//
// Option 1: Use the OIDC login helper functions (full flow with SSO):
//
//	// Step 1: Initiate OIDC login
//	oidcConfig := &OIDCLoginConfig{
//		IssuerURL:   "https://acme.okta.com",
//		ClientID:    "client-id",
//		RedirectURL: "http://localhost:8080/callback",
//		Scopes:      []string{"openid", "profile", "email"},
//	}
//	authReq, err := InitiateOIDCLogin(ctx, oidcConfig)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	// Step 2: Direct user to authReq.AuthURL for authentication
//	fmt.Printf("Visit: %s\n", authReq.AuthURL)
//
//	// Step 3: After redirect, complete login with authorization code
//	tokens, err := CompleteOIDCLogin(ctx, oidcConfig, authCode, authReq.CodeVerifier)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	// Step 4: Use ID token for enterprise auth
//	enterpriseConfig := &EnterpriseAuthConfig{
//		IdPIssuerURL:     "https://acme.okta.com",
//		IdPClientID:      "client-id-at-idp",
//		IdPClientSecret:  "secret-at-idp",
//		MCPAuthServerURL: "https://auth.mcpserver.example",
//		MCPResourceURI:   "https://mcp.mcpserver.example",
//		MCPClientID:      "client-id-at-mcp",
//		MCPClientSecret:  "secret-at-mcp",
//		MCPScopes:        []string{"read", "write"},
//	}
//	accessToken, err := EnterpriseAuthFlow(ctx, enterpriseConfig, tokens.IDToken)
//	if err != nil {
//		log.Fatal(err)
//	}
//
// Option 2: Bring your own ID Token (if you already have one):
//
//	config := &EnterpriseAuthConfig{
//		IdPIssuerURL:     "https://acme.okta.com",
//		IdPClientID:      "client-id-at-idp",
//		IdPClientSecret:  "secret-at-idp",
//		MCPAuthServerURL: "https://auth.mcpserver.example",
//		MCPResourceURI:   "https://mcp.mcpserver.example",
//		MCPClientID:      "client-id-at-mcp",
//		MCPClientSecret:  "secret-at-mcp",
//		MCPScopes:        []string{"read", "write"},
//	}
//
//	// If you already obtained an ID token through your own means
//	accessToken, err := EnterpriseAuthFlow(ctx, config, myIDToken)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	// Use accessToken to call MCP Server APIs
func EnterpriseAuthFlow(
	ctx context.Context,
	config *EnterpriseAuthConfig,
	idToken string,
) (*oauth2.Token, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}
	if idToken == "" {
		return nil, fmt.Errorf("idToken is required")
	}
	// Validate configuration
	if config.IdPIssuerURL == "" {
		return nil, fmt.Errorf("IdPIssuerURL is required")
	}
	if config.MCPAuthServerURL == "" {
		return nil, fmt.Errorf("MCPAuthServerURL is required")
	}
	if config.MCPResourceURI == "" {
		return nil, fmt.Errorf("MCPResourceURI is required")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	// Step 1: Discover IdP token endpoint via OIDC discovery
	idpMeta, err := oauthex.GetAuthServerMeta(ctx, config.IdPIssuerURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to discover IdP metadata: %w", err)
	}

	// Step 2: Token Exchange (ID Token → ID-JAG)
	tokenExchangeReq := &oauthex.TokenExchangeRequest{
		RequestedTokenType: oauthex.TokenTypeIDJAG,
		Audience:           config.MCPAuthServerURL,
		Resource:           config.MCPResourceURI,
		Scope:              config.MCPScopes,
		SubjectToken:       idToken,
		SubjectTokenType:   oauthex.TokenTypeIDToken,
	}

	tokenExchangeResp, err := oauthex.ExchangeToken(
		ctx,
		idpMeta.TokenEndpoint,
		tokenExchangeReq,
		config.IdPClientID,
		config.IdPClientSecret,
		httpClient,
	)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	// Step 3: JWT Bearer Grant (ID-JAG → Access Token)
	mcpMeta, err := oauthex.GetAuthServerMeta(ctx, config.MCPAuthServerURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to discover MCP auth server metadata: %w", err)
	}

	accessToken, err := oauthex.ExchangeJWTBearer(
		ctx,
		mcpMeta.TokenEndpoint,
		tokenExchangeResp.AccessToken,
		config.MCPClientID,
		config.MCPClientSecret,
		httpClient,
	)
	if err != nil {
		return nil, fmt.Errorf("JWT bearer grant failed: %w", err)
	}
	return accessToken, nil
}
