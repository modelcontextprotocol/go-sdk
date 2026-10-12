// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package oauthex_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

func TestAccessTokenHash_RFC9449(t *testing.T) {
	// RFC 9449 §4.1 example.
	const accessToken = "Kz~8mXK1EalYznwH-LC-1fBAo.4Ljp~zsPE_NeO.gxU"
	const want = "fUHyO2r2Z3DZ53EsNrWBb0xWXoaNy59IiKCAqksmQEo"
	if got := oauthex.AccessTokenHash(accessToken); got != want {
		t.Fatalf("AccessTokenHash = %q, want %q", got, want)
	}
}

func TestJWKThumbprint_RFC9449(t *testing.T) {
	// RFC 9449 §4 / §6.1 example.
	jwk := map[string]string{
		"kty": "EC",
		"x":   "l8tFrhx-34tV3hRICRDY9zCkDlpBhF42UQUfWVAWBFs",
		"y":   "9VE4jf_Ok_o64zbTTlcuNJajHmt6v9TDVrU0CdvGRDA",
		"crv": "P-256",
	}
	const want = "0ZcOCORZNYy-DWpqq30jZyJGHTN0d2HglBV3uiguA4I"
	got, err := oauthex.JWKThumbprint(jwk)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("JWKThumbprint = %q, want %q", got, want)
	}
}

func TestGenerateDPoPKeyPair(t *testing.T) {
	kp, err := oauthex.GenerateDPoPKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if kp.PublicJWK["kty"] != "EC" || kp.PublicJWK["crv"] != "P-256" {
		t.Fatalf("unexpected JWK: %v", kp.PublicJWK)
	}
	if _, ok := kp.PublicJWK["d"]; ok {
		t.Fatal("public JWK must not contain private key")
	}
	thumb, err := oauthex.JWKThumbprint(kp.PublicJWK)
	if err != nil {
		t.Fatal(err)
	}
	if thumb != kp.Thumbprint {
		t.Fatalf("Thumbprint = %q, recomputed %q", kp.Thumbprint, thumb)
	}
}

func TestBuildDPoPProof(t *testing.T) {
	kp, err := oauthex.GenerateDPoPKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	accessToken := "example-access-token"
	proof, err := oauthex.BuildDPoPProof(kp, "POST", "https://example.com/mcp", accessToken)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		t.Fatalf("want 3 JWT parts, got %d", len(parts))
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var header map[string]any
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatal(err)
	}
	if header["typ"] != "dpop+jwt" {
		t.Fatalf("typ = %v", header["typ"])
	}
	if header["alg"] != "ES256" {
		t.Fatalf("alg = %v", header["alg"])
	}
	jwk, ok := header["jwk"].(map[string]any)
	if !ok {
		t.Fatalf("jwk missing: %v", header["jwk"])
	}
	if jwk["d"] != nil {
		t.Fatal("embedded jwk must be public")
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"jti", "htm", "htu", "iat", "ath"} {
		if claims[key] == nil {
			t.Fatalf("missing claim %q", key)
		}
	}
	if claims["htm"] != "POST" {
		t.Fatalf("htm = %v", claims["htm"])
	}
	if claims["htu"] != "https://example.com/mcp" {
		t.Fatalf("htu = %v", claims["htu"])
	}
	if claims["ath"] != oauthex.AccessTokenHash(accessToken) {
		t.Fatalf("ath = %v", claims["ath"])
	}

	// Verify signature with the public key from the JWK.
	x, _ := base64.RawURLEncoding.DecodeString(jwk["x"].(string))
	y, _ := base64.RawURLEncoding.DecodeString(jwk["y"].(string))
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	parsed, err := jwt.Parse(proof, func(token *jwt.Token) (any, error) {
		return pub, nil
	}, jwt.WithValidMethods([]string{"ES256"}))
	if err != nil || !parsed.Valid {
		t.Fatalf("signature verify: %v valid=%v", err, parsed != nil && parsed.Valid)
	}
}

func TestBuildDPoPProof_FreshJTI(t *testing.T) {
	kp, err := oauthex.GenerateDPoPKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	p1, err := oauthex.BuildDPoPProof(kp, "GET", "https://example.com/mcp", "")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := oauthex.BuildDPoPProof(kp, "GET", "https://example.com/mcp", "")
	if err != nil {
		t.Fatal(err)
	}
	jti := func(proof string) string {
		payload, _ := base64.RawURLEncoding.DecodeString(strings.Split(proof, ".")[1])
		var c map[string]any
		_ = json.Unmarshal(payload, &c)
		return c["jti"].(string)
	}
	if jti(p1) == jti(p2) {
		t.Fatal("expected distinct jti values")
	}
}

func TestHTU(t *testing.T) {
	got, err := oauthex.HTU("https://example.com:8443/mcp?x=1#frag")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com:8443/mcp" {
		t.Fatalf("HTU = %q", got)
	}
}

func TestAccessTokenHash_MatchesSHA256(t *testing.T) {
	tok := "abc"
	sum := sha256.Sum256([]byte(tok))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if got := oauthex.AccessTokenHash(tok); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
