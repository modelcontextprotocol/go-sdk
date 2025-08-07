// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

type TokenInfo struct {
	Scopes     []string
	Expiration time.Time
}

type TokenVerifier func(ctx context.Context, token string) (*TokenInfo, error)

type RequireBearerTokenOptions struct {
	Scopes              []string
	ResourceMetadataURL string
}

var ErrInvalidToken = errors.New("invalid token")

type tokenInfoKey struct{}

// RequireBearerToken returns a piece of middleware that verifies a bearer token using the verifier.
// If verification succeeds, the [TokenInfo] is added to the request's context and the request proceeds.
// If verification fails, the request fails with a 401 Unauthenticated, and the WWW-Authenticate header
// is populated to enable [protected resource metadata].
//
// [protected resource metadata]: https://datatracker.ietf.org/doc/rfc9728
func RequireBearerToken(verifier TokenVerifier, opts *RequireBearerTokenOptions) func(http.Handler) http.Handler {
	// Based on typescript-sdk/src/server/auth/middleware/bearerAuth.ts.

	return func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenInfo, errmsg, code := verify(r.Context(), verifier, opts, r.Header.Get("Authorization"))
			if code != 0 {
				if code == http.StatusUnauthorized || code == http.StatusForbidden {
					if opts != nil && opts.ResourceMetadataURL != "" {
						w.Header().Add("WWW-Authenticate", "Bearer resource_metadata="+opts.ResourceMetadataURL)
					}
				}
				http.Error(w, errmsg, code)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), tokenInfoKey{}, tokenInfo))
			handler.ServeHTTP(w, r)
		})
	}
}

func verify(ctx context.Context, verifier TokenVerifier, opts *RequireBearerTokenOptions, authHeader string) (_ *TokenInfo, errmsg string, code int) {
	// Extract bearer token.
	fields := strings.Fields(authHeader)
	if len(fields) != 2 || strings.ToLower(fields[0]) != "bearer" {
		return nil, "no bearer token", http.StatusUnauthorized
	}

	// Verify the token and get information from it.
	tokenInfo, err := verifier(ctx, fields[1])
	if err != nil {
		if errors.Is(err, ErrInvalidToken) {
			return nil, err.Error(), http.StatusUnauthorized
		}
		// TODO: the TS SDK distinguishes another error, OAuthError, and returns a 400.
		// Investigate how that works.
		// See typescript-sdk/src/server/auth/middleware/bearerAuth.ts.
		return nil, err.Error(), http.StatusInternalServerError
	}

	// Check scopes.
	if opts != nil {
		// Note: quadratic, but N is small.
		for _, s := range opts.Scopes {
			if !slices.Contains(tokenInfo.Scopes, s) {
				return nil, "insufficient scope", http.StatusForbidden
			}
		}
	}

	// Check expiration.
	if tokenInfo.Expiration.IsZero() {
		return nil, "token missing expiration", http.StatusUnauthorized
	}
	if tokenInfo.Expiration.Before(time.Now()) {
		return nil, "token expired", http.StatusUnauthorized
	}
	return tokenInfo, "", 0
}
