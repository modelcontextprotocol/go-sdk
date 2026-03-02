// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/internal/oauthtest"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

func TestAuthorize(t *testing.T) {
	authServer := oauthtest.NewFakeAuthorizationServer(oauthtest.Config{
		RegistrationConfig: &oauthtest.RegistrationConfig{
			PreregisteredClients: map[string]oauthtest.ClientInfo{
				"test_client_id": {
					Secret:       "test_client_secret",
					RedirectURIs: []string{"http://localhost:12345/callback"},
				},
			},
		},
	})
	authServer.Start(t)

	resourceMux := http.NewServeMux()
	resourceServer := httptest.NewServer(resourceMux)
	t.Cleanup(resourceServer.Close)
	resourceURL := resourceServer.URL + "/resource"

	resourceMux.Handle("/.well-known/oauth-protected-resource/resource", ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:             resourceURL,
		AuthorizationServers: []string{authServer.URL()},
	}))

	handler, err := NewAuthorizationCodeHandler(&AuthorizationCodeHandlerConfig{
		RedirectURL: "http://localhost:12345/callback",
		PreregisteredClientConfig: &PreregisteredClientConfig{
			ClientSecretAuthConfig: &ClientSecretAuthConfig{
				ClientID:     "test_client_id",
				ClientSecret: "test_client_secret",
			},
		},
		AuthorizationCodeFetcher: func(ctx context.Context, args *AuthorizationArgs) (*AuthorizationResult, error) {
			// The fake authorization server will redirect to an URL with code and state.
			client := &http.Client{
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}
			resp, err := client.Get(args.URL)
			if err != nil {
				return nil, fmt.Errorf("failed to visit auth URL: %v", err)
			}
			defer resp.Body.Close()
			dump, err := httputil.DumpResponse(resp, true)
			if err != nil {
				t.Fatalf("failed to dump response: %v", err)
			}
			t.Log(string(dump))

			location, err := resp.Location()
			if err != nil {
				return nil, fmt.Errorf("failed to get location header: %v", err)
			}
			return &AuthorizationResult{
				Code:  location.Query().Get("code"),
				State: location.Query().Get("state"),
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewAuthorizationCodeHandler failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, resourceURL, nil)
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    req,
	}
	resp.Header.Set(
		"WWW-Authenticate",
		"Bearer resource_metadata="+resourceServer.URL+"/.well-known/oauth-protected-resource/resource",
	)

	if err := handler.Authorize(context.Background(), req, resp); err != nil {
		t.Fatalf("Authorize failed: %v", err)
	}

	tokenSource, err := handler.TokenSource(t.Context())
	if err != nil {
		t.Fatalf("Failed to get token source: %v", err)
	}
	token, err := tokenSource.Token()
	if err != nil {
		t.Fatalf("Failed to get token: %v", err)
	}
	if token.AccessToken != "test_access_token" {
		t.Errorf("Expected access token 'test_access_token', got '%s'", token.AccessToken)
	}
}

func TestAuthorize_ForbiddenUnhandledError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", nil)
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    req,
	}
	resp.Header.Set(
		"WWW-Authenticate",
		"Bearer error=invalid_token",
	)
	handler := &AuthorizationCodeHandler{} // No config needed for this test.
	err := handler.Authorize(t.Context(), req, resp)
	if err != nil {
		t.Fatalf("Authorize() failed: %v", err)
	}
}

func TestNewAuthorizationCodeHandler_Success(t *testing.T) {
	simpleHandler := func(ctx context.Context, args *AuthorizationArgs) (*AuthorizationResult, error) {
		return nil, nil
	}
	tests := []struct {
		name   string
		config *AuthorizationCodeHandlerConfig
	}{
		{
			name: "ClientIDMetadataDocumentConfig",
			config: &AuthorizationCodeHandlerConfig{
				ClientIDMetadataDocumentConfig: &ClientIDMetadataDocumentConfig{URL: "https://example.com/client"},
				RedirectURL:                    "https://example.com/callback",
				AuthorizationCodeFetcher:       simpleHandler,
			},
		},
		{
			name: "PreregisteredClientConfig",
			config: &AuthorizationCodeHandlerConfig{
				PreregisteredClientConfig: &PreregisteredClientConfig{
					ClientSecretAuthConfig: &ClientSecretAuthConfig{
						ClientID:     "test_client_id",
						ClientSecret: "test_client_secret",
					},
				},
				RedirectURL:              "https://example.com/callback",
				AuthorizationCodeFetcher: simpleHandler,
			},
		},
		{
			name: "DynamicClientRegistrationConfig_NoRedirectURL",
			config: &AuthorizationCodeHandlerConfig{
				DynamicClientRegistrationConfig: &DynamicClientRegistrationConfig{
					Metadata: &oauthex.ClientRegistrationMetadata{
						RedirectURIs: []string{
							"https://example.com/callback",
						},
					},
				},
				AuthorizationCodeFetcher: simpleHandler,
			},
		},
		{
			name: "DynamicClientRegistrationConfig_WithRedirectURL",
			config: &AuthorizationCodeHandlerConfig{
				DynamicClientRegistrationConfig: &DynamicClientRegistrationConfig{
					Metadata: &oauthex.ClientRegistrationMetadata{
						RedirectURIs: []string{
							"https://example.com/callback",
						},
					},
				},
				RedirectURL:              "https://example.com/callback",
				AuthorizationCodeFetcher: simpleHandler,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewAuthorizationCodeHandler(tt.config); err != nil {
				t.Fatalf("NewAuthorizationCodeHandler failed: %v", err)
			}
		})
	}
}

func TestNewAuthorizationCodeHandler_Error(t *testing.T) {
	validConfig := func() *AuthorizationCodeHandlerConfig {
		return &AuthorizationCodeHandlerConfig{
			ClientIDMetadataDocumentConfig: &ClientIDMetadataDocumentConfig{URL: "https://example.com/client"},
			RedirectURL:                    "https://example.com/callback",
			AuthorizationCodeFetcher: func(ctx context.Context, args *AuthorizationArgs) (*AuthorizationResult, error) {
				return nil, nil
			},
		}
	}
	// Ensure the base config is valid
	if _, err := NewAuthorizationCodeHandler(validConfig()); err != nil {
		t.Fatalf("NewAuthorizationCodeHandler failed: %v", err)
	}

	tests := []struct {
		name   string
		config func() *AuthorizationCodeHandlerConfig
	}{
		{
			name: "NilConfig",
			config: func() *AuthorizationCodeHandlerConfig {
				return nil
			},
		},
		{
			name: "NoRegistrationConfig",
			config: func() *AuthorizationCodeHandlerConfig {
				cfg := validConfig()
				cfg.ClientIDMetadataDocumentConfig = nil
				cfg.PreregisteredClientConfig = nil
				cfg.DynamicClientRegistrationConfig = nil
				return cfg
			},
		},
		{
			name: "MissingRedirectURL",
			config: func() *AuthorizationCodeHandlerConfig {
				cfg := validConfig()
				cfg.RedirectURL = ""
				return cfg
			},
		},
		{
			name: "MissingAuthorizationCodeFetcher",
			config: func() *AuthorizationCodeHandlerConfig {
				cfg := validConfig()
				cfg.AuthorizationCodeFetcher = nil
				return cfg
			},
		},
		{
			name: "InvalidMetadataURL",
			config: func() *AuthorizationCodeHandlerConfig {
				cfg := validConfig()
				cfg.ClientIDMetadataDocumentConfig.URL = "https://example.com"
				return cfg
			},
		},
		{
			name: "InvalidPreregistered_MissingSecretConfig",
			config: func() *AuthorizationCodeHandlerConfig {
				cfg := validConfig()
				cfg.PreregisteredClientConfig = &PreregisteredClientConfig{}
				return cfg
			},
		},
		{
			name: "InvalidPreregistered_EmptyID",
			config: func() *AuthorizationCodeHandlerConfig {
				cfg := validConfig()
				cfg.PreregisteredClientConfig = &PreregisteredClientConfig{
					ClientSecretAuthConfig: &ClientSecretAuthConfig{
						ClientSecret: "secret",
					},
				}
				return cfg
			},
		},
		{
			name: "InvalidPreregistered_EmptySecret",
			config: func() *AuthorizationCodeHandlerConfig {
				cfg := validConfig()
				cfg.PreregisteredClientConfig = &PreregisteredClientConfig{
					ClientSecretAuthConfig: &ClientSecretAuthConfig{
						ClientID: "test_client_id",
					},
				}
				return cfg
			},
		},
		{
			name: "InvalidDynamic_MissingMetadata",
			config: func() *AuthorizationCodeHandlerConfig {
				cfg := validConfig()
				cfg.DynamicClientRegistrationConfig = &DynamicClientRegistrationConfig{}
				return cfg
			},
		},
		{
			name: "InvalidDynamic_InconsistentRedirectURI",
			config: func() *AuthorizationCodeHandlerConfig {
				cfg := validConfig()
				cfg.DynamicClientRegistrationConfig = &DynamicClientRegistrationConfig{
					Metadata: &oauthex.ClientRegistrationMetadata{
						RedirectURIs: []string{"https://example.com/callback1"},
					},
				}
				cfg.RedirectURL = "https://example.com/callback2"
				return cfg
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAuthorizationCodeHandler(tt.config())
			if err == nil {
				t.Errorf("NewAuthorizationCodeHandler() = nil, want error")
			}
		})
	}
}

func TestGetProtectedResourceMetadata(t *testing.T) {
	handler := &AuthorizationCodeHandler{} // No config needed for this method
	pathForChallenge := "/protected-resource"

	tests := []struct {
		name               string
		challengesProvided bool
		prmPath            string
		resourcePath       string
		wantError          bool
	}{
		{
			name:               "FromChallenges",
			challengesProvided: true,
			prmPath:            pathForChallenge,
			resourcePath:       "/resource",
			wantError:          false,
		},
		{
			name:               "FallbackToEndpoint",
			challengesProvided: false,
			prmPath:            "/.well-known/oauth-protected-resource/resource",
			resourcePath:       "/resource",
			wantError:          false,
		},
		{
			name:               "FallbackToRoot",
			challengesProvided: false,
			prmPath:            "/.well-known/oauth-protected-resource",
			resourcePath:       "",
			wantError:          false,
		},
		{
			name:               "NoMetadata",
			challengesProvided: false,
			prmPath:            "/incorrect",
			wantError:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			resourceURL := server.URL + tt.resourcePath
			metadata := &oauthex.ProtectedResourceMetadata{
				Resource:        resourceURL,
				ScopesSupported: []string{"read", "write"},
			}
			mux.Handle(tt.prmPath, ProtectedResourceMetadataHandler(metadata))
			var challenges []oauthex.Challenge
			if tt.challengesProvided {
				challenges = []oauthex.Challenge{
					{
						Scheme: "Bearer",
						Params: map[string]string{
							"resource_metadata": server.URL + pathForChallenge,
						},
					},
				}
			}

			got, err := handler.getProtectedResourceMetadata(t.Context(), challenges, resourceURL)
			if err != nil {
				if !tt.wantError {
					t.Fatalf("getProtectedResourceMetadata() error = %v, want nil", err)
				}
				return
			}
			if got == nil {
				t.Fatal("getProtectedResourceMetadata() got nil, want metadata")
			}
			if diff := cmp.Diff(metadata, got); diff != "" {
				t.Errorf("getProtectedResourceMetadata() metadata mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetAuthServerMetadata(t *testing.T) {
	handler := &AuthorizationCodeHandler{} // No config needed for this method

	tests := []struct {
		name                     string
		authorizationAtMCPServer bool
		issuerPath               string
		endpointConfig           *oauthtest.MetadataEndpointConfig
	}{
		{
			name:                     "OAuthEndpoint_Root",
			authorizationAtMCPServer: false,
			issuerPath:               "",
			endpointConfig: &oauthtest.MetadataEndpointConfig{
				ServeOAuthInsertedEndpoint: true,
			},
		},
		{
			name:                     "OpenIDEndpoint_Root",
			authorizationAtMCPServer: false,
			issuerPath:               "",
			endpointConfig: &oauthtest.MetadataEndpointConfig{
				ServeOpenIDInsertedEndpoint: true,
			},
		},
		{
			name:                     "OAuthEndpoint_Path",
			authorizationAtMCPServer: false,
			issuerPath:               "/oauth",
			endpointConfig: &oauthtest.MetadataEndpointConfig{
				ServeOAuthInsertedEndpoint: true,
			},
		},
		{
			name:                     "OpenIDEndpoint_Path",
			authorizationAtMCPServer: false,
			issuerPath:               "/openid",
			endpointConfig: &oauthtest.MetadataEndpointConfig{
				ServeOpenIDInsertedEndpoint: true,
			},
		},
		{
			name:                     "OpenIDAppendedEndpoint_Path",
			authorizationAtMCPServer: false,
			issuerPath:               "/openid",
			endpointConfig: &oauthtest.MetadataEndpointConfig{
				ServeOpenIDAppendedEndpoint: true,
			},
		},
		{
			name:                     "FallbackToMCPServer",
			authorizationAtMCPServer: true,
		},
		{
			name:       "NoMetadata",
			issuerPath: "",
			endpointConfig: &oauthtest.MetadataEndpointConfig{
				// All metadata endpoints disabled.
				ServeOAuthInsertedEndpoint:  false,
				ServeOpenIDInsertedEndpoint: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := oauthtest.NewFakeAuthorizationServer(oauthtest.Config{
				IssuerPath:             tt.issuerPath,
				MetadataEndpointConfig: tt.endpointConfig,
			})
			s.Start(t)
			issuerURL := s.URL() + tt.issuerPath
			resourceURL := "https://example.com/resource"
			authServers := []string{issuerURL}
			if tt.authorizationAtMCPServer {
				resourceURL = issuerURL
				authServers = nil
			}
			prm := &oauthex.ProtectedResourceMetadata{
				Resource:             resourceURL,
				AuthorizationServers: authServers,
			}

			got, err := handler.getAuthServerMetadata(t.Context(), prm)
			if err != nil {
				t.Fatalf("getAuthServerMetadata() error = %v, want nil", err)
			}
			if got == nil {
				t.Fatal("getAuthServerMetadata() got nil, want metadata")
			}
			if got.Issuer != issuerURL {
				t.Errorf("getAuthServerMetadata() issuer = %q, want %q", got.Issuer, issuerURL)
			}
		})
	}
}

func TestSelectTokenAuthMethod(t *testing.T) {
	tests := []struct {
		name      string
		supported []string
		want      oauth2.AuthStyle
	}{
		{
			name:      "PostPreferredOverBasic",
			supported: []string{"client_secret_basic", "client_secret_post"},
			want:      oauth2.AuthStyleInParams,
		},
		{
			name:      "BasicChosenIfPostNotSupported",
			supported: []string{"private_key_jwt", "client_secret_basic"},
			want:      oauth2.AuthStyleInHeader,
		},
		{
			name:      "NoneSupported",
			supported: []string{"private_key_jwt"},
			want:      oauth2.AuthStyleAutoDetect,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectTokenAuthMethod(tt.supported)
			if got != tt.want {
				t.Errorf("selectTokenAuthMethod() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleRegistration(t *testing.T) {
	tests := []struct {
		name          string
		serverConfig  *oauthtest.RegistrationConfig
		handlerConfig *AuthorizationCodeHandlerConfig
		asm           *oauthex.AuthServerMeta
		want          *resolvedClientConfig
		wantError     bool
	}{
		{
			name: "ClientIDMetadataDocument",
			serverConfig: &oauthtest.RegistrationConfig{
				ClientIDMetadataDocumentSupported: true,
			},
			handlerConfig: &AuthorizationCodeHandlerConfig{
				ClientIDMetadataDocumentConfig: &ClientIDMetadataDocumentConfig{URL: "https://client.example.com"},
			},
			want: &resolvedClientConfig{
				registrationType: registrationTypeClientIDMetadataDocument,
				clientID:         "https://client.example.com",
			},
		},
		{
			name: "Preregistered",
			serverConfig: &oauthtest.RegistrationConfig{
				PreregisteredClients: map[string]oauthtest.ClientInfo{
					"pre_client_id": {
						Secret: "pre_client_secret",
					},
				},
			},
			handlerConfig: &AuthorizationCodeHandlerConfig{
				PreregisteredClientConfig: &PreregisteredClientConfig{
					ClientSecretAuthConfig: &ClientSecretAuthConfig{
						ClientID:     "pre_client_id",
						ClientSecret: "pre_client_secret",
					},
				},
			},
			want: &resolvedClientConfig{
				registrationType: registrationTypePreregistered,
				clientID:         "pre_client_id",
				clientSecret:     "pre_client_secret",
				authStyle:        oauth2.AuthStyleInParams,
			},
		},
		{
			name: "NoneSupported",
			handlerConfig: &AuthorizationCodeHandlerConfig{
				ClientIDMetadataDocumentConfig: &ClientIDMetadataDocumentConfig{URL: "https://client.example.com"},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := oauthtest.NewFakeAuthorizationServer(oauthtest.Config{RegistrationConfig: tt.serverConfig})
			s.Start(t)
			handler := &AuthorizationCodeHandler{config: tt.handlerConfig}
			asm, err := handler.getAuthServerMetadata(t.Context(), &oauthex.ProtectedResourceMetadata{
				AuthorizationServers: []string{s.URL()},
			})
			if err != nil {
				t.Fatalf("getAuthServerMetadata() error = %v, want nil", err)
			}
			got, err := handler.handleRegistration(t.Context(), asm)
			if err != nil {
				if !tt.wantError {
					t.Fatalf("handleRegistration() unexpected error = %v", err)
				}
				return
			}
			if got.registrationType != tt.want.registrationType {
				t.Errorf("handleRegistration() registrationType = %v, want %v", got.registrationType, tt.want.registrationType)
			}
			if got.clientID != tt.want.clientID {
				t.Errorf("handleRegistration() clientID = %q, want %q", got.clientID, tt.want.clientID)
			}
			if got.clientSecret != tt.want.clientSecret {
				t.Errorf("handleRegistration() clientSecret = %q, want %q", got.clientSecret, tt.want.clientSecret)
			}
			if got.authStyle != tt.want.authStyle {
				t.Errorf("handleRegistration() authStyle = %v, want %v", got.authStyle, tt.want.authStyle)
			}
		})
	}
}

func TestDynamicRegistration(t *testing.T) {
	s := oauthtest.NewFakeAuthorizationServer(oauthtest.Config{
		RegistrationConfig: &oauthtest.RegistrationConfig{
			DynamicClientRegistrationEnabled: true,
		},
	})
	s.Start(t)
	handler := &AuthorizationCodeHandler{config: &AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{},
		},
	}}
	asm, err := handler.getAuthServerMetadata(t.Context(), &oauthex.ProtectedResourceMetadata{
		AuthorizationServers: []string{s.URL()},
	})
	if err != nil {
		t.Fatalf("getAuthServerMetadata() error = %v, want nil", err)
	}
	got, err := handler.handleRegistration(t.Context(), asm)
	if err != nil {
		t.Fatalf("handleRegistration() error = %v, want nil", err)
	}
	if got.registrationType != registrationTypeDynamic {
		t.Errorf("handleRegistration() registrationType = %v, want %v", got.registrationType, registrationTypeDynamic)
	}
	if got.clientID == "" {
		t.Errorf("handleRegistration() clientID = %q, want non-empty", got.clientID)
	}
	if got.clientSecret == "" {
		t.Errorf("handleRegistration() clientSecret = %q, want non-empty", got.clientSecret)
	}
	if got.authStyle != oauth2.AuthStyleInHeader {
		t.Errorf("handleRegistration() authStyle = %v, want %v", got.authStyle, oauth2.AuthStyleInHeader)
	}
}
