// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package testing

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	authServerPort = ":8080"
	issuer         = "http://localhost" + authServerPort
	tokenExpiry    = time.Hour
)

var jwtSigningKey = []byte("fake-secret-key")

type authCodeInfo struct {
	codeChallenge string
	redirectURI   string
}

type state struct {
	authCodes map[string]authCodeInfo
}

// NewFakeAuthMux constructs a ServeMux that implements an OAuth 2.1 authentication
// server. It should be used with [httptest.NewTLSServer].
func NewFakeAuthMux() *http.ServeMux {
	s := &state{authCodes: make(map[string]authCodeInfo)}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", s.handleMetadata)
	mux.HandleFunc("/authorize", s.handleAuthorize)
	mux.HandleFunc("/token", s.handleToken)
	return mux
}

func (s *state) handleMetadata(w http.ResponseWriter, r *http.Request) {
	issuer := "https://localhost:" + r.URL.Port()
	metadata := map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/authorize",
		"token_endpoint":                        issuer + "/token",
		"jwks_uri":                              issuer + "/.well-known/jwks.json",
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metadata)
}

func (s *state) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	responseType := query.Get("response_type")
	redirectURI := query.Get("redirect_uri")
	codeChallenge := query.Get("code_challenge")
	codeChallengeMethod := query.Get("code_challenge_method")

	if responseType != "code" {
		http.Error(w, "unsupported_response_type", http.StatusBadRequest)
		return
	}
	if redirectURI == "" {
		http.Error(w, "invalid_request (no redirect_uri)", http.StatusBadRequest)
		return
	}
	if codeChallenge == "" || codeChallengeMethod != "S256" {
		http.Error(w, "invalid_request (code challenge is not S256)", http.StatusBadRequest)
		return
	}
	if query.Get("client_id") == "" {
		http.Error(w, "invalid_request (missing client_id)", http.StatusBadRequest)
		return
	}

	authCode := "fake-auth-code-" + fmt.Sprintf("%d", time.Now().UnixNano())
	s.authCodes[authCode] = authCodeInfo{
		codeChallenge: codeChallenge,
		redirectURI:   redirectURI,
	}

	redirectURL := fmt.Sprintf("%s?code=%s&state=%s", redirectURI, authCode, query.Get("state"))
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (s *state) handleToken(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	grantType := r.Form.Get("grant_type")
	code := r.Form.Get("code")
	codeVerifier := r.Form.Get("code_verifier")
	// Ignore redirect_uri; it is not required in 2.1.
	// https://www.ietf.org/archive/id/draft-ietf-oauth-v2-1-13.html#redirect-uri-in-token-request

	if grantType != "authorization_code" {
		http.Error(w, "unsupported_grant_type", http.StatusBadRequest)
		return
	}

	authCodeInfo, ok := s.authCodes[code]
	if !ok {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
		return
	}
	delete(s.authCodes, code)

	// PKCE verification
	hasher := sha256.New()
	hasher.Write([]byte(codeVerifier))
	calculatedChallenge := base64.RawURLEncoding.EncodeToString(hasher.Sum(nil))
	if calculatedChallenge != authCodeInfo.codeChallenge {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
		return
	}

	// Issue JWT
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": issuer,
		"sub": "fake-user-id",
		"aud": "fake-client-id",
		"exp": now.Add(tokenExpiry).Unix(),
		"iat": now.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	accessToken, err := token.SignedString(jwtSigningKey)
	if err != nil {
		http.Error(w, "server_error", http.StatusInternalServerError)
		return
	}

	tokenResponse := map[string]any{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   int(tokenExpiry.Seconds()),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tokenResponse)
}
