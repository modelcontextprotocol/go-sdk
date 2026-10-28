// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package extauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/internal/oauthtest"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

const testRedirectURL = "http://localhost:18927/callback"

func validOIDCLoginConfig(issuerURL string) *OIDCLoginConfig {
	return &OIDCLoginConfig{
		IssuerURL: issuerURL,
		Credentials: &oauthex.ClientCredentials{
			ClientID: "test-client",
			ClientSecretAuth: &oauthex.ClientSecretAuth{
				ClientSecret: "test-secret",
			},
		},
		RedirectURL: testRedirectURL,
		Scopes:      []string{"openid", "profile", "email"},
	}
}

func TestPerformOIDCLogin(t *testing.T) {
	idpServer := oauthtest.NewFakeIdPServer(oauthtest.IdPConfig{
		PreregisteredClients: map[string]oauthtest.ClientInfo{
			"test-client": {
				Secret:       "test-secret",
				RedirectURIs: []string{testRedirectURL},
			},
		},
	})
	idpServer.Start(t)

	config := validOIDCLoginConfig(idpServer.URL())

	// fetchAuthCode visits the authorization URL on the fake IdP server,
	// follows the redirect, and extracts the authorization code and state.
	fetchAuthCode := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := client.Get(args.URL)
		if err != nil {
			return nil, fmt.Errorf("failed to visit auth URL: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			return nil, fmt.Errorf("expected redirect, got status %d", resp.StatusCode)
		}
		loc, err := resp.Location()
		if err != nil {
			return nil, fmt.Errorf("missing Location header: %w", err)
		}
		return &auth.AuthorizationResult{
			Code:  loc.Query().Get("code"),
			State: loc.Query().Get("state"),
		}, nil
	}

	verifyOIDCToken := func(token *oauth2.Token) error {
		idToken, ok := token.Extra("id_token").(string)
		if !ok || idToken == "" {
			return fmt.Errorf("id_token is missing or empty")
		}
		parts := strings.Split(idToken, ".")
		if len(parts) != 3 {
			return fmt.Errorf("id_token is not a JWT (expected 3 parts, got %d)", len(parts))
		}
		if token.AccessToken == "" {
			return fmt.Errorf("access token is empty")
		}
		if token.TokenType != "Bearer" {
			return fmt.Errorf("token type = %q, want %q", token.TokenType, "Bearer")
		}
		return nil
	}

	t.Run("successful flow", func(t *testing.T) {
		token, err := PerformOIDCLogin(context.Background(), config, fetchAuthCode)
		if err != nil {
			t.Fatalf("PerformOIDCLogin() error = %v", err)
		}
		if err := verifyOIDCToken(token); err != nil {
			t.Errorf("invalid token returned: %v", err)
		}
	})

	t.Run("with login_hint", func(t *testing.T) {
		configWithHint := *config
		configWithHint.LoginHint = "user@example.com"

		token, err := PerformOIDCLogin(context.Background(), &configWithHint,
			func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
				u, err := url.Parse(args.URL)
				if err != nil {
					return nil, fmt.Errorf("invalid authURL: %w", err)
				}
				if got := u.Query().Get("login_hint"); got != "user@example.com" {
					t.Errorf("login_hint = %q, want %q", got, "user@example.com")
				}
				return fetchAuthCode(ctx, args)
			})
		if err != nil {
			t.Fatalf("PerformOIDCLogin() error = %v", err)
		}
		if err := verifyOIDCToken(token); err != nil {
			t.Errorf("invalid token returned: %v", err)
		}
	})

	t.Run("without login_hint", func(t *testing.T) {
		token, err := PerformOIDCLogin(context.Background(), config,
			func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
				u, err := url.Parse(args.URL)
				if err != nil {
					return nil, fmt.Errorf("invalid authURL: %w", err)
				}
				if u.Query().Has("login_hint") {
					t.Errorf("login_hint should be absent, got %q", u.Query().Get("login_hint"))
				}
				return fetchAuthCode(ctx, args)
			})
		if err != nil {
			t.Fatalf("PerformOIDCLogin() error = %v", err)
		}
		if err := verifyOIDCToken(token); err != nil {
			t.Errorf("invalid token returned: %v", err)
		}
	})

	t.Run("state mismatch", func(t *testing.T) {
		_, err := PerformOIDCLogin(context.Background(), config,
			func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
				return &auth.AuthorizationResult{
					Code:  "mock-auth-code",
					State: "wrong-state",
				}, nil
			})

		if err == nil {
			t.Fatal("expected error for state mismatch, got nil")
		}
		if !strings.Contains(err.Error(), "state mismatch") {
			t.Errorf("error = %v, want error containing %q", err, "state mismatch")
		}
	})

	t.Run("fetcher error", func(t *testing.T) {
		_, err := PerformOIDCLogin(context.Background(), config,
			func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
				return nil, fmt.Errorf("user cancelled")
			})

		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "user cancelled") {
			t.Errorf("error = %v, want error containing %q", err, "user cancelled")
		}
	})
}

func TestPerformOIDCLogin_ValidationErrors(t *testing.T) {
	const nonexistentIssuer = "https://idp.example.com"

	noopFetcher := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		return nil, fmt.Errorf("should not be called")
	}

	tests := []struct {
		name    string
		config  *OIDCLoginConfig
		fetcher auth.AuthorizationCodeFetcher
		wantErr string
	}{
		{
			name:    "nil config",
			config:  nil,
			fetcher: noopFetcher,
			wantErr: "config is required",
		},
		{
			name:    "nil fetcher",
			config:  validOIDCLoginConfig(nonexistentIssuer),
			fetcher: nil,
			wantErr: "authCodeFetcher is required",
		},
		{
			name:    "missing IssuerURL",
			config:  validOIDCLoginConfig(""),
			fetcher: noopFetcher,
			wantErr: "IssuerURL is required",
		},
		{
			name: "missing ClientID",
			config: func() *OIDCLoginConfig {
				c := validOIDCLoginConfig(nonexistentIssuer)
				c.Credentials = &oauthex.ClientCredentials{}
				return c
			}(),
			fetcher: noopFetcher,
			wantErr: "ClientID is required",
		},
		{
			name: "missing RedirectURL",
			config: func() *OIDCLoginConfig {
				c := validOIDCLoginConfig(nonexistentIssuer)
				c.RedirectURL = ""
				return c
			}(),
			fetcher: noopFetcher,
			wantErr: "RedirectURL is required",
		},
		{
			name: "missing Scopes",
			config: func() *OIDCLoginConfig {
				c := validOIDCLoginConfig(nonexistentIssuer)
				c.Scopes = nil
				return c
			}(),
			fetcher: noopFetcher,
			wantErr: "at least one scope is required",
		},
		{
			name: "missing openid scope",
			config: func() *OIDCLoginConfig {
				c := validOIDCLoginConfig(nonexistentIssuer)
				c.Scopes = []string{"profile", "email"}
				return c
			}(),
			fetcher: noopFetcher,
			wantErr: "openid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PerformOIDCLogin(context.Background(), tt.config, tt.fetcher)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestPerformOIDCLogin_RequiresPKCE verifies that the OIDC login refuses an
// identity provider that does not advertise PKCE, before the user is sent to it.
func TestPerformOIDCLogin_RequiresPKCE(t *testing.T) {
	idpServer := oauthtest.NewFakeIdPServer(oauthtest.IdPConfig{OmitPKCEMethods: true})
	idpServer.Start(t)

	fetcher := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		t.Error("authCodeFetcher called for an identity provider without PKCE")
		return nil, fmt.Errorf("unexpected authorization")
	}
	_, err := PerformOIDCLogin(context.Background(), validOIDCLoginConfig(idpServer.URL()), fetcher)
	if err == nil || !strings.Contains(err.Error(), "does not implement PKCE") {
		t.Errorf("PerformOIDCLogin() = %v, want an error about missing PKCE support", err)
	}
}

// TestPerformOIDCLogin_Issuer checks the iss parameter of the authorization
// response (RFC 9207), the mitigation for mix-up attacks.
func TestPerformOIDCLogin_Issuer(t *testing.T) {
	const otherIssuer = "https://attacker.example.com"
	tests := []struct {
		name       string
		advertised bool
		// iss returns the iss the fetcher reports, given the one in the
		// IdP's redirect and the IdP's issuer.
		iss     func(fromRedirect, issuer string) string
		wantErr string
	}{
		{
			name:       "advertised, iss from the IdP",
			advertised: true,
			iss:        func(fromRedirect, _ string) string { return fromRedirect },
		},
		{
			name:       "advertised, iss missing",
			advertised: true,
			iss:        func(string, string) string { return "" },
			wantErr:    "none was received",
		},
		{
			name:       "advertised, other issuer",
			advertised: true,
			iss:        func(string, string) string { return otherIssuer },
			wantErr:    "does not match expected issuer",
		},
		{
			name: "not advertised, iss missing",
			iss:  func(string, string) string { return "" },
		},
		{
			name: "not advertised, matching iss",
			iss:  func(_, issuer string) string { return issuer },
		},
		{
			name:    "not advertised, other issuer",
			iss:     func(string, string) string { return otherIssuer },
			wantErr: "does not match expected issuer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idpServer := oauthtest.NewFakeIdPServer(oauthtest.IdPConfig{
				PreregisteredClients: map[string]oauthtest.ClientInfo{
					"test-client": {
						Secret:       "test-secret",
						RedirectURIs: []string{testRedirectURL},
					},
				},
				IssParameterSupported: tt.advertised,
			})
			idpServer.Start(t)

			fetch := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
				client := &http.Client{
					CheckRedirect: func(*http.Request, []*http.Request) error {
						return http.ErrUseLastResponse
					},
				}
				resp, err := client.Get(args.URL)
				if err != nil {
					return nil, err
				}
				defer resp.Body.Close()
				loc, err := resp.Location()
				if err != nil {
					return nil, err
				}
				q := loc.Query()
				return &auth.AuthorizationResult{
					Code:  q.Get("code"),
					State: q.Get("state"),
					Iss:   tt.iss(q.Get("iss"), idpServer.URL()),
				}, nil
			}

			_, err := PerformOIDCLogin(context.Background(), validOIDCLoginConfig(idpServer.URL()), fetch)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("PerformOIDCLogin() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("PerformOIDCLogin() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
