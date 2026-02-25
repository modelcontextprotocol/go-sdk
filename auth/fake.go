// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package auth

import (
	"context"
	"net/http"

	"golang.org/x/oauth2"
)

type FakeOAuthHandler struct {
	// Token to be returned via [TokenSource]. If nil, [TokenSource] returns nil.
	Token *oauth2.Token
	// AuthorizeErr is an error to be returned from [Authorize].
	AuthorizeErr error
	// AuthorizeCalled is true if [Authorize] was called.
	AuthorizeCalled bool
}

func (h *FakeOAuthHandler) isOAuthHandler() {}

func (h *FakeOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	if h.Token == nil {
		return nil, nil
	}
	return oauth2.StaticTokenSource(h.Token), nil
}

func (h *FakeOAuthHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	h.AuthorizeCalled = true
	return h.AuthorizeErr
}
