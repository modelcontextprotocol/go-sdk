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
	// ClientID and ClientSecret to be used for client authentication.
	ClientID     string
	ClientSecret string
	// AuthStyle is an optional client authentication method.
	// See [oauth2.AuthStyleAutoDetect] for the documentation of the zero value.
	AuthStyle oauth2.AuthStyle
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

// AuthorizationCodeOAuthHandler is an implementation of [OAuthHandler] that uses
// the authorization code flow to obtain access tokens.
// The handler is stateful and can only handle one authorization flow at a time.
type AuthorizationCodeOAuthHandler struct {
	// Client registration configuration.
	// It is attempted in the following order:
	//
	//   1. Client ID Metadata Document
	//   2. Preregistration
	//   3. Dynamic Client Registration
	//
	// At least one method must be configured.
	ClientIDMetadataDocumentConfig  *ClientIDMetadataDocumentConfig
	PreregisteredClientConfig       *PreregisteredClientConfig
	DynamicClientRegistrationConfig *DynamicClientRegistrationConfig

	// RedirectURL is a required URL to redirect to after authorization.
	// The caller is responsible for handling the redirect out of band.
	// If Dynamic Client Registration is used, the RedirectURL must be consistent
	// with [DynamicClientRegistrationConfig.Metadata.RedirectURIs].
	RedirectURL string

	// AuthorizationURLHandler is a required function called to handle the authorization URL.
	// It is responsible for opening the URL in a browser for the user to start the authorization.
	// It should return once the proccess has been initiated and the URL was
	// presented to the user successfully. Once the Authorizatin Server redirects back
	// to the [AuthorizationCodeOAuthHandler.RedirectURL], the caller should set
	// the authorization code by calling [AuthorizationCodeOAuthHandler.SetAuthorizationCode]
	// before retrying the request.
	AuthorizationURLHandler func(ctx context.Context, authorizationURL string) error

	// StateProvider is an optional function to generate a state string for authorization
	// requests. If not provided, a random string will be generated.
	// The state should be validated on the redirect callback.
	StateProvider func() string

	// resolvedClientConfig used during the authorization flow.
	resolvedClientConfig *resolvedClientConfig
	// tokenSource is the token source to use for authorization.
	// It can be prepopulated by calling [AuthorizationCodeOAuthHandler.SetTokenSource].
	tokenSource oauth2.TokenSource
	// codeVerifier is the PKCE code verifier.
	codeVerifier string
	// authorizationCode is the authorization code obtained from the authorization server.
	authorizationCode string
	// state is the state string used in the authorization request.
	state string
}

var _ OAuthHandler = (*AuthorizationCodeOAuthHandler)(nil)

func (h *AuthorizationCodeOAuthHandler) isOAuthHandler() {}

func (h *AuthorizationCodeOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	return h.tokenSource, nil
}

// Authorize performs the authorization flow.
// It is designed to be reentrant and called in two phases.
//  1. It initiates the Authorization Grant flow by calling [AuthorizationCodeOAuthHandler.AuthorizationURLHandler].
//     It will return [ErrRedirected] if the authorization flow was initiated successfully.
//  2. It exchanges the authorization code for an access token.
//     It will return a `nil` error if the authorization flow was completed successfully.
//     From this point on, [AuthorizationCodeOAuthHandler.TokenSource] will return a token source with the fetched token.
func (h *AuthorizationCodeOAuthHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	defer resp.Body.Close()
	log.Printf("Authorize: %s %s", req.Method, req.URL)
	if err := h.validate(); err != nil {
		return err
	}

	resourceURL := req.URL.String()
	wwwChallenges, err := oauthex.ParseWWWAuthenticate(resp.Header[http.CanonicalHeaderKey("WWW-Authenticate")])
	if err != nil {
		return fmt.Errorf("failed to parse WWW-Authenticate header: %v", err)
	}

	log.Printf("WWW-Authenticate header: %v", wwwChallenges)
	var prm *oauthex.ProtectedResourceMetadata
	for _, url := range oauthex.ProtectedResourceMetadataURLs(oauthex.ResourceMetadataURL(wwwChallenges), resourceURL) {
		var err error
		log.Printf("Getting protected resource metadata from %q", url)
		prm, err = oauthex.GetProtectedResourceMetadata(ctx, url, http.DefaultClient)
		if err == nil {
			break
		}
		log.Printf("Failed to get protected resource metadata from %q: %v", url, err)
	}
	asm, err := h.getAuthServerMetadata(ctx, prm, resourceURL)
	if err != nil {
		return err
	}

	if err := h.handleRegistration(ctx, asm); err != nil {
		return err
	}

	scopes := oauthex.Scopes(wwwChallenges)
	if len(scopes) == 0 && prm != nil && len(prm.ScopesSupported) > 0 {
		scopes = prm.ScopesSupported
	}

	cfg := &oauth2.Config{
		ClientID:     h.resolvedClientConfig.clientID,
		ClientSecret: h.resolvedClientConfig.clientSecret,

		Endpoint: oauth2.Endpoint{
			AuthURL:  asm.AuthorizationEndpoint,
			TokenURL: asm.TokenEndpoint,
			// TODO: validate if the auth style is supported by the AS.
			AuthStyle: h.resolvedClientConfig.authStyle,
		},
		RedirectURL: h.RedirectURL,
		Scopes:      scopes,
	}

	if h.authorizationCode != "" {
		return h.exchangeAuthorizationCode(ctx, cfg, resourceURL)
	}

	return h.startAuthFlow(ctx, cfg, req.URL.String())
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

func (h *AuthorizationCodeOAuthHandler) validate() error {
	if h.ClientIDMetadataDocumentConfig == nil &&
		h.PreregisteredClientConfig == nil &&
		h.DynamicClientRegistrationConfig == nil {
		return errors.New("at least one client registration configuration must be provided")
	}
	if h.RedirectURL == "" {
		return errors.New("field RedirectURL is required")
	}
	if h.AuthorizationURLHandler == nil {
		return errors.New("field AuthorizationURLHandler is required")
	}
	if h.ClientIDMetadataDocumentConfig != nil && !isNonRootHTTPSURL(h.ClientIDMetadataDocumentConfig.URL) {
		return fmt.Errorf("client ID metadata document URL must be a non-root HTTPS URL")
	}
	if h.PreregisteredClientConfig != nil {
		if h.PreregisteredClientConfig.ClientID == "" || h.PreregisteredClientConfig.ClientSecret == "" {
			return fmt.Errorf("pre-registered client ID or secret is empty")
		}
	}
	if h.DynamicClientRegistrationConfig != nil {
		if h.DynamicClientRegistrationConfig.Metadata == nil {
			return errors.New("field Metadata is required for dynamic client registration")
		}
		if !slices.Contains(h.DynamicClientRegistrationConfig.Metadata.RedirectURIs, h.RedirectURL) {
			return fmt.Errorf("redirect URI %q is not in the list of allowed redirect URIs for dynamic client registration", h.RedirectURL)
		}
	}
	if h.resolvedClientConfig == nil && h.authorizationCode != "" {
		return fmt.Errorf("exchanging authorization code with unregistered client is not allowed")
	}
	return nil
}

func isNonRootHTTPSURL(u string) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return false
	}
	return pu.Scheme == "https" && pu.Path != ""
}

// getAuthServerMetadata returns the authorization server metadata.
// If no metadata is available, it returns a minimal set of endpoints
// as a fallback to 2025-03-26 spec.
func (h *AuthorizationCodeOAuthHandler) getAuthServerMetadata(ctx context.Context, prm *oauthex.ProtectedResourceMetadata, resourceURL string) (*oauthex.AuthServerMeta, error) {
	var authServerURL string
	if prm != nil && len(prm.AuthorizationServers) > 0 {
		// Use the first authorization server, similarly to other SDKs.
		authServerURL = prm.AuthorizationServers[0]
	} else {
		// Fallback to 2025-03-26 spec: MCP server base URL acts as Authorization Server.
		authURL, err := url.Parse(resourceURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse resource URL: %v", err)
		}
		authURL.Path = ""
		authServerURL = authURL.String()
	}
	log.Printf("Authorization server URL: %s", authServerURL)

	asm, err := oauthex.GetAuthServerMeta(ctx, authServerURL, http.DefaultClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get authorization server metadata: %w", err)
	}
	log.Print("Authorization server medatada fetched")
	if asm == nil {
		log.Print("Authorization server metadata not found, using fallback")
		// Fallback to 2025-03-26 spec: predefined endpoints.
		// https://modelcontextprotocol.io/specification/2025-03-26/basic/authorization#fallbacks-for-servers-without-metadata-discovery
		asm = &oauthex.AuthServerMeta{
			Issuer:                authServerURL,
			AuthorizationEndpoint: authServerURL + "/authorize",
			TokenEndpoint:         authServerURL + "/token",
			RegistrationEndpoint:  authServerURL + "/register",
		}
	}
	return asm, nil
}

// handleRegistration handles client registration.
// The provided authorization server metadata must be non-nil.
// It must also have RegistrationEndpoint set if dynamic client registration is supported.
func (h *AuthorizationCodeOAuthHandler) handleRegistration(ctx context.Context, asm *oauthex.AuthServerMeta) error {
	// 1. Attempt to use Client ID Metadata Document (SEP-991).
	cimdCfg := h.ClientIDMetadataDocumentConfig
	if cimdCfg != nil && asm.ClientIDMetadataDocumentSupported {
		h.resolvedClientConfig = &resolvedClientConfig{
			registrationType: registrationTypeClientIDMetadataDocument,
			clientID:         cimdCfg.URL,
		}
		return nil
	}
	// 2. Attempt to use pre-registered client configuration.
	pCfg := h.PreregisteredClientConfig
	if pCfg != nil {
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
	if dcrCfg != nil && asm.RegistrationEndpoint != "" {
		regResp, err := oauthex.RegisterClient(ctx, asm.RegistrationEndpoint, dcrCfg.Metadata, http.DefaultClient)
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
	return fmt.Errorf("no configured client registration methods are supported by the authorization server")
}

// exchangeAuthorizationCode exchanges the authorization code for a token and stores it in a token source.
func (h *AuthorizationCodeOAuthHandler) exchangeAuthorizationCode(ctx context.Context, cfg *oauth2.Config, resourceURL string) error {
	log.Print("Authorization code is available, exchanging for token")
	opts := []oauth2.AuthCodeOption{
		oauth2.VerifierOption(h.codeVerifier),
		oauth2.SetAuthURLParam("resource", resourceURL),
	}
	defer func() {
		// Authorization code has been consumed, clear it.
		h.authorizationCode = ""
	}()
	token, err := cfg.Exchange(ctx, h.authorizationCode, opts...)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}
	h.tokenSource = cfg.TokenSource(ctx, token)
	return nil
}

// startAuthFlow generates the authorization URL and redirects the user to the authorization server.
func (h *AuthorizationCodeOAuthHandler) startAuthFlow(ctx context.Context, cfg *oauth2.Config, resourceURL string) error {
	h.codeVerifier = oauth2.GenerateVerifier()
	h.state = rand.Text()
	if h.StateProvider != nil {
		h.state = h.StateProvider()
	}

	authURL := cfg.AuthCodeURL(h.state,
		oauth2.S256ChallengeOption(h.codeVerifier),
		oauth2.SetAuthURLParam("resource", resourceURL),
	)

	log.Print("No authorization code available, opening authorization URL")
	if err := h.AuthorizationURLHandler(ctx, authURL); err != nil {
		return fmt.Errorf("authorization URL handler failed: %w", err)
	}
	return ErrRedirected
}
