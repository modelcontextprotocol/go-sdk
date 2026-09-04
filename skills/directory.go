// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"mime"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DirectoryCacheOptions configures catalog caching. A zero value builds the
// catalog on the first request and caches it indefinitely.
type DirectoryCacheOptions struct {
	// Preload builds the catalog during provider construction, so scan and
	// validation errors are returned by the constructor.
	Preload bool
	// MaxAge makes the catalog expire after this duration. The next request
	// rebuilds an expired catalog. A zero value disables expiry.
	MaxAge time.Duration
	// Invalidate marks the catalog stale. Signals are coalesced and consumed
	// when a request arrives, so callers should use a buffered channel.
	Invalidate <-chan struct{}
}

// DirectoryOptions configures a filesystem-backed provider.
type DirectoryOptions struct {
	URIPathPrefix string
	PageSize      int
	// Cache enables catalog caching. A nil value rebuilds the catalog for every
	// request.
	Cache             *DirectoryCacheOptions
	ServerOptions     *ServerOptions
	CatalogValidators []func(context.Context, []*Skill) error
}

// DirectoryProvider discovers and serves skills from a filesystem.
type DirectoryProvider struct {
	fsys              fs.FS
	osPath            string
	rootName          string
	prefix            []string
	pageSize          int
	serverOptions     *ServerOptions
	catalogValidators []func(context.Context, []*Skill) error
	cacheEnabled      bool
	preload           bool
	maxAge            time.Duration
	invalidate        <-chan struct{}
	cacheMu           sync.Mutex
	cached            *directoryCatalog
	refreshedAt       time.Time
}

type directoryCatalog struct {
	skills  []*Skill
	bySkill map[string]*Skill
	files   map[string]catalogFile
	dirs    map[string][]*mcp.Resource
	seen    map[[2]string]bool
}

type catalogFile struct {
	path     string
	mimeType string
}

type catalogEntry struct {
	path string
	info fs.FileInfo
}

type catalogDigest struct {
	digest string
	size   int64
}

type rootedFS struct{ root *os.Root }

func (f rootedFS) Open(name string) (fs.File, error) { return f.root.Open(name) }

// NewDirectoryProvider constructs a live provider rooted at dir.
func NewDirectoryProvider(dir string, options *DirectoryOptions) (*DirectoryProvider, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("skills: %q is not a directory", dir)
	}
	provider, err := newDirectoryProvider(nil, options)
	if err != nil {
		return nil, err
	}
	provider.osPath = abs
	provider.rootName = filepath.Base(abs)
	return provider.initialize()
}

// NewFSProvider constructs a filesystem-backed provider using fsys.
func NewFSProvider(fsys fs.FS, options *DirectoryOptions) (*DirectoryProvider, error) {
	if fsys == nil {
		return nil, fmt.Errorf("skills: nil filesystem")
	}
	provider, err := newDirectoryProvider(fsys, options)
	if err != nil {
		return nil, err
	}
	return provider.initialize()
}

func newDirectoryProvider(fsys fs.FS, options *DirectoryOptions) (*DirectoryProvider, error) {
	p := &DirectoryProvider{fsys: fsys, pageSize: mcp.DefaultPageSize}
	if options != nil {
		if options.PageSize < 0 {
			return nil, fmt.Errorf("skills: invalid page size %d", options.PageSize)
		}
		if options.PageSize > 0 {
			p.pageSize = options.PageSize
		}
		if options.Cache != nil {
			if options.Cache.MaxAge < 0 {
				return nil, fmt.Errorf("skills: invalid cache max age %s", options.Cache.MaxAge)
			}
			p.cacheEnabled = true
			p.preload = options.Cache.Preload
			p.maxAge = options.Cache.MaxAge
			p.invalidate = options.Cache.Invalidate
		}
		p.serverOptions = cloneServerOptions(options.ServerOptions)
		p.catalogValidators = slices.Clone(options.CatalogValidators)
		if options.URIPathPrefix != "" {
			p.prefix = strings.Split(strings.Trim(options.URIPathPrefix, "/"), "/")
			for _, segment := range p.prefix {
				if segment == "" || segment == "." || segment == ".." {
					return nil, fmt.Errorf("skills: invalid URI path prefix %q", options.URIPathPrefix)
				}
			}
		}
	}
	return p, nil
}

func (p *DirectoryProvider) initialize() (*DirectoryProvider, error) {
	if p.preload {
		if _, err := p.catalog(context.Background(), true); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// AddDirectory discovers and serves skills rooted at dir.
func AddDirectory(server *mcp.Server, dir string, options *DirectoryOptions) error {
	provider, err := NewDirectoryProvider(dir, options)
	if err != nil {
		return err
	}
	return provider.AddTo(server)
}

// AddFS discovers and serves skills from fsys.
func AddFS(server *mcp.Server, fsys fs.FS, options *DirectoryOptions) error {
	provider, err := NewFSProvider(fsys, options)
	if err != nil {
		return err
	}
	return provider.AddTo(server)
}

// AddTo registers the provider's extension handlers and resource template.
func (p *DirectoryProvider) AddTo(server *mcp.Server) error {
	if err := AddHandlers(server, &Handlers{
		List:          p.ListSkills,
		Get:           p.GetSkill,
		ReadDirectory: p.ReadDirectory,
	}, p.serverOptions); err != nil {
		return err
	}
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "skills",
		Description: "Resources served by the MCP Skills extension.",
		URITemplate: "skill://{authority}/{+path}",
	}, p.ReadResource)
	return nil
}

// Refresh rebuilds a cached catalog immediately.
func (p *DirectoryProvider) Refresh(ctx context.Context) error {
	if !p.cacheEnabled {
		return fmt.Errorf("skills: explicit refresh requires a cached catalog mode")
	}
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	return p.refreshLocked(ctx)
}

// ListSkills returns a current, paginated view of the skills in the filesystem.
func (p *DirectoryProvider) ListSkills(ctx context.Context, _ *mcp.ServerSession, params *ListSkillsParams) (*ListSkillsResult, error) {
	catalog, err := p.catalog(ctx, true)
	if err != nil {
		return nil, err
	}
	cursor := ""
	if params != nil {
		cursor = params.Cursor
	}
	page, next, err := paginate(catalog.skills, cursor, p.pageSize, func(skill *Skill) string { return skill.URI })
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	return &ListSkillsResult{Skills: page, NextCursor: next}, nil
}

// GetSkill returns the current entry for one skill URI.
func (p *DirectoryProvider) GetSkill(ctx context.Context, _ *mcp.ServerSession, params *GetSkillParams) (*GetSkillResult, error) {
	if params == nil {
		return nil, invalidParams("missing required uri")
	}
	catalog, err := p.catalog(ctx, true)
	if err != nil {
		return nil, err
	}
	skill, ok := catalog.bySkill[params.URI]
	if !ok {
		return nil, invalidParams(fmt.Sprintf("unknown skill %q", params.URI))
	}
	return &GetSkillResult{Skill: skill}, nil
}

// ReadResource resolves and reads a current skill resource.
func (p *DirectoryProvider) ReadResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	if req == nil || req.Params == nil {
		return nil, invalidParams("missing required uri")
	}
	catalog, err := p.catalog(ctx, false)
	if err != nil {
		return nil, err
	}
	file, ok := catalog.files[req.Params.URI]
	if !ok {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	data, err := p.readFile(file.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		return nil, err
	}
	contents := &mcp.ResourceContents{URI: req.Params.URI, MIMEType: file.mimeType}
	if textualMIME(file.mimeType) && utf8.Valid(data) {
		contents.Text = string(data)
	} else {
		contents.Blob = data
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{contents}}, nil
}

// ReadDirectory returns a current, paginated view of a directory's direct children.
func (p *DirectoryProvider) ReadDirectory(ctx context.Context, _ *mcp.ServerSession, params *ReadDirectoryParams) (*ReadDirectoryResult, error) {
	if params == nil {
		return nil, invalidParams("missing required uri")
	}
	catalog, err := p.catalog(ctx, false)
	if err != nil {
		return nil, err
	}
	children, ok := catalog.dirs[params.URI]
	if !ok {
		return nil, invalidParams(fmt.Sprintf("unknown directory %q", params.URI))
	}
	page, next, err := paginate(children, params.Cursor, p.pageSize, func(resource *mcp.Resource) string { return resource.URI })
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	return &ReadDirectoryResult{Resources: page, NextCursor: next}, nil
}

func (p *DirectoryProvider) catalog(ctx context.Context, manifests bool) (*directoryCatalog, error) {
	if !p.cacheEnabled {
		return p.scanCatalog(ctx, manifests)
	}
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	invalidated := p.invalidated()
	if p.cached != nil && !invalidated && (p.maxAge == 0 || time.Since(p.refreshedAt) < p.maxAge) {
		return p.cached, nil
	}
	if err := p.refreshLocked(ctx); err != nil {
		return nil, err
	}
	return p.cached, nil
}

func (p *DirectoryProvider) refreshLocked(ctx context.Context) error {
	catalog, err := p.scanCatalog(ctx, true)
	if err != nil {
		return err
	}
	p.cached = catalog
	p.refreshedAt = time.Now()
	return nil
}

func (p *DirectoryProvider) invalidated() bool {
	requested := false
	for p.invalidate != nil {
		select {
		case _, ok := <-p.invalidate:
			if !ok {
				p.invalidate = nil
				return requested
			}
			requested = true
		default:
			return requested
		}
	}
	return requested
}

func (p *DirectoryProvider) scanCatalog(ctx context.Context, manifests bool) (*directoryCatalog, error) {
	fsys, closeFS, err := p.openFS()
	if err != nil {
		return nil, err
	}
	defer closeFS()

	var entries []catalogEntry
	var skillDirs []string
	err = fs.WalkDir(fsys, ".", func(name string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if dirEntry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("skills: symlink %q is not supported", name)
		}
		info, err := dirEntry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("skills: non-regular file %q is not supported", name)
		}
		entries = append(entries, catalogEntry{path: name, info: info})
		if !info.IsDir() && path.Base(name) == "SKILL.md" {
			skillDirs = append(skillDirs, path.Dir(name))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(skillDirs)
	catalog := &directoryCatalog{
		bySkill: make(map[string]*Skill),
		files:   make(map[string]catalogFile),
		dirs:    make(map[string][]*mcp.Resource),
		seen:    make(map[[2]string]bool),
	}
	digests := make(map[string]catalogDigest)
	for _, skillDir := range skillDirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var frontmatter Frontmatter
		if manifests || skillDir == "." && p.rootName == "" {
			data, err := fs.ReadFile(fsys, path.Join(skillDir, "SKILL.md"))
			if err != nil {
				return nil, err
			}
			frontmatter, err = parseFrontmatter(data)
			if err != nil {
				return nil, fmt.Errorf("skills: parsing %s/SKILL.md: %w", skillDir, err)
			}
		}
		segments := slices.Clone(p.prefix)
		if skillDir == "." {
			physicalName := p.rootName
			if physicalName == "" {
				physicalName, _ = frontmatter["name"].(string)
			}
			segments = append(segments, physicalName)
		} else {
			segments = append(segments, strings.Split(skillDir, "/")...)
		}
		rootURI, err := skillURI(segments)
		if err != nil {
			return nil, err
		}
		var resources []*Resource
		defaultValidation, limits := validationSettings(p.serverOptions)
		var totalSize int64
		for _, item := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if item.info.IsDir() || !withinDir(skillDir, item.path) {
				continue
			}
			if manifests && defaultValidation {
				if limits.MaxResourcesPerSkill > 0 && len(resources) == limits.MaxResourcesPerSkill {
					return nil, fmt.Errorf("skill %q exceeds the resource limit of %d", rootURI, limits.MaxResourcesPerSkill)
				}
				if limits.MaxTotalSize > 0 && item.info.Size() > 0 && totalSize > limits.MaxTotalSize-item.info.Size() {
					return nil, fmt.Errorf("skill %q exceeds the total size limit of %d", rootURI, limits.MaxTotalSize)
				}
			}
			rel := item.path
			if skillDir != "." {
				rel = strings.TrimPrefix(item.path, skillDir+"/")
			}
			resourceURI := rootURI + "/" + escapePath(rel)
			mimeType := resourceMIME(item.path, rel)
			catalog.files[resourceURI] = catalogFile{path: item.path, mimeType: mimeType}
			if !manifests {
				continue
			}
			totalSize += item.info.Size()
			digest, ok := digests[item.path]
			if !ok {
				data, err := fs.ReadFile(fsys, item.path)
				if err != nil {
					return nil, err
				}
				sum := sha256.Sum256(data)
				digest = catalogDigest{digest: fmt.Sprintf("sha256:%x", sum), size: int64(len(data))}
				digests[item.path] = digest
			}
			resources = append(resources, &Resource{
				URI:    resourceURI,
				Digest: digest.digest,
				Size:   digest.size,
			})
		}
		p.addDirectories(catalog, skillDir, rootURI, entries)
		if manifests {
			slices.SortFunc(resources, func(a, b *Resource) int { return strings.Compare(a.URI, b.URI) })
			skill := &Skill{URI: rootURI + "/SKILL.md", Frontmatter: frontmatter, Resources: StaticResources(resources...)}
			if err := validateSkillResult(ctx, skill, p.serverOptions); err != nil {
				return nil, err
			}
			if _, exists := catalog.bySkill[skill.URI]; exists {
				return nil, fmt.Errorf("skills: duplicate skill URI %q", skill.URI)
			}
			catalog.skills = append(catalog.skills, skill)
			catalog.bySkill[skill.URI] = skill
		}
	}
	slices.SortFunc(catalog.skills, func(a, b *Skill) int { return strings.Compare(a.URI, b.URI) })
	for uri := range catalog.dirs {
		slices.SortFunc(catalog.dirs[uri], func(a, b *mcp.Resource) int { return strings.Compare(a.URI, b.URI) })
	}
	if manifests {
		if err := runValidators(ctx, p.catalogValidators, catalog.skills); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

func (p *DirectoryProvider) addDirectories(catalog *directoryCatalog, skillDir, rootURI string, entries []catalogEntry) {
	// Kept as a separate pass so empty directories are represented too.
	for _, item := range entries {
		if item.path == skillDir || !withinDir(skillDir, item.path) {
			continue
		}
		rel := item.path
		if skillDir != "." {
			rel = strings.TrimPrefix(item.path, skillDir+"/")
		}
		parts := strings.Split(rel, "/")
		parentURI := rootURI
		for i, part := range parts {
			childURI := parentURI + "/" + url.PathEscape(part)
			isDirectory := i < len(parts)-1 || item.info.IsDir()
			key := [2]string{parentURI, childURI}
			if !catalog.seen[key] {
				mimeType := ""
				var size int64
				if isDirectory {
					mimeType = "inode/directory"
				} else {
					mimeType = mime.TypeByExtension(path.Ext(part))
					size = item.info.Size()
					if part == "SKILL.md" {
						mimeType = "text/markdown"
					}
				}
				catalog.dirs[parentURI] = append(catalog.dirs[parentURI], &mcp.Resource{
					URI: childURI, Name: part, MIMEType: mimeType, Size: size,
				})
				catalog.seen[key] = true
			}
			if isDirectory {
				if _, ok := catalog.dirs[childURI]; !ok {
					catalog.dirs[childURI] = []*mcp.Resource{}
				}
				parentURI = childURI
			}
		}
	}
	if _, ok := catalog.dirs[rootURI]; !ok {
		catalog.dirs[rootURI] = []*mcp.Resource{}
	}
}

func (p *DirectoryProvider) openFS() (fs.FS, func(), error) {
	if p.osPath == "" {
		return p.fsys, func() {}, nil
	}
	root, err := os.OpenRoot(p.osPath)
	if err != nil {
		return nil, nil, err
	}
	return rootedFS{root}, func() { _ = root.Close() }, nil
}

func (p *DirectoryProvider) readFile(name string) ([]byte, error) {
	fsys, closeFS, err := p.openFS()
	if err != nil {
		return nil, err
	}
	defer closeFS()
	return fs.ReadFile(fsys, name)
}

func skillURI(segments []string) (string, error) {
	if len(segments) == 0 || segments[0] == "" {
		return "", fmt.Errorf("skills: cannot construct a skill URI without a path")
	}
	u := &url.URL{Scheme: "skill", Host: segments[0]}
	if len(segments) > 1 {
		u.Path = "/" + strings.Join(segments[1:], "/")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

func escapePath(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func withinDir(dir, name string) bool {
	return dir == "." || name == dir || strings.HasPrefix(name, dir+"/")
}

func textualMIME(mimeType string) bool {
	return strings.HasPrefix(mimeType, "text/") || strings.HasPrefix(mimeType, "application/json") || strings.HasPrefix(mimeType, "application/xml") || strings.HasPrefix(mimeType, "application/yaml")
}

func resourceMIME(filename, relative string) string {
	if relative == "SKILL.md" {
		return "text/markdown"
	}
	return mime.TypeByExtension(path.Ext(filename))
}
