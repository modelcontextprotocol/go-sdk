// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// The conformance client implements features required for MCP conformance testing.
// It mirrors the functionality of the TypeScript conformance client at
// https://github.com/modelcontextprotocol/typescript-sdk/blob/main/src/conformance/everything-client.ts

//go:build mcp_go_client_oauth

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

func init() {
	authScenarios := []string{
		"auth/2025-03-26-oauth-metadata-backcompat",
		"auth/2025-03-26-oauth-endpoint-fallback",
		"auth/basic-cimd",
		"auth/metadata-default",
		"auth/metadata-var1",
		"auth/metadata-var2",
		"auth/metadata-var3",
		"auth/pre-registration",
		"auth/resource-mismatch",
		"auth/scope-from-www-authenticate",
		"auth/scope-from-scopes-supported",
		"auth/scope-omitted-when-undefined",
		"auth/scope-step-up",
		"auth/scope-retry-limit",
		"auth/token-endpoint-auth-basic",
		"auth/token-endpoint-auth-post",
		"auth/token-endpoint-auth-none",
	}
	for _, scenario := range authScenarios {
		registerScenario(scenario, runAuthClient)
	}
}

// ============================================================================
// Auth scenarios
// ============================================================================

func fetchAuthorizationCodeAndState(ctx context.Context, authURL string) (*auth.AuthorizationResult, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, "GET", authURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	location := resp.Header.Get("Location")
	if location == "" {
		return nil, fmt.Errorf("no Location header in redirect")
	}

	locURL, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("parse location: %v", err)
	}

	code := locURL.Query().Get("code")
	if code == "" {
		return nil, fmt.Errorf("no code parameter in redirect URL")
	}
	state := locURL.Query().Get("state")
	if state == "" {
		return nil, fmt.Errorf("no state parameter in redirect URL")
	}

	return &auth.AuthorizationResult{
		AuthorizationCode: code,
		State:             state,
	}, nil
}

func runAuthClient(ctx context.Context, serverURL string, configCtx map[string]any) error {
	authConfig := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:             "http://localhost:3000/callback",
		AuthorizationURLHandler: fetchAuthorizationCodeAndState,
		// Try client ID metadata document based registration.
		ClientIDMetadataDocumentConfig: &auth.ClientIDMetadataDocumentConfig{
			URL: "https://conformance-test.local/client-metadata.json",
		},
		// Try dynamic client registration.
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				RedirectURIs: []string{"http://localhost:3000/callback"},
			},
		},
	}
	// Try pre-registered client information if provided in the context.
	if clientId, ok := configCtx["client_id"].(string); ok {
		if clientSecret, ok := configCtx["client_secret"].(string); ok {
			authConfig.PreregisteredClientConfig = &auth.PreregisteredClientConfig{
				ClientID:     clientId,
				ClientSecret: clientSecret,
			}
		}
	}

	authHandler, err := auth.NewAuthorizationCodeOAuthHandler(authConfig)
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

func withOAuthHandler(handler auth.OAuthHandler) connectOption {
	return func(c *connectConfig) {
		c.oauthHandler = handler
	}
}
