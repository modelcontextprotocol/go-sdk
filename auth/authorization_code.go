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

// AuthorizationResult is the result of an authorization flow.
// It is returned by [AuthorizationCodeOAuthHandler.AuthorizationURLHandler] implementations.
type AuthorizationResult struct {
	// AuthorizationCode is the authorization code obtained from the authorization server.
	AuthorizationCode string
	// State is the state string returned by the authorization server.
	State string
}

// AuthorizationCodeHandlerConfig is the configuration for [AuthorizationCodeOAuthHandler].
type AuthorizationCodeHandlerConfig struct {
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
	// It should return the authorization code and state once the Authorization Server
	// redirects back to the [AuthorizationCodeOAuthHandler.RedirectURL].
	AuthorizationURLHandler func(ctx context.Context, authorizationURL string) (*AuthorizationResult, error)

	// StateProvider is an optional function to generate a state string for authorization
	// requests. If not provided, a random string will be generated.
	// The state will be validated on the redirect callback.
	StateProvider func() string
}

// AuthorizationCodeOAuthHandler is an implementation of [OAuthHandler] that uses
// the authorization code flow to obtain access tokens.
type AuthorizationCodeOAuthHandler struct {
	config *AuthorizationCodeHandlerConfig

	// tokenSource is the token source to use for authorization.
	tokenSource oauth2.TokenSource
}

var _ OAuthHandler = (*AuthorizationCodeOAuthHandler)(nil)

func (h *AuthorizationCodeOAuthHandler) isOAuthHandler() {}

func (h *AuthorizationCodeOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	return h.tokenSource, nil
}

// NewAuthorizationCodeOAuthHandler creates a new AuthorizationCodeOAuthHandler.
// It performs validation of the configuration and returns an error if it is invalid.
// The passed config is consumed by the handler and should not be modified after.
func NewAuthorizationCodeOAuthHandler(config *AuthorizationCodeHandlerConfig) (*AuthorizationCodeOAuthHandler, error) {
	if config == nil {
		return nil, errors.New("config must be provided")
	}
	if config.ClientIDMetadataDocumentConfig == nil &&
		config.PreregisteredClientConfig == nil &&
		config.DynamicClientRegistrationConfig == nil {
		return nil, errors.New("at least one client registration configuration must be provided")
	}
	if config.RedirectURL == "" {
		return nil, errors.New("field RedirectURL is required")
	}
	if config.AuthorizationURLHandler == nil {
		return nil, errors.New("field AuthorizationURLHandler is required")
	}
	if config.ClientIDMetadataDocumentConfig != nil && !isNonRootHTTPSURL(config.ClientIDMetadataDocumentConfig.URL) {
		return nil, fmt.Errorf("client ID metadata document URL must be a non-root HTTPS URL")
	}
	if config.PreregisteredClientConfig != nil {
		if config.PreregisteredClientConfig.ClientID == "" || config.PreregisteredClientConfig.ClientSecret == "" {
			return nil, fmt.Errorf("pre-registered client ID or secret is empty")
		}
	}
	if config.DynamicClientRegistrationConfig != nil {
		if config.DynamicClientRegistrationConfig.Metadata == nil {
			return nil, errors.New("field Metadata is required for dynamic client registration")
		}
		if !slices.Contains(config.DynamicClientRegistrationConfig.Metadata.RedirectURIs, config.RedirectURL) {
			return nil, fmt.Errorf("redirect URI %q is not in the list of allowed redirect URIs for dynamic client registration", config.RedirectURL)
		}
	}
	return &AuthorizationCodeOAuthHandler{config: config}, nil
}

// Authorize performs the authorization flow.
// It is designed to perform the whole Authorization Code Grant flow.
// On success, [AuthorizationCodeOAuthHandler.TokenSource] will return a token source with the fetched token.
func (h *AuthorizationCodeOAuthHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	defer resp.Body.Close()
	log.Printf("Authorize: %s %s", req.Method, req.URL)

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
	// log.Printf("Protected resource metadata: %+v", prm)
	asm, err := h.getAuthServerMetadata(ctx, prm, resourceURL)
	if err != nil {
		return err
	}
	// log.Printf("Authorization server metadata: %+v", asm)

	resolvedClientConfig, err := h.handleRegistration(ctx, asm)
	if err != nil {
		return err
	}

	scopes := oauthex.Scopes(wwwChallenges)
	if len(scopes) == 0 && prm != nil && len(prm.ScopesSupported) > 0 {
		scopes = prm.ScopesSupported
	}

	cfg := &oauth2.Config{
		ClientID:     resolvedClientConfig.clientID,
		ClientSecret: resolvedClientConfig.clientSecret,

		Endpoint: oauth2.Endpoint{
			AuthURL:  asm.AuthorizationEndpoint,
			TokenURL: asm.TokenEndpoint,
			// TODO: validate if the auth style is supported by the AS.
			AuthStyle: resolvedClientConfig.authStyle,
		},
		RedirectURL: h.config.RedirectURL,
		Scopes:      scopes,
	}

	authRes, err := h.getAuthorizationCode(ctx, cfg, req.URL.String())
	if err != nil {
		return err
	}

	return h.exchangeAuthorizationCode(ctx, cfg, authRes, resourceURL)
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

// handleRegistration handles client registration.
// The provided authorization server metadata must be non-nil.
// Support for different registration methods is defined as follows:
//   - Client ID Metadata Document: metadata must have
//     `ClientIDMetadataDocumentSupported` set to true.
//   - Pre-registered client: assumed to be supported.
//   - Dynamic client registration: metadata must have
//     `RegistrationEndpoint` set to a non-empty value.
func (h *AuthorizationCodeOAuthHandler) handleRegistration(ctx context.Context, asm *oauthex.AuthServerMeta) (*resolvedClientConfig, error) {
	// 1. Attempt to use Client ID Metadata Document (SEP-991).
	cimdCfg := h.config.ClientIDMetadataDocumentConfig
	if cimdCfg != nil && asm.ClientIDMetadataDocumentSupported {
		return &resolvedClientConfig{
			registrationType: registrationTypeClientIDMetadataDocument,
			clientID:         cimdCfg.URL,
		}, nil
	}
	// 2. Attempt to use pre-registered client configuration.
	pCfg := h.config.PreregisteredClientConfig
	if pCfg != nil {
		return &resolvedClientConfig{
			registrationType: registrationTypePreregistered,
			clientID:         pCfg.ClientID,
			clientSecret:     pCfg.ClientSecret,
			authStyle:        pCfg.AuthStyle,
		}, nil
	}
	// 3. Attempt to use dynamic client registration.
	dcrCfg := h.config.DynamicClientRegistrationConfig
	if dcrCfg != nil && asm.RegistrationEndpoint != "" {
		regResp, err := oauthex.RegisterClient(ctx, asm.RegistrationEndpoint, dcrCfg.Metadata, http.DefaultClient)
		if err != nil {
			return nil, fmt.Errorf("failed to register client: %w", err)
		}
		cfg := &resolvedClientConfig{
			registrationType: registrationTypeDynamic,
			clientID:         regResp.ClientID,
			clientSecret:     regResp.ClientSecret,
		}
		switch regResp.TokenEndpointAuthMethod {
		case "client_secret_post":
			cfg.authStyle = oauth2.AuthStyleInParams
		case "client_secret_basic":
			cfg.authStyle = oauth2.AuthStyleInHeader
		case "none":
			// "none" is equivalent to "client_secret_post" but without sending client secret.
			cfg.authStyle = oauth2.AuthStyleInParams
			cfg.clientSecret = ""
		default:
			// We leave the AuthStyle set to zero value, which is auto-detection.
		}
		log.Printf("Client registered with client ID: %s", regResp.ClientID)
		return cfg, nil
	}
	return nil, fmt.Errorf("no configured client registration methods are supported by the authorization server")
}

type authResult struct {
	*AuthorizationResult
	// usedCodeVerifier is the PKCE code verifier used to obtain the authorization code.
	// It is preserved for the token exchange step.
	usedCodeVerifier string
}

// getAuthorizationCode uses the [AuthorizationCodeOAuthHandler.AuthorizationURLHandler]
// to obtain an authorization code.
func (h *AuthorizationCodeOAuthHandler) getAuthorizationCode(ctx context.Context, cfg *oauth2.Config, resourceURL string) (*authResult, error) {
	codeVerifier := oauth2.GenerateVerifier()
	state := rand.Text()
	if h.config.StateProvider != nil {
		state = h.config.StateProvider()
	}

	authURL := cfg.AuthCodeURL(state,
		oauth2.S256ChallengeOption(codeVerifier),
		oauth2.SetAuthURLParam("resource", resourceURL),
	)

	log.Printf("Calling AuthorizationURLHandler: %q", authURL)
	authRes, err := h.config.AuthorizationURLHandler(ctx, authURL)
	if err != nil {
		return nil, err
	}
	if authRes.State != state {
		return nil, fmt.Errorf("state mismatch")
	}
	return &authResult{
		AuthorizationResult: authRes,
		usedCodeVerifier:    codeVerifier,
	}, nil
}

// exchangeAuthorizationCode exchanges the authorization code for a token
// and stores it in a token source.
func (h *AuthorizationCodeOAuthHandler) exchangeAuthorizationCode(ctx context.Context, cfg *oauth2.Config, authResult *authResult, resourceURL string) error {
	log.Printf("Exchanging authorization code for token")
	opts := []oauth2.AuthCodeOption{
		oauth2.VerifierOption(authResult.usedCodeVerifier),
		oauth2.SetAuthURLParam("resource", resourceURL),
	}
	token, err := cfg.Exchange(ctx, authResult.AuthorizationCode, opts...)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}
	h.tokenSource = cfg.TokenSource(ctx, token)
	return nil
}
