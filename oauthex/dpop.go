// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package oauthex

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DPoPConfig configures DPoP (RFC 9449) for an OAuth client. Nonce handling
// is intentionally out of scope for this baseline API.
type DPoPConfig struct {
	// KeyPair signs DPoP proofs. If nil, [GenerateDPoPKeyPair] is used when
	// the config is applied.
	KeyPair *DPoPKeyPair
}

// DPoPKeyPair is an ES256 (P-256) key pair used to mint DPoP proofs.
type DPoPKeyPair struct {
	Private   *ecdsa.PrivateKey
	PublicJWK map[string]string
	// Thumbprint is the RFC 7638 JWK SHA-256 thumbprint (base64url).
	Thumbprint string
}

// GenerateDPoPKeyPair creates a new ES256 key pair for DPoP proofs.
func GenerateDPoPKeyPair() (*DPoPKeyPair, error) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	jwk := map[string]string{
		"kty": "EC",
		"crv": "P-256",
		"x":   base64.RawURLEncoding.EncodeToString(padCoord(private.X, 32)),
		"y":   base64.RawURLEncoding.EncodeToString(padCoord(private.Y, 32)),
	}
	thumb, err := JWKThumbprint(jwk)
	if err != nil {
		return nil, err
	}
	return &DPoPKeyPair{
		Private:    private,
		PublicJWK:  jwk,
		Thumbprint: thumb,
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

// AccessTokenHash returns base64url(SHA-256(ASCII(accessToken))) per RFC 9449 §4.1.
func AccessTokenHash(accessToken string) string {
	sum := sha256.Sum256([]byte(accessToken))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// JWKThumbprint returns the RFC 7638 SHA-256 thumbprint of an EC public JWK.
func JWKThumbprint(jwk map[string]string) (string, error) {
	if jwk["kty"] != "EC" {
		return "", fmt.Errorf("JWKThumbprint: only EC keys are supported")
	}
	// Required members in lexicographic order: crv, kty, x, y.
	canonical, err := json.Marshal(struct {
		Crv string `json:"crv"`
		Kty string `json:"kty"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}{
		Crv: jwk["crv"],
		Kty: jwk["kty"],
		X:   jwk["x"],
		Y:   jwk["y"],
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// HTU returns the HTTP URI for a DPoP htu claim: scheme + host + path,
// with query and fragment removed (RFC 9449 §4.2).
func HTU(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return u.Scheme + "://" + u.Host + u.EscapedPath(), nil
}

// BuildDPoPProof builds a well-formed dpop+jwt for the given request.
// When accessToken is non-empty, the ath claim is included.
func BuildDPoPProof(kp *DPoPKeyPair, htm, htu, accessToken string) (string, error) {
	if kp == nil || kp.Private == nil {
		return "", fmt.Errorf("BuildDPoPProof: nil key pair")
	}
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
		claims["ath"] = AccessTokenHash(accessToken)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["typ"] = "dpop+jwt"
	token.Header["jwk"] = kp.PublicJWK
	return token.SignedString(kp.Private)
}

// DPoPRoundTripper attaches a DPoP proof to OAuth token-endpoint style
// requests (POST with application/x-www-form-urlencoded). Other requests
// pass through unchanged (PRM/ASM GETs, DCR JSON POSTs).
type DPoPRoundTripper struct {
	Base http.RoundTripper
	Key  *DPoPKeyPair
}

// RoundTrip implements [http.RoundTripper].
func (t *DPoPRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if t.Key == nil || req.Method != http.MethodPost {
		return base.RoundTrip(req)
	}
	ct := req.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		return base.RoundTrip(req)
	}
	req = req.Clone(req.Context())
	htu, err := HTU(req.URL.String())
	if err != nil {
		return nil, fmt.Errorf("DPoP htu: %w", err)
	}
	proof, err := BuildDPoPProof(t.Key, req.Method, htu, "")
	if err != nil {
		return nil, fmt.Errorf("DPoP proof: %w", err)
	}
	req.Header.Set("DPoP", proof)
	return base.RoundTrip(req)
}
