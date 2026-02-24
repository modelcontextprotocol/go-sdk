// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package auth

import (
	"context"
	"net/http"

	"golang.org/x/oauth2"
)

type OAuthHandler interface {
	isOAuthHandler()

	// TokenSource returns a token source to be used for outgoing requests.
	// Returned token source might be nil. In that case, the transport will not
	// add any authorization headers to the request.
	TokenSource(context.Context) (oauth2.TokenSource, error)

	// Authorize is called when an HTTP request results in an error that may
	// be addressed by the authorization flow (currently 401 Unauthorized and 403 Forbidden).
	// It is responsible for performing the OAuth flow to obtain an access token.
	// The arguments are the request that failed and the response that was received for it.
	// If the returned error is nil, [TokenSource] is expected to return a non-nil token source.
	// After a successful call to [Authorize], the HTTP request should be retried by the transport.
	// The function is responsible for closing the response body.
	Authorize(context.Context, *http.Request, *http.Response) error
}
