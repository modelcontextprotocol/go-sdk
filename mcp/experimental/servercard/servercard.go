// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package servercard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

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

	// InputFormatBoolean identifies a boolean input represented as "true" or
	// "false".
	InputFormatBoolean = "boolean"

	// InputFormatFilePath identifies a path on the user's filesystem.
	InputFormatFilePath = "filepath"

	// InputFormatNumber identifies a number represented as a decimal string.
	InputFormatNumber = "number"

	// InputFormatString identifies an arbitrary string input.
	InputFormatString = "string"
)

var (
	nameRE                   = regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`)
	remoteURLRE              = regexp.MustCompile(`^(https?://[^\s]+|\{[a-zA-Z_][a-zA-Z0-9_]*\}[^\s]*)$`)
	versionRangeOperatorRE   = regexp.MustCompile(`[\^~|]|[<>]=?`)
	versionWildcardSegmentRE = regexp.MustCompile(`(?:^|\.)[xX*](?:\.|$)`)
	versionHyphenRangeRE     = regexp.MustCompile(`^\s*[vV]?\d+(?:\.\d+){0,2}(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?\s+-\s+[vV]?\d+(?:\.\d+){0,2}(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?\s*$`)
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
	name        string
	description string
	remotes     []Remote
	repository  *Repository
	meta        map[string]any
}

// BuildOption configures [BuildServerCard].
type BuildOption func(*buildOptions)

// WithName sets the Server Card's reverse-DNS namespace/name identifier.
func WithName(name string) BuildOption {
	return func(o *buildOptions) {
		o.name = name
	}
}

// WithDescription sets the Server Card's short user-facing description.
func WithDescription(description string) BuildOption {
	return func(o *buildOptions) {
		o.description = description
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
// The implementation provides the title, version, website URL, and icons. The
// card name is supplied with [WithName] because MCP implementation names are
// free-form while Server Card names must be reverse-DNS namespace/name
// identifiers. The card description is supplied with [WithDescription].
func BuildServerCard(impl *mcp.Implementation, opts ...BuildOption) (*ServerCard, error) {
	if impl == nil {
		return nil, errors.New("implementation must not be nil")
	}
	cfg := buildOptions{}
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
	if cfg.description == "" {
		return nil, errors.New("server card description must be set")
	}
	card := &ServerCard{
		Schema:      SchemaURL,
		Name:        cfg.name,
		Title:       impl.Title,
		Description: cfg.description,
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
	nameLength := utf8.RuneCountInString(c.Name)
	if nameLength < 3 || nameLength > 200 || !nameRE.MatchString(c.Name) {
		return fmt.Errorf("server card name must match reverse-DNS namespace/name format: %q", c.Name)
	}
	if c.Description == "" {
		return errors.New("server card description must be set")
	}
	if utf8.RuneCountInString(c.Description) > 100 {
		return fmt.Errorf("server card description must be at most 100 characters")
	}
	if c.Version == "" {
		return errors.New("server card version must be set")
	}
	if utf8.RuneCountInString(c.Version) > 255 {
		return fmt.Errorf("server card version must be at most 255 characters")
	}
	if isVersionRange(c.Version) {
		return fmt.Errorf("server card version must be an exact version, not a range/wildcard: %q", c.Version)
	}
	if c.Title != "" && utf8.RuneCountInString(c.Title) > 100 {
		return fmt.Errorf("server card title must be at most 100 characters")
	}
	if c.WebsiteURL != "" && !isAbsoluteURI(c.WebsiteURL) {
		return fmt.Errorf("server card website URL must be an absolute URI: %q", c.WebsiteURL)
	}
	for i, icon := range c.Icons {
		if icon.Source == "" {
			return fmt.Errorf("server card icon %d source must be set", i)
		}
		if !isAbsoluteURI(icon.Source) {
			return fmt.Errorf("server card icon %d source must be an absolute URI: %q", i, icon.Source)
		}
		if icon.Theme != "" && icon.Theme != mcp.IconThemeLight && icon.Theme != mcp.IconThemeDark {
			return fmt.Errorf("server card icon %d has unsupported theme %q", i, icon.Theme)
		}
	}
	if c.Repository != nil {
		if c.Repository.URL == "" {
			return errors.New("server card repository URL must be set")
		}
		if !isAbsoluteURI(c.Repository.URL) {
			return fmt.Errorf("server card repository URL must be an absolute URI: %q", c.Repository.URL)
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
		for name, input := range remote.Variables {
			if err := validateInput(input); err != nil {
				return fmt.Errorf("server card remote %d variable %q: %w", i, name, err)
			}
		}
		for j, header := range remote.Headers {
			if header.Name == "" {
				return fmt.Errorf("server card remote %d header %d name must be set", i, j)
			}
			if err := validateInput(header.Input); err != nil {
				return fmt.Errorf("server card remote %d header %d: %w", i, j, err)
			}
			for name, input := range header.Variables {
				if err := validateInput(input); err != nil {
					return fmt.Errorf("server card remote %d header %d variable %q: %w", i, j, name, err)
				}
			}
		}
	}
	return nil
}

func validateInput(input Input) error {
	switch input.Format {
	case "", InputFormatBoolean, InputFormatFilePath, InputFormatNumber, InputFormatString:
		return nil
	default:
		return fmt.Errorf("unsupported input format %q", input.Format)
	}
}

// Handler returns an HTTP handler that serves card as a Server Card discovery
// document.
func Handler(card *ServerCard) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setDiscoveryHeaders(w.Header())
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := card.Validate(); err != nil {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		body, err := json.Marshal(card)
		if err != nil {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sum := sha256.Sum256(body)
		etag := `"` + hex.EncodeToString(sum[:]) + `"`
		w.Header().Set("Content-Type", MediaType)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", etag)
		if ifNoneMatchMatches(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
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
}

func ifNoneMatchMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			return true
		}
		if strings.HasPrefix(candidate, "W/") || strings.HasPrefix(candidate, "w/") {
			candidate = strings.TrimSpace(candidate[2:])
		}
		if candidate == etag {
			return true
		}
	}
	return false
}

func isVersionRange(version string) bool {
	withoutBuild, _, _ := strings.Cut(version, "+")
	release, _, _ := strings.Cut(withoutBuild, "-")
	return versionRangeOperatorRE.MatchString(version) ||
		versionWildcardSegmentRE.MatchString(release) ||
		versionHyphenRangeRE.MatchString(version)
}

func isAbsoluteURI(value string) bool {
	for _, r := range value {
		if r > unicode.MaxASCII || unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	uri, err := url.ParseRequestURI(value)
	return err == nil && uri.IsAbs()
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
