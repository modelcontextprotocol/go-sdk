// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Flags.
var (
	serverURL = flag.String("server_url", "http://localhost:8000/mcp", "Server URL")
)

type authResult struct {
	code  string
	state string
	err   error
}

type codeReceiver struct {
	authChan chan authResult
	server   *http.Server
}

func (r *codeReceiver) startAuthorizationFlow(ctx context.Context, authorizationURL string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		code := req.URL.Query().Get("code")
		state := req.URL.Query().Get("state")
		if code == "" {
			http.Error(w, "authorization code not found", http.StatusBadRequest)
			return
		}

		r.authChan <- authResult{
			code:  code,
			state: state,
		}
		fmt.Fprint(w, "Authentication successful. You can close this window.")
	})

	r.server = &http.Server{
		Addr:    "localhost:3142",
		Handler: mux,
	}

	go func() {
		// We ignore ErrServerClosed as it is returned on Shutdown.
		if err := r.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.authChan <- authResult{err: fmt.Errorf("server error: %w", err)}
		}
	}()

	fmt.Printf("Please authorize by visiting: %s\n", authorizationURL)
	return nil
}

func main() {
	flag.Parse()
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	receiver := &codeReceiver{
		authChan: make(chan authResult),
	}

	authHandler := &auth.AuthorizationCodeOAuthHandler{
		RedirectURL: "http://localhost:3142",
		// Uncomment the client configuration you want to use.
		// PreregisteredClientConfig: &auth.PreregisteredClientConfig{
		// 	ClientID:     "",
		// 	ClientSecret: "",
		// },
		// DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{
		// 	Metadata: &oauthex.ClientRegistrationMetadata{
		// 		ClientName: "Dynamically registered MCP client",
		// 		RedirectURIs: []string{"http://localhost:3142"},
		// 		Scope: "read",
		// 	},
		// },
		AuthorizationURLHandler: receiver.startAuthorizationFlow,
	}

	transport := &mcp.StreamableClientTransport{
		Endpoint:     *serverURL,
		OAuthHandler: authHandler,
	}

	ctx := context.Background()
	var session *mcp.ClientSession
	var err error

	for {
		session, err = client.Connect(ctx, transport, nil)
		if err == nil {
			break
		}
		// If the error is ErrRedirected, it means the authorization flow has started
		// and we need to wait for the code.
		if errors.Is(err, auth.ErrRedirected) {
			fmt.Println("Authorization flow started. Waiting for authorization code...")
			res := <-receiver.authChan
			if res.err != nil {
				log.Fatalf("Authorization failed: %v", res.err)
			}

			// Shutdown the temporary server
			if err := receiver.server.Shutdown(ctx); err != nil {
				log.Printf("Failed to shutdown server: %v", err)
			}

			if err := authHandler.FinalizeAuthorization(res.code, res.state); err != nil {
				log.Fatalf("Failed to finalize authorization: %v", err)
			}
			continue
		}
		log.Fatalf("client.Connect(): %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		log.Fatalf("session.ListTools(): %v", err)
	}
	log.Println("Tools:")
	for _, tool := range tools.Tools {
		log.Printf("- %q", tool.Name)
	}
}
