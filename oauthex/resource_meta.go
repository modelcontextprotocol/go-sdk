// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file implements Protected Resource Metadata.
// See https://www.rfc-editor.org/rfc/rfc9728.html.

//go:build mcp_go_client_oauth

package oauthex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/internal/util"
)

const defaultProtectedResourceMetadataURI = "/.well-known/oauth-protected-resource"

// GetProtectedResourceMetadataFromID issues a GET request to retrieve protected resource
// metadata from a resource server by its ID.
// The resource ID is an HTTPS URL, typically with a host:port and possibly a path.
// For example:
//
//	https://example.com/server
//
// This function, following the spec (§3), inserts the default well-known path into the
// URL. In our example, the result would be
//
//	https://example.com/.well-known/oauth-protected-resource/server
//
// It then retrieves the metadata at that location using the given client (or the
// default client if nil) and validates its resource field against resourceID.
//
// Deprecated: Use [GetProtectedResourceMetadata] instead. This function will be removed in v1.5.0.
func GetProtectedResourceMetadataFromID(ctx context.Context, resourceID string, c *http.Client) (_ *ProtectedResourceMetadata, err error) {
	defer util.Wrapf(&err, "GetProtectedResourceMetadataFromID(%q)", resourceID)

	u, err := url.Parse(resourceID)
	if err != nil {
		return nil, err
	}
	// Insert well-known URI into URL.
	u.Path = path.Join(defaultProtectedResourceMetadataURI, u.Path)
	return GetProtectedResourceMetadata(ctx, u.String(), resourceID, c)
}

// GetProtectedResourceMetadataFromHeader retrieves protected resource metadata
// using information in the given header, using the given client (or the default
// client if nil).
// It issues a GET request to a URL discovered by parsing the WWW-Authenticate headers in the given request.
// Per RFC 9728 section 3.3, it validates that the resource field of the resulting metadata
// matches the serverURL (the URL that the client used to make the original request to the resource server).
// If there is no metadata URL in the header, it returns nil, nil.
//
// Deprecated: Use [GetProtectedResourceMetadata] instead. This function will be removed in v1.5.0.
func GetProtectedResourceMetadataFromHeader(ctx context.Context, serverURL string, header http.Header, c *http.Client) (_ *ProtectedResourceMetadata, err error) {
	headers := header[http.CanonicalHeaderKey("WWW-Authenticate")]
	if len(headers) == 0 {
		return nil, nil
	}
	cs, err := ParseWWWAuthenticate(headers)
	if err != nil {
		return nil, err
	}
	metadataURL := ResourceMetadataURL(cs)
	if metadataURL == "" {
		return nil, nil
	}
	return GetProtectedResourceMetadata(ctx, metadataURL, serverURL, c)
}

// GetProtectedResourceMetadataFromID issues a GET request to retrieve protected resource
// metadata from a resource server.
// The metadataURL is typically a URL with a host:port and possibly a path.
// The resourceURL is the resource URI the metadataURL is for.
// The following checks are performed:
//   - The metadataURL must use HTTPS or be a local address.
//   - The resource field of the resulting metadata must match the resourceURL.
//   - The authorization_servers field of the resulting metadata is checked for dangerous URL schemes.
func GetProtectedResourceMetadata(ctx context.Context, metadataURL, resourceURL string, c *http.Client) (_ *ProtectedResourceMetadata, err error) {
	defer util.Wrapf(&err, "GetProtectedResourceMetadata(%q)", metadataURL)
	u, err := url.Parse(metadataURL)
	if err != nil {
		return nil, err
	}
	// Only allow HTTP for local addresses (testing or development purposes).
	if !util.IsLoopback(u.Host) && u.Scheme != "https" {
		return nil, fmt.Errorf("metadataURL %q does not use HTTPS", metadataURL)
	}
	prm, err := getJSON[ProtectedResourceMetadata](ctx, c, metadataURL, 1<<20)
	if err != nil {
		return nil, err
	}
	// Validate the Resource field (see RFC 9728, section 3.3).
	if prm.Resource != resourceURL {
		return nil, fmt.Errorf("got metadata resource %q, want %q", prm.Resource, resourceURL)
	}
	// Validate the authorization server URLs to prevent XSS attacks (see #526).
	for _, u := range prm.AuthorizationServers {
		if err := checkURLScheme(u); err != nil {
			return nil, err
		}
	}
	return prm, nil
}

// ResourceMetadataURL returns a resource metadata URL from the given "WWW-Authenticate" header challenges,
// or the empty string if there is none.
func ResourceMetadataURL(cs []Challenge) string {
	for _, c := range cs {
		if u := c.Params["resource_metadata"]; u != "" {
			return u
		}
	}
	return ""
}

// Scopes returns the scopes from the given "WWW-Authenticate" header challenges.
// It only looks at challenges with the "Bearer" scheme.
func Scopes(cs []Challenge) []string {
	for _, c := range cs {
		if c.Scheme == "bearer" && c.Params["scope"] != "" {
			return strings.Fields(c.Params["scope"])
		}
	}
	return nil
}
