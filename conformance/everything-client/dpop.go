// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// runDpopClient exercises SEP-1932 / RFC 9449 baseline DPoP (auth/dpop)
// using the SDK AuthorizationCodeHandler DPoP path. Nonce handling is
// intentionally omitted (auth/dpop-nonce remains an expected failure).
func runDpopClient(ctx context.Context, serverURL string, configCtx map[string]any) error {
	authConfig := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:              "http://127.0.0.1:9876/callback",
		AuthorizationCodeFetcher: fetchAuthorizationCodeAndState,
		ClientIDMetadataDocumentConfig: &auth.ClientIDMetadataDocumentConfig{
			URL: "https://conformance-test.local/client-metadata.json",
		},
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				RedirectURIs:            []string{"http://127.0.0.1:9876/callback"},
				ApplicationType:         "native",
				ClientName:              "conformance-dpop-client",
				TokenEndpointAuthMethod: "none",
			},
		},
		DPoP: &oauthex.DPoPConfig{},
	}
	if clientID, ok := configCtx["client_id"].(string); ok {
		if clientSecret, ok := configCtx["client_secret"].(string); ok {
			authConfig.PreregisteredClient = &oauthex.ClientCredentials{
				ClientID: clientID,
				ClientSecretAuth: &oauthex.ClientSecretAuth{
					ClientSecret: clientSecret,
				},
			}
		}
	}

	authHandler, err := auth.NewAuthorizationCodeHandler(authConfig)
	if err != nil {
		return fmt.Errorf("failed to create auth handler: %w", err)
	}

	session, err := connectToServer(ctx, serverURL, withOAuthHandler(authHandler))
	if err != nil {
		return err
	}
	defer session.Close()

	if _, err := session.ListTools(ctx, nil); err != nil {
		return fmt.Errorf("session.ListTools(): %v", err)
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "test-tool",
		Arguments: map[string]any{},
	}); err != nil {
		return fmt.Errorf("session.CallTool('test-tool'): %v", err)
	}
	return nil
}
