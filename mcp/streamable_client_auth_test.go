// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/oauth2"
)

type mockOAuthHandler struct {
	// Embed to satisfy the interface.
	auth.AuthorizationCodeHandler

	token           *oauth2.Token
	authorizeErr    error
	authorizeCalled bool
}

func (h *mockOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	if h.token == nil {
		return nil, nil
	}
	return oauth2.StaticTokenSource(h.token), nil
}

func (h *mockOAuthHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	h.authorizeCalled = true
	return h.authorizeErr
}

func TestStreamableClientOAuth_AuthorizationHeader(t *testing.T) {
	ctx := context.Background()
	token := &oauth2.Token{AccessToken: "test-token"}
	oauthHandler := &mockOAuthHandler{token: token}

	fake := &fakeStreamableServer{
		t: t,
		responses: fakeResponses{
			{"POST", "", methodInitialize, ""}: {
				header: header{
					"Content-Type":  "application/json",
					sessionIDHeader: "123",
				},
				body: jsonBody(t, initResp),
			},
			{"POST", "123", notificationInitialized, ""}: {
				status:              http.StatusAccepted,
				wantProtocolVersion: latestProtocolVersion,
			},
			{"GET", "123", "", ""}: {
				header: header{
					"Content-Type": "text/event-stream",
				},
			},
			{"DELETE", "123", "", ""}: {},
		},
	}
	verifier := func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		if token != "test-token" {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{Expiration: time.Now().Add(time.Hour)}, nil
	}
	httpServer := httptest.NewServer(auth.RequireBearerToken(verifier, nil)(fake))
	t.Cleanup(httpServer.Close)

	transport := &StreamableClientTransport{
		Endpoint:     httpServer.URL,
		OAuthHandler: oauthHandler,
	}
	client := NewClient(testImpl, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	session.Close()
}

func TestStreamableClientOAuth_401(t *testing.T) {
	ctx := context.Background()
	oauthHandler := &mockOAuthHandler{token: nil}

	fake := &fakeStreamableServer{
		t: t,
		responses: fakeResponses{
			{"POST", "", methodInitialize, ""}: {
				header: header{
					"Content-Type":  "application/json",
					sessionIDHeader: "123",
				},
				body: jsonBody(t, initResp),
			},
		},
	}
	verifier := func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		// Accept any token.
		return &auth.TokenInfo{Expiration: time.Now().Add(time.Hour)}, nil
	}
	httpServer := httptest.NewServer(auth.RequireBearerToken(verifier, nil)(fake))
	t.Cleanup(httpServer.Close)

	transport := &StreamableClientTransport{
		Endpoint:     httpServer.URL,
		OAuthHandler: oauthHandler,
	}
	client := NewClient(testImpl, nil)
	_, err := client.Connect(ctx, transport, nil)
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("client.Connect() error does not contain 'Unauthorized': %v", err)
	}

	if !oauthHandler.authorizeCalled {
		t.Errorf("expected Authorize to be called")
	}
}

func TestTokenInfo(t *testing.T) {
	ctx := context.Background()

	// Create a server with a tool that returns TokenInfo.
	tokenInfo := func(ctx context.Context, req *CallToolRequest, _ struct{}) (*CallToolResult, any, error) {
		return &CallToolResult{Content: []Content{&TextContent{Text: fmt.Sprintf("%v", req.Extra.TokenInfo)}}}, nil, nil
	}
	server := NewServer(testImpl, nil)
	AddTool(server, &Tool{Name: "tokenInfo", Description: "return token info"}, tokenInfo)

	streamHandler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
	verifier := func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		if token != "test-token" {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{
			Scopes: []string{"scope"},
			// Expiration is far, far in the future.
			Expiration: time.Date(5000, 1, 2, 3, 4, 5, 0, time.UTC),
		}, nil
	}
	handler := auth.RequireBearerToken(verifier, nil)(streamHandler)
	httpServer := httptest.NewServer(mustNotPanic(t, handler))
	defer httpServer.Close()

	transport := &StreamableClientTransport{
		Endpoint:     httpServer.URL,
		OAuthHandler: &mockOAuthHandler{token: &oauth2.Token{AccessToken: "test-token"}},
	}
	client := NewClient(testImpl, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &CallToolParams{Name: "tokenInfo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) == 0 {
		t.Fatal("missing content")
	}
	tc, ok := res.Content[0].(*TextContent)
	if !ok {
		t.Fatal("not TextContent")
	}
	if g, w := tc.Text, "&{[scope] 5000-01-02 03:04:05 +0000 UTC  map[]}"; g != w {
		t.Errorf("got %q, want %q", g, w)
	}
}
