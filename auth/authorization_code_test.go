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
	"strings"
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
		PreregisteredClient: &oauthex.ClientCredentials{
			ClientID: "test_client_id",
			ClientSecretAuth: &oauthex.ClientSecretAuth{
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
	handler, err := NewAuthorizationCodeHandler(validConfig())
	if err != nil {
		t.Fatalf("NewAuthorizationCodeHandler failed: %v", err)
	}
	err = handler.Authorize(t.Context(), req, resp)
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
				PreregisteredClient: &oauthex.ClientCredentials{
					ClientID: "test_client_id",
					ClientSecretAuth: &oauthex.ClientSecretAuth{
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
	// Ensure the base config is valid.
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
				cfg.PreregisteredClient = nil
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
				cfg.PreregisteredClient = &oauthex.ClientCredentials{}
				return cfg
			},
		},
		{
			name: "InvalidPreregistered_EmptyID",
			config: func() *AuthorizationCodeHandlerConfig {
				cfg := validConfig()
				cfg.PreregisteredClient = &oauthex.ClientCredentials{
					ClientID: "",
					ClientSecretAuth: &oauthex.ClientSecretAuth{
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
				cfg.PreregisteredClient = &oauthex.ClientCredentials{
					ClientID: "test_client_id",
					ClientSecretAuth: &oauthex.ClientSecretAuth{
						ClientSecret: "",
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

func TestGetProtectedResourceMetadata_Success(t *testing.T) {
	handler, err := NewAuthorizationCodeHandler(validConfig())
	if err != nil {
		t.Fatalf("NewAuthorizationCodeHandler() error = %v", err)
	}

	pathForChallenge := "/protected-resource"

	tests := []struct {
		name               string
		challengesProvided bool
		// Path of the PRM endpoint.
		prmPath string
		// Path of the MCP server that is accessed.
		mcpServerPath string
		// Path for the Resource expected in the returned PRM.
		resourcePath string
	}{
		{
			name:               "FromChallenges",
			challengesProvided: true,
			prmPath:            pathForChallenge,
			mcpServerPath:      "/resource",
			resourcePath:       "/resource",
		},
		{
			name:               "FallbackToEndpoint",
			challengesProvided: false,
			prmPath:            "/.well-known/oauth-protected-resource/resource",
			mcpServerPath:      "/resource",
			resourcePath:       "/resource",
		},
		{
			name:               "FallbackToRoot",
			challengesProvided: false,
			prmPath:            "/.well-known/oauth-protected-resource",
			mcpServerPath:      "/resource",
			resourcePath:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			metadata := &oauthex.ProtectedResourceMetadata{
				Resource:             server.URL + tt.resourcePath,
				AuthorizationServers: []string{"https://oauth.example.com"},
				ScopesSupported:      []string{"read", "write"},
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

			got, err := handler.getProtectedResourceMetadata(t.Context(), challenges, server.URL+tt.mcpServerPath)
			if err != nil {
				t.Fatalf("getProtectedResourceMetadata() error = %v", err)
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

func TestGetProtectedResourceMetadata_Backcompat(t *testing.T) {
	handler, err := NewAuthorizationCodeHandler(validConfig())
	if err != nil {
		t.Fatalf("NewAuthorizationCodeHandler() error = %v", err)
	}
	var challenges []oauthex.Challenge
	got, err := handler.getProtectedResourceMetadata(t.Context(), challenges, "http://localhost:1234/resource")
	if err != nil {
		t.Fatalf("getProtectedResourceMetadata() error = %v", err)
	}
	wantPRM := &oauthex.ProtectedResourceMetadata{
		Resource:             "http://localhost:1234/resource",
		AuthorizationServers: []string{"http://localhost:1234"},
	}
	if diff := cmp.Diff(wantPRM, got); diff != "" {
		t.Errorf("getProtectedResourceMetadata() metadata mismatch (-want +got):\n%s", diff)
	}
}

func TestGetProtectedResourceMetadata_Error(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	metadata := &oauthex.ProtectedResourceMetadata{
		Resource:             server.URL + "/resource",
		AuthorizationServers: nil, // Empty list is invalid
		ScopesSupported:      []string{"read", "write"},
	}
	mux.Handle("/.well-known/oauth-protected-resource/resource", ProtectedResourceMetadataHandler(metadata))
	handler, err := NewAuthorizationCodeHandler(validConfig())
	if err != nil {
		t.Fatalf("NewAuthorizationCodeHandler() error = %v", err)
	}
	var challenges []oauthex.Challenge
	got, err := handler.getProtectedResourceMetadata(t.Context(), challenges, server.URL+"/resource")
	if err == nil || !strings.Contains(err.Error(), "authorization servers") {
		t.Errorf("getProtectedResourceMetadata() = %v, want error containing \"authorization servers\"", err)
	}
	if got != nil {
		t.Errorf("getProtectedResourceMetadata() = %+v, want nil", got)
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
				ClientIDMetadataDocumentConfig: &ClientIDMetadataDocumentConfig{URL: "https://client.example.com/metadata.json"},
			},
			want: &resolvedClientConfig{
				registrationType: registrationTypeClientIDMetadataDocument,
				clientID:         "https://client.example.com/metadata.json",
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
				PreregisteredClient: &oauthex.ClientCredentials{
					ClientID: "pre_client_id",
					ClientSecretAuth: &oauthex.ClientSecretAuth{
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
				ClientIDMetadataDocumentConfig: &ClientIDMetadataDocumentConfig{URL: "https://client.example.com/metadata.json"},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := oauthtest.NewFakeAuthorizationServer(oauthtest.Config{RegistrationConfig: tt.serverConfig})
			s.Start(t)
			tt.handlerConfig.AuthorizationCodeFetcher = func(ctx context.Context, args *AuthorizationArgs) (*AuthorizationResult, error) {
				return nil, nil
			}
			tt.handlerConfig.RedirectURL = "https://example.com/callback"
			handler, err := NewAuthorizationCodeHandler(tt.handlerConfig)
			if err != nil {
				t.Fatalf("NewAuthorizationCodeHandler() error = %v, want nil", err)
			}
			asm, err := GetAuthServerMetadata(t.Context(), s.URL(), http.DefaultClient)
			if err != nil {
				t.Fatalf("GetAuthServerMetadata() unexpected error = %v", err)
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
	handler, err := NewAuthorizationCodeHandler(&AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				RedirectURIs: []string{"https://example.com/callback"},
			},
		},
		RedirectURL: "https://example.com/callback",
		AuthorizationCodeFetcher: func(ctx context.Context, args *AuthorizationArgs) (*AuthorizationResult, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("NewAuthorizationCodeHandler() error = %v", err)
	}
	asm, err := GetAuthServerMetadata(t.Context(), s.URL(), http.DefaultClient)
	if err != nil {
		t.Fatalf("GetAuthServerMetadata() unexpected error = %v", err)
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

// validConfig for test to create an AuthorizationCodeHandler using its constructor.
// Values that are relevant to the test should be set explicitly.
func validConfig() *AuthorizationCodeHandlerConfig {
	return &AuthorizationCodeHandlerConfig{
		ClientIDMetadataDocumentConfig: &ClientIDMetadataDocumentConfig{URL: "https://example.com/client"},
		RedirectURL:                    "https://example.com/callback",
		AuthorizationCodeFetcher: func(ctx context.Context, args *AuthorizationArgs) (*AuthorizationResult, error) {
			return nil, nil
		},
	}
}
