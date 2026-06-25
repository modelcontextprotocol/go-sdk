// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package servercard

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// MediaType is the canonical media type for MCP Server Card documents.
	MediaType = "application/mcp-server-card+json"

	// SchemaURL is the canonical v1 Server Card JSON Schema URL.
	SchemaURL = "https://static.modelcontextprotocol.io/schemas/v1/server-card.schema.json"

	// DefaultPath is the recommended path for serving a Server Card relative to a
	// Streamable HTTP endpoint.
	DefaultPath = "/server-card"

	// RemoteTypeStreamableHTTP identifies a Streamable HTTP MCP endpoint.
	RemoteTypeStreamableHTTP = "streamable-http"

	// RemoteTypeSSE identifies an SSE MCP endpoint.
	RemoteTypeSSE = "sse"
)

var (
	nameRE                   = regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`)
	remoteURLRE              = regexp.MustCompile(`^(https?://[^\s]+|\{[a-zA-Z_][a-zA-Z0-9_]*\}[^\s]*)$`)
	versionRangeOperatorRE   = regexp.MustCompile(`[\^~|]|[<>]=?|\s`)
	versionWildcardSegmentRE = regexp.MustCompile(`(?:^|\.)[xX*](?:\.|$)`)
)

// Icon is an optionally sized icon that can be displayed in a user interface.
type Icon = mcp.Icon

// Input describes a user-supplied or pre-set input value for remote URL
// variables and header values.
type Input struct {
	Description string   `json:"description,omitempty"`
	IsRequired  bool     `json:"isRequired,omitempty"`
	IsSecret    bool     `json:"isSecret,omitempty"`
	Format      string   `json:"format,omitempty"`
	Default     string   `json:"default,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Value       string   `json:"value,omitempty"`
	Choices     []string `json:"choices,omitempty"`
}

// KeyValueInput is a named input used for HTTP headers.
type KeyValueInput struct {
	Input
	Name      string           `json:"name"`
	Variables map[string]Input `json:"variables,omitempty"`
}

// Repository describes source repository metadata for a Server Card.
type Repository struct {
	URL       string `json:"url"`
	Source    string `json:"source"`
	Subfolder string `json:"subfolder,omitempty"`
	ID        string `json:"id,omitempty"`
}

// Remote describes connection metadata for a remote MCP server endpoint.
type Remote struct {
	Type                      string           `json:"type"`
	URL                       string           `json:"url"`
	Headers                   []KeyValueInput  `json:"headers,omitempty"`
	Variables                 map[string]Input `json:"variables,omitempty"`
	SupportedProtocolVersions []string         `json:"supportedProtocolVersions,omitempty"`
}

// ServerCard is a static metadata document describing a remote MCP server.
type ServerCard struct {
	Schema      string         `json:"$schema"`
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	Version     string         `json:"version"`
	WebsiteURL  string         `json:"websiteUrl,omitempty"`
	Icons       []Icon         `json:"icons,omitempty"`
	Repository  *Repository    `json:"repository,omitempty"`
	Remotes     []Remote       `json:"remotes,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

type buildOptions struct {
	name       string
	schema     string
	remotes    []Remote
	repository *Repository
	meta       map[string]any
}

// BuildOption configures [BuildServerCard].
type BuildOption func(*buildOptions)

// WithName sets the Server Card's reverse-DNS namespace/name identifier.
func WithName(name string) BuildOption {
	return func(o *buildOptions) {
		o.name = name
	}
}

// WithSchema sets the Server Card schema URL. If unset, [SchemaURL] is used.
func WithSchema(schema string) BuildOption {
	return func(o *buildOptions) {
		o.schema = schema
	}
}

// WithRemotes sets the remote endpoints advertised by the Server Card.
func WithRemotes(remotes ...Remote) BuildOption {
	return func(o *buildOptions) {
		o.remotes = append([]Remote(nil), remotes...)
	}
}

// WithRepository sets repository metadata for source inspection.
func WithRepository(repository Repository) BuildOption {
	return func(o *buildOptions) {
		o.repository = &repository
	}
}

// WithMeta sets extension metadata for the Server Card's _meta field.
func WithMeta(meta map[string]any) BuildOption {
	return func(o *buildOptions) {
		o.meta = copyMap(meta)
	}
}

// BuildServerCard builds a Server Card from MCP implementation identity
// metadata.
//
// The implementation provides the title, description, version, website URL, and
// icons. The card name is supplied with [WithName] because MCP implementation
// names are free-form while Server Card names must be reverse-DNS namespace/name
// identifiers.
func BuildServerCard(impl *mcp.Implementation, opts ...BuildOption) (*ServerCard, error) {
	if impl == nil {
		return nil, errors.New("implementation must not be nil")
	}
	cfg := buildOptions{schema: SchemaURL}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.name == "" {
		return nil, errors.New("server card name must be set")
	}
	if impl.Version == "" {
		return nil, errors.New("implementation version must be set to build a Server Card")
	}
	if impl.Description == "" {
		return nil, errors.New("implementation description must be set to build a Server Card")
	}
	card := &ServerCard{
		Schema:      cfg.schema,
		Name:        cfg.name,
		Title:       impl.Title,
		Description: impl.Description,
		Version:     impl.Version,
		WebsiteURL:  impl.WebsiteURL,
		Icons:       append([]Icon(nil), impl.Icons...),
		Repository:  cfg.repository,
		Remotes:     append([]Remote(nil), cfg.remotes...),
		Meta:        copyMap(cfg.meta),
	}
	if err := card.Validate(); err != nil {
		return nil, err
	}
	return card, nil
}

// Validate reports whether c satisfies the Server Card schema constraints that
// are enforced by this package.
func (c *ServerCard) Validate() error {
	if c == nil {
		return errors.New("server card must not be nil")
	}
	if c.Schema != SchemaURL {
		return fmt.Errorf("server card schema must be %q", SchemaURL)
	}
	if c.Name == "" {
		return errors.New("server card name must be set")
	}
	if len(c.Name) < 3 || len(c.Name) > 200 || !nameRE.MatchString(c.Name) {
		return fmt.Errorf("server card name must match reverse-DNS namespace/name format: %q", c.Name)
	}
	if c.Description == "" {
		return errors.New("server card description must be set")
	}
	if len(c.Description) > 100 {
		return fmt.Errorf("server card description must be at most 100 characters")
	}
	if c.Version == "" {
		return errors.New("server card version must be set")
	}
	if len(c.Version) > 255 {
		return fmt.Errorf("server card version must be at most 255 characters")
	}
	if isVersionRange(c.Version) {
		return fmt.Errorf("server card version must be an exact version, not a range/wildcard: %q", c.Version)
	}
	if c.Title != "" && len(c.Title) > 100 {
		return fmt.Errorf("server card title must be at most 100 characters")
	}
	for i, icon := range c.Icons {
		if icon.Source == "" {
			return fmt.Errorf("server card icon %d source must be set", i)
		}
	}
	if c.Repository != nil {
		if c.Repository.URL == "" {
			return errors.New("server card repository URL must be set")
		}
		if c.Repository.Source == "" {
			return errors.New("server card repository source must be set")
		}
	}
	for i, remote := range c.Remotes {
		if remote.Type != RemoteTypeStreamableHTTP && remote.Type != RemoteTypeSSE {
			return fmt.Errorf("server card remote %d has unsupported type %q", i, remote.Type)
		}
		if remote.URL == "" {
			return fmt.Errorf("server card remote %d URL must be set", i)
		}
		if !remoteURLRE.MatchString(remote.URL) {
			return fmt.Errorf("server card remote %d URL must start with http://, https://, or a template variable", i)
		}
		for j, header := range remote.Headers {
			if header.Name == "" {
				return fmt.Errorf("server card remote %d header %d name must be set", i, j)
			}
		}
	}
	return nil
}

// Handler returns an HTTP handler that serves card as a Server Card discovery
// document.
func Handler(card *ServerCard) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setDiscoveryHeaders(w.Header())
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := card.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		body, err := json.Marshal(card)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", MediaType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}

// Mount registers [Handler] on mux at path. If path is empty, [DefaultPath] is
// used.
func Mount(mux *http.ServeMux, path string, card *ServerCard) {
	if path == "" {
		path = DefaultPath
	}
	mux.Handle(path, Handler(card))
}

func setDiscoveryHeaders(h http.Header) {
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", http.MethodGet)
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Cache-Control", "public, max-age=3600")
}

func isVersionRange(version string) bool {
	release, _, _ := strings.Cut(version, "-")
	return versionRangeOperatorRE.MatchString(version) || versionWildcardSegmentRE.MatchString(release)
}

func copyMap[M ~map[string]V, V any](m M) M {
	if m == nil {
		return nil
	}
	copy := make(M, len(m))
	for k, v := range m {
		copy[k] = v
	}
	return copy
}
