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
	MCPResourceURL   string   // MCP Server's resource identifier
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
// Example:
//
//	config := &EnterpriseAuthConfig{
//		IdPIssuerURL:     "https://acme.okta.com",
//		IdPClientID:      "client-id-at-idp",
//		IdPClientSecret:  "secret-at-idp",
//		MCPAuthServerURL: "https://auth.mcpserver.example",
//		MCPResourceURL:   "https://mcp.mcpserver.example",
//		MCPClientID:      "client-id-at-mcp",
//		MCPClientSecret:  "secret-at-mcp",
//		MCPScopes:        []string{"read", "write"},
//	}
//
//	// After user logs in via OIDC, you have an ID Token
//	accessToken, err := EnterpriseAuthFlow(ctx, config, idToken)
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
	if config.MCPResourceURL == "" {
		return nil, fmt.Errorf("MCPResourceURL is required")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	// Step 1: Token Exchange (ID Token → ID-JAG)
	tokenExchangeReq := &oauthex.TokenExchangeRequest{
		RequestedTokenType: oauthex.TokenTypeIDJAG,
		Audience:           config.MCPAuthServerURL,
		Resource:           config.MCPResourceURL,
		Scope:              config.MCPScopes,
		SubjectToken:       idToken,
		SubjectTokenType:   oauthex.TokenTypeIDToken,
	}
	// Construct IdP token endpoint (assuming standard path)
	idpTokenEndpoint := config.IdPIssuerURL + "/oauth2/v1/token"
	if config.IdPIssuerURL[len(config.IdPIssuerURL)-1] == '/' {
		idpTokenEndpoint = config.IdPIssuerURL + "oauth2/v1/token"
	}
	tokenExchangeResp, err := oauthex.ExchangeToken(
		ctx,
		idpTokenEndpoint,
		tokenExchangeReq,
		config.IdPClientID,
		config.IdPClientSecret,
		httpClient,
	)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}

	// Step 2: JWT Bearer Grant (ID-JAG → Access Token)
	mcpTokenEndpoint := config.MCPAuthServerURL + "/v1/token"
	if config.MCPAuthServerURL[len(config.MCPAuthServerURL)-1] == '/' {
		mcpTokenEndpoint = config.MCPAuthServerURL + "v1/token"
	}
	accessToken, err := oauthex.ExchangeJWTBearer(
		ctx,
		mcpTokenEndpoint,
		tokenExchangeResp.AccessToken, // The ID-JAG
		config.MCPClientID,
		config.MCPClientSecret,
		httpClient,
	)
	if err != nil {
		return nil, fmt.Errorf("JWT bearer grant failed: %w", err)
	}
	return accessToken, nil
}
