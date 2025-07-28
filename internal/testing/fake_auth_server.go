package testing

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
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

// FakeAuthServer is a fake OAuth2 authorization server.
type FakeAuthServer struct {
	server    *http.Server
	authCodes map[string]authCodeInfo
}

func NewFakeAuthServer() *FakeAuthServer {
	server := &FakeAuthServer{
		authCodes: make(map[string]authCodeInfo),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", server.handleMetadata)
	mux.HandleFunc("/authorize", server.handleAuthorize)
	mux.HandleFunc("/token", server.handleToken)
	server.server = &http.Server{
		Addr:    authServerPort,
		Handler: mux,
	}
	return server
}

func (s *FakeAuthServer) Start() {
	go func() {
		if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe(): %v", err)
		}
	}()
}

func (s *FakeAuthServer) Stop() {
	if err := s.server.Close(); err != nil {
		log.Printf("Failed to stop server: %v", err)
	}
}

func (s *FakeAuthServer) handleMetadata(w http.ResponseWriter, r *http.Request) {
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

func (s *FakeAuthServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return
	}
	if codeChallenge == "" || codeChallengeMethod != "S256" {
		http.Error(w, "invalid_request", http.StatusBadRequest)
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

func (s *FakeAuthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	grantType := r.Form.Get("grant_type")
	code := r.Form.Get("code")
	redirectURI := r.Form.Get("redirect_uri")
	codeVerifier := r.Form.Get("code_verifier")

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

	if authCodeInfo.redirectURI != redirectURI {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
		return
	}

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
