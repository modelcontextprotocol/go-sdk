// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const dpopRedirectURI = "http://127.0.0.1:9876/callback"

// dpopKeyPair is an ES256 (P-256) key pair used to mint DPoP proofs (RFC 9449).
type dpopKeyPair struct {
	private   *ecdsa.PrivateKey
	publicJWK map[string]string
}

func generateDpopKeyPair() (*dpopKeyPair, error) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &dpopKeyPair{
		private: private,
		publicJWK: map[string]string{
			"kty": "EC",
			"crv": "P-256",
			"x":   base64.RawURLEncoding.EncodeToString(padCoord(private.X, 32)),
			"y":   base64.RawURLEncoding.EncodeToString(padCoord(private.Y, 32)),
		},
	}, nil
}

func padCoord(n *big.Int, size int) []byte {
	b := n.Bytes()
	if len(b) >= size {
		return b
	}
	out := make([]byte, size)
	copy(out[size-len(b):], b)
	return out
}

// accessTokenHash returns base64url(SHA-256(ASCII(accessToken))) (RFC 9449 §4.1).
func accessTokenHash(accessToken string) string {
	sum := sha256.Sum256([]byte(accessToken))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// buildDpopProof builds a well-formed dpop+jwt for the given request.
// When accessToken is non-empty, the ath claim is included.
func buildDpopProof(kp *dpopKeyPair, htm, htu, accessToken string) (string, error) {
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", err
	}
	claims := jwt.MapClaims{
		"jti": base64.RawURLEncoding.EncodeToString(jti),
		"htm": htm,
		"htu": htu,
		"iat": time.Now().Unix(),
	}
	if accessToken != "" {
		claims["ath"] = accessTokenHash(accessToken)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["typ"] = "dpop+jwt"
	token.Header["jwk"] = kp.publicJWK
	return token.SignedString(kp.private)
}

func stripQuery(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.Scheme + "://" + u.Host + u.EscapedPath(), nil
}

// runDpopClient exercises SEP-1932 / RFC 9449 baseline DPoP (auth/dpop):
// proof at the token endpoint, Authorization: DPoP on MCP requests, and a
// fresh proof per request. Nonce handling is intentionally omitted.
func runDpopClient(ctx context.Context, serverURL string, _ map[string]any) error {
	kp, err := generateDpopKeyPair()
	if err != nil {
		return fmt.Errorf("generate DPoP key pair: %w", err)
	}

	accessToken, err := acquireDpopBoundToken(ctx, serverURL, kp)
	if err != nil {
		return err
	}
	log.Printf("Obtained DPoP-bound access token")

	httpClient := &http.Client{
		Transport: &dpopRoundTripper{
			base:        http.DefaultTransport,
			kp:          kp,
			accessToken: accessToken,
		},
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "conformance-dpop-client",
		Version: "1.0.0",
	}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   serverURL,
		HTTPClient: httpClient,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("client.Connect(): %w", err)
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

type dpopRoundTripper struct {
	base        http.RoundTripper
	kp          *dpopKeyPair
	accessToken string
}

func (t *dpopRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	htu, err := stripQuery(req.URL.String())
	if err != nil {
		return nil, fmt.Errorf("normalize request URL: %w", err)
	}
	proof, err := buildDpopProof(t.kp, req.Method, htu, t.accessToken)
	if err != nil {
		return nil, fmt.Errorf("build DPoP proof: %w", err)
	}
	req.Header.Set("Authorization", "DPoP "+t.accessToken)
	req.Header.Set("DPoP", proof)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func acquireDpopBoundToken(ctx context.Context, serverURL string, kp *dpopKeyPair) (string, error) {
	httpClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	base, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}

	// 1. Protected Resource Metadata → authorization server issuer.
	prmURL := &url.URL{
		Scheme: base.Scheme,
		Host:   base.Host,
		Path:   "/.well-known/oauth-protected-resource/mcp",
	}
	var prm struct {
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := getJSON(ctx, httpClient, prmURL.String(), &prm); err != nil {
		return "", fmt.Errorf("fetch PRM: %w", err)
	}
	if len(prm.AuthorizationServers) == 0 {
		return "", fmt.Errorf("PRM has no authorization_servers")
	}
	authServerURL := strings.TrimRight(prm.AuthorizationServers[0], "/")

	// 2. Authorization server metadata.
	asMetaURL := authServerURL + "/.well-known/oauth-authorization-server"
	var asMeta struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		RegistrationEndpoint  string `json:"registration_endpoint"`
	}
	if err := getJSON(ctx, httpClient, asMetaURL, &asMeta); err != nil {
		return "", fmt.Errorf("fetch AS metadata: %w", err)
	}
	if asMeta.AuthorizationEndpoint == "" || asMeta.TokenEndpoint == "" || asMeta.RegistrationEndpoint == "" {
		return "", fmt.Errorf("AS metadata missing required endpoints")
	}

	// 3. Dynamic client registration.
	regBody, err := json.Marshal(map[string]any{
		"client_name":      "conformance-dpop-client",
		"redirect_uris":    []string{dpopRedirectURI},
		"application_type": "native",
	})
	if err != nil {
		return "", err
	}
	regReq, err := http.NewRequestWithContext(ctx, http.MethodPost, asMeta.RegistrationEndpoint, strings.NewReader(string(regBody)))
	if err != nil {
		return "", err
	}
	regReq.Header.Set("Content-Type", "application/json")
	regResp, err := httpClient.Do(regReq)
	if err != nil {
		return "", fmt.Errorf("DCR: %w", err)
	}
	defer regResp.Body.Close()
	if regResp.StatusCode < 200 || regResp.StatusCode >= 300 {
		body, _ := io.ReadAll(regResp.Body)
		return "", fmt.Errorf("DCR failed: HTTP %d: %s", regResp.StatusCode, body)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(regResp.Body).Decode(&reg); err != nil {
		return "", fmt.Errorf("decode DCR response: %w", err)
	}
	if reg.ClientID == "" {
		return "", fmt.Errorf("DCR response missing client_id")
	}

	// 4. Authorization request (PKCE). The test AS redirects immediately.
	state, err := randomB64URL(16)
	if err != nil {
		return "", err
	}
	codeVerifier, err := randomB64URL(32)
	if err != nil {
		return "", err
	}
	challengeSum := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(challengeSum[:])

	authorizeURL, err := url.Parse(asMeta.AuthorizationEndpoint)
	if err != nil {
		return "", err
	}
	q := authorizeURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", reg.ClientID)
	q.Set("state", state)
	q.Set("redirect_uri", dpopRedirectURI)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	authorizeURL.RawQuery = q.Encode()

	authReq, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizeURL.String(), nil)
	if err != nil {
		return "", err
	}
	authResp, err := httpClient.Do(authReq)
	if err != nil {
		return "", fmt.Errorf("authorize: %w", err)
	}
	io.Copy(io.Discard, authResp.Body)
	authResp.Body.Close()
	loc := authResp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("authorization endpoint did not redirect with a code")
	}
	locURL, err := url.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("parse redirect: %w", err)
	}
	code := locURL.Query().Get("code")
	if code == "" {
		return "", fmt.Errorf("no authorization code in redirect")
	}

	// 5. Token request with a DPoP proof → DPoP-bound access token.
	tokenHTU, err := stripQuery(asMeta.TokenEndpoint)
	if err != nil {
		return "", err
	}
	proof, err := buildDpopProof(kp, http.MethodPost, tokenHTU, "")
	if err != nil {
		return "", fmt.Errorf("token-request DPoP proof: %w", err)
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {dpopRedirectURI},
		"code_verifier": {codeVerifier},
		"client_id":     {reg.ClientID},
	}
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, asMeta.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenReq.Header.Set("DPoP", proof)
	tokenResp, err := httpClient.Do(tokenReq)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer tokenResp.Body.Close()
	tokenBody, err := io.ReadAll(tokenResp.Body)
	if err != nil {
		return "", err
	}
	if tokenResp.StatusCode < 200 || tokenResp.StatusCode >= 300 {
		return "", fmt.Errorf("token request failed: HTTP %d: %s", tokenResp.StatusCode, tokenBody)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(tokenBody, &tok); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("token response missing access_token")
	}
	if !strings.EqualFold(tok.TokenType, "DPoP") {
		return "", fmt.Errorf("expected token_type DPoP, got %q", tok.TokenType)
	}
	return tok.AccessToken, nil
}

func getJSON(ctx context.Context, client *http.Client, rawURL string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

func randomB64URL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
