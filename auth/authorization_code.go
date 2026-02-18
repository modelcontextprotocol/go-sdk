// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// ErrRedirected is returned when the user was redirected to the authorization server.
var ErrRedirected = errors.New("redirected")

// ClientIDMetadataDocumentConfig is used to configure the Client ID Metadata Document
// based client registration per
// https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization#client-id-metadata-documents.
// See https://client.dev/ for more information.
type ClientIDMetadataDocumentConfig struct {
	// URL is the client identifier URL as per
	// https://datatracker.ietf.org/doc/html/draft-ietf-oauth-client-id-metadata-document-00#section-3.
	URL string
}

// PreregisteredClientConfig is used to configure a pre-registered client per
// https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization#preregistration.
type PreregisteredClientConfig struct {
	ClientID     string
	ClientSecret string
	AuthStyle    oauth2.AuthStyle
}

// DynamicClientRegistrationConfig is used to configure dynamic client registration per
// https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization#dynamic-client-registration.
type DynamicClientRegistrationConfig struct {
	// Metadata to be used in dynamic client registration request as per
	// https://datatracker.ietf.org/doc/html/rfc7591#section-2.
	Metadata *oauthex.ClientRegistrationMetadata
}

type registrationType int

const (
	registrationTypeClientIDMetadataDocument registrationType = iota
	registrationTypePreregistered
	registrationTypeDynamic
)

type resolvedClientConfig struct {
	registrationType registrationType
	clientID         string
	clientSecret     string
	authStyle        oauth2.AuthStyle
}

type AuthorizationCodeOAuthHandler struct {
	// Client registration configuration.
	// It is attempted in the following order:
	// 1. Client ID Metadata Document
	// 2. Preregistration
	// 3. Dynamic Client Registration
	ClientIDMetadataDocumentConfig  *ClientIDMetadataDocumentConfig
	PreregisteredClientConfig       *PreregisteredClientConfig
	DynamicClientRegistrationConfig *DynamicClientRegistrationConfig

	// RedirectURL is the URL to redirect to after authorization.
	// If Dynamic Client Registration is used, the RedirectURL must be consistent
	// with [DynamicClientRegistrationConfig.Metadata.RedirectURIs].
	RedirectURL string

	// AuthorizationURLHandler is called to handle the authorization URL.
	// It is responsible for opening the URL in a browser.
	// It should return once the redirect has been issued.
	// The redirect callback should be handled by the caller and the authorization code
	// should be set by calling [SetAuthorizationCode] before retrying the request.
	AuthorizationURLHandler func(ctx context.Context, authorizationURL string) error

	// StateProvider is an optional function to generate a state string for authorization
	// requests. If not provided, a random string will be generated.
	// The state should be validated on the redirect callback.
	StateProvider func() string

	// resolvedClientConfig used during the authorization flow.
	resolvedClientConfig *resolvedClientConfig
	// tokenSource is the token source to use for authorization.
	// It can be prepopulated by calling [SetTokenSource].
	tokenSource oauth2.TokenSource
	// codeVerifier is the PKCE code verifier.
	codeVerifier string
	// authorizationCode is the authorization code obtained from the authorization server.
	authorizationCode string
	// state is the state string used in the authorization request.
	state string
}

func (h *AuthorizationCodeOAuthHandler) isOAuthHandler() {}

func (h *AuthorizationCodeOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	return h.tokenSource, nil
}

// TODO: extract some logic into helper functions.
// TODO: validate required args
func (h *AuthorizationCodeOAuthHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	defer resp.Body.Close()
	log.Printf("Authorize: %s %s", req.Method, req.URL)

	if h.resolvedClientConfig == nil && h.authorizationCode != "" {
		return fmt.Errorf("exchanging authorization code with unregistered client is not allowed")
	}

	resourceURL := req.URL.String()
	challenges, err := oauthex.ParseWWWAuthenticate(resp.Header[http.CanonicalHeaderKey("WWW-Authenticate")])
	if err != nil {
		return fmt.Errorf("failed to parse WWW-Authenticate header: %v", err)
	}
	log.Printf("WWW-Authenticate header: %v", challenges)
	var prm *oauthex.ProtectedResourceMetadata
	for _, url := range oauthex.ProtectedResourceMetadataURLs(oauthex.ResourceMetadataURL(challenges), resourceURL) {
		var err error
		log.Printf("Getting protected resource metadata from %q", url)
		prm, err = oauthex.GetProtectedResourceMetadata(ctx, url, http.DefaultClient)
		if err == nil {
			break
		}
		log.Printf("Failed to get protected resource metadata from %q: %v", url, err)
	}
	var authServerURL string
	if prm != nil && len(prm.AuthorizationServers) > 0 {
		// Use the first authorization server, similarly to other SDKs.
		authServerURL = prm.AuthorizationServers[0]
	} else {
		// Fallback to 2025-03-26 spec: MCP server base URL acts as Authorization Server.
		authURL, err := url.Parse(resourceURL)
		if err != nil {
			return fmt.Errorf("failed to parse resource URL: %v", err)
		}
		authURL.Path = ""
		authServerURL = authURL.String()
	}
	log.Printf("Authorization server URL: %s", authServerURL)

	asm, err := oauthex.GetAuthServerMeta(ctx, authServerURL, http.DefaultClient)
	if err != nil {
		return fmt.Errorf("failed to get authorization server metadata: %w", err)
	}
	log.Print("Authorization server medatada fetched")

	if err := h.handleRegistration(ctx, authServerURL, asm); err != nil {
		return err
	}

	scopes := oauthex.Scopes(challenges)
	if len(scopes) == 0 && prm != nil && len(prm.ScopesSupported) > 0 {
		scopes = prm.ScopesSupported
	}

	var authorizationEndpoint, tokenEndpoint string
	if asm != nil {
		authorizationEndpoint = asm.AuthorizationEndpoint
		tokenEndpoint = asm.TokenEndpoint
	} else {
		// Fallback to 2025-03-26 spec: predefined endpoints if not provided by AS.
		authorizationEndpoint = authServerURL + "/authorize"
		tokenEndpoint = authServerURL + "/token"
	}

	cfg := &oauth2.Config{
		ClientID:     h.resolvedClientConfig.clientID,
		ClientSecret: h.resolvedClientConfig.clientSecret,

		Endpoint: oauth2.Endpoint{
			AuthURL:  authorizationEndpoint,
			TokenURL: tokenEndpoint,
			// TODO: validate if the auth style is supported by the AS.
			AuthStyle: h.resolvedClientConfig.authStyle,
		},
		RedirectURL: h.RedirectURL,
		Scopes:      scopes,
	}

	if h.authorizationCode != "" {
		log.Print("Authorization code is available, exchanging for token")
		opts := []oauth2.AuthCodeOption{
			oauth2.VerifierOption(h.codeVerifier),
			oauth2.SetAuthURLParam("resource", req.URL.String()),
		}
		token, err := cfg.Exchange(ctx, h.authorizationCode, opts...)
		defer func() {
			// Authorization code has been consumed, clear it.
			h.authorizationCode = ""
		}()
		if err != nil {
			return fmt.Errorf("token exchange failed: %w", err)
		}
		h.tokenSource = cfg.TokenSource(ctx, token)
		return nil
	}

	h.codeVerifier = oauth2.GenerateVerifier()
	h.state = rand.Text()
	if h.StateProvider != nil {
		h.state = h.StateProvider()
	}

	authURL := cfg.AuthCodeURL(h.state,
		oauth2.S256ChallengeOption(h.codeVerifier),
		oauth2.SetAuthURLParam("resource", req.URL.String()),
	)

	log.Print("No authorization code available, opening authorization URL")
	if h.AuthorizationURLHandler != nil {
		if err := h.AuthorizationURLHandler(ctx, authURL); err != nil {
			return fmt.Errorf("authorization URL handler failed: %w", err)
		}
	}

	return ErrRedirected
}

func (h *AuthorizationCodeOAuthHandler) FinalizeAuthorization(code, state string) error {
	defer func() {
		// State has been used for validation, clear it.
		h.state = ""
	}()
	if state != h.state {
		return fmt.Errorf("state mismatch: expected %q, got %q", h.state, state)
	}
	h.authorizationCode = code
	return nil
}

func (h *AuthorizationCodeOAuthHandler) handleRegistration(ctx context.Context, authServerURL string, asm *oauthex.AuthServerMeta) error {
	// 1. Attempt to use Client ID Metadata Document (SEP-991).
	cimdCfg := h.ClientIDMetadataDocumentConfig
	if cimdCfg != nil {
		supportsCIMD := asm != nil && asm.ClientIDMetadataDocumentSupported
		if supportsCIMD {
			if !isNonRootHTTPSURL(cimdCfg.URL) {
				return fmt.Errorf("client ID metadata document URL is not a non-root HTTPS URL")
			}
			h.resolvedClientConfig = &resolvedClientConfig{
				registrationType: registrationTypeClientIDMetadataDocument,
				clientID:         cimdCfg.URL,
			}
			return nil
		}
	}
	// 2. Attempt to use pre-registered client ID.
	pCfg := h.PreregisteredClientConfig
	if pCfg != nil {
		if pCfg.ClientID == "" || pCfg.ClientSecret == "" {
			return fmt.Errorf("pre-registered client ID or secret is empty")
		}
		h.resolvedClientConfig = &resolvedClientConfig{
			registrationType: registrationTypePreregistered,
			clientID:         pCfg.ClientID,
			clientSecret:     pCfg.ClientSecret,
			authStyle:        pCfg.AuthStyle,
		}
		return nil
	}
	// 3. Attempt to use dynamic client registration.
	dcrCfg := h.DynamicClientRegistrationConfig
	if dcrCfg != nil {
		if !slices.Contains(dcrCfg.Metadata.RedirectURIs, h.RedirectURL) {
			return fmt.Errorf("redirect URI %q is not in the list of allowed redirect URIs for dynamic client registration", h.RedirectURL)
		}
		var registrationEndpoint string
		if asm != nil {
			if asm.RegistrationEndpoint == "" {
				return fmt.Errorf("authorization server does not support dynamic client registration")
			}
			registrationEndpoint = asm.RegistrationEndpoint
		} else {
			// Fallback to 2025-03-26 spec: predefined endpoints if not provided by AS.
			registrationEndpoint = authServerURL + "/register"
		}
		log.Printf("Attempting dynamic client registration at %v", registrationEndpoint)
		regResp, err := oauthex.RegisterClient(ctx, registrationEndpoint, dcrCfg.Metadata, http.DefaultClient)
		if err != nil {
			return fmt.Errorf("failed to register client: %w", err)
		}
		h.resolvedClientConfig = &resolvedClientConfig{
			registrationType: registrationTypeDynamic,
			clientID:         regResp.ClientID,
			clientSecret:     regResp.ClientSecret,
		}
		switch regResp.TokenEndpointAuthMethod {
		case "client_secret_post":
			h.resolvedClientConfig.authStyle = oauth2.AuthStyleInParams
		case "client_secret_basic":
			h.resolvedClientConfig.authStyle = oauth2.AuthStyleInHeader
		case "none":
			// "none" is equivalent to "client_secret_post" but without sending client secret.
			h.resolvedClientConfig.authStyle = oauth2.AuthStyleInParams
			h.resolvedClientConfig.clientSecret = ""
		default:
			// We leave the AuthStyle set to zero value, which is auto-detection.
		}
		log.Printf("Client registered with client ID: %s", regResp.ClientID)
		return nil
	}
	return fmt.Errorf("no client registration method configured")
}

func isNonRootHTTPSURL(u string) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return false
	}
	return pu.Scheme == "https" && pu.Path != ""
}

var _ OAuthHandler = (*AuthorizationCodeOAuthHandler)(nil)
