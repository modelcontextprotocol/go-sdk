// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package oauthtest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	internaljson "github.com/modelcontextprotocol/go-sdk/internal/json"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

type ClientInfo struct {
	Secret       string
	RedirectURIs []string
}

type MetadataEndpointConfig struct {
	// Whether to serve the OAuth Authorization Server Metadata at
	// /.well-known/oauth-authorization-server + issuerPath.
	ServeOAuthInsertedEndpoint bool
	// Whether to serve the OAuth Authorization Server Metadata at
	// /.well-known/openid-configuration + issuerPath.
	ServeOpenIDInsertedEndpoint bool
	// Whether to serve the OAuth Authorization Server Metadata at
	// issuerPath + /.well-known/openid-configuration.
	// Should be used when issuerPath is not empty.
	ServeOpenIDAppendedEndpoint bool
}

type RegistrationConfig struct {
	// Whether the client ID metadata document is supported.
	ClientIDMetadataDocumentSupported bool
	// PreregisteredClients is a map of valid ClientIDs to ClientSecrets.
	PreregisteredClients map[string]ClientInfo
	// Whether dynamic client registration is enabled.
	DynamicClientRegistrationEnabled bool
}

// Config holds configuration for FakeAuthorizationServer.
type Config struct {
	// The optional path component of the issuer URL.
	// If non-empty, it should start with a "/". It should not end with a "/".
	// It affects the paths of the server endpoints.
	IssuerPath string
	// Configuration of the metadata endpoint.
	MetadataEndpointConfig *MetadataEndpointConfig
	// Configuration for client registration.
	RegistrationConfig *RegistrationConfig
}

// FakeAuthorizationServer is a fake OAuth 2.0 Authorization Server for testing.
type FakeAuthorizationServer struct {
	server  *httptest.Server
	Mux     *http.ServeMux
	config  Config
	clients map[string]ClientInfo
	codes   map[string]codeInfo
}

type codeInfo struct {
	CodeChallenge string
}

// NewFakeAuthorizationServer creates a new FakeAuthorizationServer.
// The server is simple and should not be used outside of testing.
// It supports:
// - Only the authorization Code Grant
// - PKCE verification
// - Client tracking & dynamic registration
// - Client authentication
func NewFakeAuthorizationServer(config Config) *FakeAuthorizationServer {
	s := &FakeAuthorizationServer{
		Mux:    http.NewServeMux(),
		config: config,
		codes:  make(map[string]codeInfo),
	}
	if config.RegistrationConfig != nil {
		s.clients = maps.Clone(config.RegistrationConfig.PreregisteredClients)
	}
	if s.clients == nil {
		s.clients = make(map[string]ClientInfo)
	}

	s.Mux.HandleFunc(s.config.IssuerPath+"/authorize", s.handleAuthorize)
	s.Mux.HandleFunc(s.config.IssuerPath+"/token", s.handleToken)
	if config.MetadataEndpointConfig != nil {
		if config.MetadataEndpointConfig.ServeOAuthInsertedEndpoint {
			s.Mux.HandleFunc("/.well-known/oauth-authorization-server"+s.config.IssuerPath, s.handleMetadata)
		}
		if config.MetadataEndpointConfig.ServeOpenIDInsertedEndpoint {
			s.Mux.HandleFunc("/.well-known/openid-configuration"+s.config.IssuerPath, s.handleMetadata)
		}
		if config.MetadataEndpointConfig.ServeOpenIDAppendedEndpoint && s.config.IssuerPath != "" {
			s.Mux.HandleFunc(s.config.IssuerPath+"/.well-known/openid-configuration", s.handleMetadata)
		}
	} else {
		// Serve the default OAuth endpoint.
		s.Mux.HandleFunc("/.well-known/oauth-authorization-server", s.handleMetadata)
	}
	if config.RegistrationConfig != nil && config.RegistrationConfig.DynamicClientRegistrationEnabled {
		s.Mux.HandleFunc(s.config.IssuerPath+"/register", s.handleRegister)
	}
	s.server = httptest.NewUnstartedServer(s.Mux)

	return s
}

// Start starts the HTTP server and registers a cleanup function on t to close the server.
func (s *FakeAuthorizationServer) Start(t testing.TB) {
	s.server.Start()
	t.Cleanup(s.server.Close)
}

// URL returns the base URL of the server (Issuer).
func (s *FakeAuthorizationServer) URL() string {
	return s.server.URL
}

func (s *FakeAuthorizationServer) handleMetadata(w http.ResponseWriter, r *http.Request) {
	cimdSupported := false
	var registrationEndpoint string
	if s.config.RegistrationConfig != nil {
		cimdSupported = s.config.RegistrationConfig.ClientIDMetadataDocumentSupported
		if s.config.RegistrationConfig.DynamicClientRegistrationEnabled {
			registrationEndpoint = s.URL() + s.config.IssuerPath + "/register"
		}
	}
	meta := &oauthex.AuthServerMeta{
		Issuer:                            s.URL() + s.config.IssuerPath,
		AuthorizationEndpoint:             s.URL() + s.config.IssuerPath + "/authorize",
		TokenEndpoint:                     s.URL() + s.config.IssuerPath + "/token",
		RegistrationEndpoint:              registrationEndpoint,
		ResponseTypesSupported:            []string{"code"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		ClientIDMetadataDocumentSupported: cimdSupported,
		TokenEndpointAuthMethodsSupported: []string{"client_secret_post", "client_secret_basic"},
	}
	// Set CORS headers for cross-origin client discovery.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle CORS preflight requests
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Only GET allowed for metadata retrieval
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(meta); err != nil {
		http.Error(w, "Failed to encode metadata", http.StatusInternalServerError)
		return
	}
}

func (s *FakeAuthorizationServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var metadata oauthex.ClientRegistrationMetadata
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	if err := internaljson.Unmarshal(body, &metadata); err != nil {
		http.Error(w, "failed to parse request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	clientID := rand.Text()
	ci := ClientInfo{
		Secret:       rand.Text(),
		RedirectURIs: metadata.RedirectURIs,
	}
	s.clients[clientID] = ci
	metadata.TokenEndpointAuthMethod = "client_secret_basic"
	json.NewEncoder(w).Encode(&oauthex.ClientRegistrationResponse{
		ClientID:                   clientID,
		ClientSecret:               ci.Secret,
		ClientRegistrationMetadata: metadata,
	})
}

func (s *FakeAuthorizationServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clientID := r.URL.Query().Get("client_id")
	clientInfo, ok := s.clients[clientID]
	if !ok {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}

	redirectURI := r.URL.Query().Get("redirect_uri")
	if redirectURI == "" {
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		return
	}
	if !slices.Contains(clientInfo.RedirectURIs, redirectURI) {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	codeChallenge := r.URL.Query().Get("code_challenge")
	if codeChallenge == "" {
		http.Error(w, "missing code_challenge", http.StatusBadRequest)
		return
	}
	code := rand.Text()
	s.codes[code] = codeInfo{
		CodeChallenge: codeChallenge,
	}

	state := r.URL.Query().Get("state")

	redirectURL := fmt.Sprintf("%s?code=%s&state=%s", redirectURI, code, state)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (s *FakeAuthorizationServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.authenticateClient(r); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Form.Get("grant_type") != "authorization_code" {
		http.Error(w, "invalid grant_type", http.StatusBadRequest)
		return
	}
	code := r.Form.Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	codeInfo, ok := s.codes[code]
	if !ok {
		http.Error(w, "unknown authorization code", http.StatusBadRequest)
		return
	}
	verifier := r.Form.Get("code_verifier")
	if verifier == "" {
		http.Error(w, "missing code_verifier", http.StatusBadRequest)
		return
	}
	sha := sha256.Sum256([]byte(verifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(sha[:])
	if expectedChallenge != codeInfo.CodeChallenge {
		http.Error(w, "PKCE verification failed", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token": "test_access_token",
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

func (s *FakeAuthorizationServer) authenticateClient(r *http.Request) error {
	clientID, clientSecret, ok := r.BasicAuth()
	if !ok {
		clientID = r.Form.Get("client_id")
		clientSecret = r.Form.Get("client_secret")
	}

	clientInfo, ok := s.clients[clientID]
	if !ok || clientInfo.Secret != clientSecret {
		return errors.New("client not found")
	}
	return nil
}
