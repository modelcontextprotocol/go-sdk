// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectResourceClient(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "resources-test", Version: "v1"}, nil)
	if err := AddClient(client); err != nil {
		t.Fatal(err)
	}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := client.Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestResourceListingLiveAndMerged(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "Alpha skill.", map[string]string{"reference notes.md": "notes"})
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, &mcp.ServerOptions{PageSize: 1})
	readStatic := func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, Text: "registered"}}}, nil
	}
	server.AddResource(&mcp.Resource{URI: "config://one", Name: "one"}, readStatic)
	server.AddResource(&mcp.Resource{URI: "config://two", Name: "two"}, readStatic)
	if err := AddDirectory(server, dir, &DirectoryOptions{PageSize: 1}); err != nil {
		t.Fatal(err)
	}
	cs := connectResourceClient(t, server)
	list := func() []*mcp.Resource {
		t.Helper()
		params := &mcp.ListResourcesParams{}
		var resources []*mcp.Resource
		for range 20 {
			page, err := cs.ListResources(t.Context(), params)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Resources) > 1 {
				t.Fatalf("page has %d resources, want at most 1", len(page.Resources))
			}
			resources = append(resources, page.Resources...)
			if page.NextCursor == "" {
				return resources
			}
			params.Cursor = page.NextCursor
		}
		t.Fatal("resources/list did not terminate")
		return nil
	}
	check := func(want []string, description string) {
		t.Helper()
		var uris []string
		for _, resource := range list() {
			uris = append(uris, resource.URI)
			if resource.URI == "skill://alpha/SKILL.md" {
				if resource.Name != "alpha" || resource.Description != description || resource.MIMEType != "text/markdown" {
					t.Fatalf("incorrect SKILL.md metadata: %+v", resource)
				}
			}
		}
		if !slices.Equal(uris, want) {
			t.Fatalf("resources/list = %v, want %v", uris, want)
		}
	}
	check([]string{"config://one", "config://two", "skill://alpha/SKILL.md", "skill://alpha/reference%20notes.md"}, "Alpha skill.")
	writeSkill(t, dir, "beta", "Beta skill.", nil)
	writeSkill(t, dir, "alpha", "Updated description.", nil)
	if err := os.Remove(filepath.Join(dir, "alpha", "reference notes.md")); err != nil {
		t.Fatal(err)
	}
	server.RemoveResources("config://one")
	check([]string{"config://two", "skill://alpha/SKILL.md", "skill://beta/SKILL.md"}, "Updated description.")

	// Exact registrations win for both listing and reading, even when added later.
	server.AddResource(&mcp.Resource{URI: "skill://beta/SKILL.md", Name: "override"}, readStatic)
	resources := list()
	if len(resources) != 3 || resources[2].Name != "override" {
		t.Fatalf("duplicate URI not resolved to exact registration: %+v", resources)
	}
	read, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "skill://beta/SKILL.md"})
	if err != nil || read.Contents[0].Text != "registered" {
		t.Fatalf("ReadResource = %v, %v", read, err)
	}
	if _, err := cs.ListResources(t.Context(), &mcp.ListResourcesParams{Cursor: "%%%"}); err == nil {
		t.Fatal("accepted invalid cursor")
	}
}

func TestResourceAndDirectoryMetadataWithoutHashing(t *testing.T) {
	files := fstest.MapFS{
		"parent/SKILL.md":       {Data: []byte("---\nname: parent\ndescription: Parent.\n---\n")},
		"parent/child/SKILL.md": {Data: []byte("---\nname: child\ndescription: Child.\n---\n")},
		"parent/child/info.bin": {Data: []byte{0xff, 0, 1}},
		"unpublished.txt":       {Data: []byte("outside skills")},
	}
	fsys := &countingFS{FS: files, opens: make(map[string]int)}
	p, err := NewFSProvider(fsys, &DirectoryOptions{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	if err := p.AddTo(server); err != nil {
		t.Fatal(err)
	}
	cs := connectResourceClient(t, server)
	for _, description := range []string{"Child.", "Updated child."} {
		files["parent/child/SKILL.md"].Data = fmt.Appendf(nil, "---\nname: child\ndescription: %s\n---\n", description)
		var uris []string
		for resource, err := range cs.Resources(t.Context(), nil) {
			if err != nil {
				t.Fatal(err)
			}
			uris = append(uris, resource.URI)
		}
		want := []string{"skill://parent/SKILL.md", "skill://parent/child/SKILL.md", "skill://parent/child/info.bin"}
		if !slices.Equal(uris, want) {
			t.Fatalf("listed %v, want %v", uris, want)
		}
		for resource, err := range DirectoryEntries(t.Context(), cs, &ReadDirectoryParams{URI: "skill://parent/child"}) {
			if err != nil {
				t.Fatal(err)
			}
			if resource.URI == "skill://parent/child/SKILL.md" && (resource.Name != "child" || resource.Description != description) {
				t.Fatalf("directory metadata = %+v", resource)
			}
		}
	}
	if got := fsys.opens["parent/child/info.bin"]; got != 0 {
		t.Fatalf("listing read supporting bytes %d times", got)
	}
	delete(files, "parent/child/info.bin")
	files["parent/child/new.txt"] = &fstest.MapFile{Data: []byte("new")}
	var children []string
	for resource, err := range DirectoryEntries(t.Context(), cs, &ReadDirectoryParams{URI: "skill://parent/child"}) {
		if err != nil {
			t.Fatal(err)
		}
		children = append(children, resource.URI)
	}
	if want := []string{"skill://parent/child/SKILL.md", "skill://parent/child/new.txt"}; !slices.Equal(children, want) {
		t.Fatalf("live directory = %v, want %v", children, want)
	}
}

func TestCatalogRefreshFailureRetries(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
			files := fstest.MapFS{"demo/SKILL.md": {Data: []byte("---\nname: demo\ndescription: Demo.\n---\n")}}
			invalidate := make(chan struct{}, 1)
			p, err := NewFSProvider(files, &DirectoryOptions{Cache: &DirectoryCacheOptions{Preload: true, Invalidate: invalidate}})
			if err != nil {
				t.Fatal(err)
			}
			previous := p.cached
			files["new/SKILL.md"] = &fstest.MapFile{Data: []byte("incomplete write")}
			if explicit {
				err = p.Refresh(t.Context())
			} else {
				invalidate <- struct{}{}
				_, err = p.ListResources(t.Context(), nil)
			}
			if err == nil || p.cached != previous {
				t.Fatalf("failed refresh = %v; previous cache retained = %v", err, p.cached == previous)
			}
			if _, err := p.ListResources(t.Context(), nil); err == nil {
				t.Fatal("silently reused stale cache after a failed refresh")
			}
			files["new/SKILL.md"].Data = []byte("---\nname: new\ndescription: New.\n---\n")
			result, err := p.ListResources(t.Context(), nil)
			if err != nil || len(result.Resources) != 2 {
				t.Fatalf("retry without another invalidation = %v, %v", result, err)
			}
		})
	}
}

func TestResourceListingMiddlewareComposition(t *testing.T) {
	p, err := NewFSProvider(fstest.MapFS{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	params := &mcp.ListResourcesParams{Meta: mcp.Meta{"caller": "original"}}
	req := &mcp.ListResourcesRequest{Params: params, Extra: &mcp.RequestExtra{}}
	upstream := &mcp.ListResourcesResult{Meta: mcp.Meta{"source": "registered"}, Cacheable: mcp.Cacheable{TTLMs: 60000, CacheScope: "public"}}
	handler := p.listResourcesMiddleware(func(_ context.Context, _ string, got mcp.Request) (mcp.Result, error) {
		if got.GetExtra() != req.Extra || got.GetSession() != req.Session {
			t.Fatal("lost request context")
		}
		got.GetParams().GetMeta()["caller"] = "modified"
		return upstream, nil
	})
	result, err := handler(t.Context(), "resources/list", req)
	if err != nil {
		t.Fatal(err)
	}
	listed := result.(*mcp.ListResourcesResult)
	if listed.TTLMs != 0 || listed.CacheScope != "private" || listed.Meta["source"] != "registered" {
		t.Fatalf("merged cache policy and metadata = %+v", listed)
	}
	if upstream.TTLMs != 60000 || params.Meta["caller"] != "original" || params.Cursor != "" {
		t.Fatal("mutated upstream result or caller params")
	}
	upstreamErr := errors.New("access denied")
	handler = p.listResourcesMiddleware(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return nil, upstreamErr
	})
	if _, err := handler(t.Context(), "resources/list", req); !errors.Is(err, upstreamErr) {
		t.Fatalf("lost upstream error: %v", err)
	}
	handler = p.listResourcesMiddleware(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.ListResourcesResult{NextCursor: "loop"}, nil
	})
	if _, err := handler(t.Context(), "resources/list", req); err == nil {
		t.Fatal("accepted a repeated upstream cursor")
	}
}
