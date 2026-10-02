// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDirectoryProviderLiveAndPaginated(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "Alpha skill.", map[string]string{
		"references/ONE.md": "one",
		"references/TWO.md": "two",
	})
	writeSkill(t, dir, "beta", "Beta skill.", nil)

	server := mcp.NewServer(&mcp.Implementation{Name: "skills-test", Version: "v1"}, nil)
	if err := AddDirectory(server, dir, &DirectoryOptions{PageSize: 1}); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "skills-client", Version: "v1"}, nil)
	if err := AddClient(client); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	params := &ListSkillsParams{}
	var uris []string
	for skill, err := range All(ctx, clientSession, params) {
		if err != nil {
			t.Fatal(err)
		}
		uris = append(uris, skill.URI)
	}
	if want := []string{"skill://alpha/SKILL.md", "skill://beta/SKILL.md"}; !slices.Equal(uris, want) {
		t.Fatalf("All() = %v, want %v", uris, want)
	}
	if params.Cursor != "" {
		t.Fatalf("All mutated caller cursor to %q", params.Cursor)
	}

	writeSkill(t, dir, "gamma", "Gamma skill.", nil)
	result, err := Get(ctx, clientSession, &GetSkillParams{URI: "skill://gamma/SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Skill.Frontmatter["name"] != "gamma" {
		t.Fatalf("Get() returned %v", result.Skill.Frontmatter)
	}
	read, err := clientSession.ReadResource(ctx, &mcp.ReadResourceParams{URI: "skill://gamma/SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Contents) != 1 || read.Contents[0].Text == "" {
		t.Fatalf("ReadResource() = %+v", read)
	}
	var liveURIs []string
	for skill, err := range All(ctx, clientSession, nil) {
		if err != nil {
			t.Fatal(err)
		}
		liveURIs = append(liveURIs, skill.URI)
	}
	if want := []string{"skill://alpha/SKILL.md", "skill://beta/SKILL.md", "skill://gamma/SKILL.md"}; !slices.Equal(liveURIs, want) {
		t.Fatalf("live All() = %v, want %v", liveURIs, want)
	}

	directoryParams := &ReadDirectoryParams{URI: "skill://alpha"}
	var children []string
	for resource, err := range DirectoryEntries(ctx, clientSession, directoryParams) {
		if err != nil {
			t.Fatal(err)
		}
		children = append(children, resource.URI)
	}
	if want := []string{"skill://alpha/SKILL.md", "skill://alpha/references"}; !slices.Equal(children, want) {
		t.Fatalf("DirectoryEntries() = %v, want %v", children, want)
	}
	if directoryParams.Cursor != "" {
		t.Fatalf("DirectoryEntries mutated caller cursor to %q", directoryParams.Cursor)
	}

	if _, err := List(ctx, clientSession, &ListSkillsParams{Cursor: "%%%"}); err == nil {
		t.Fatal("List accepted an invalid cursor")
	}
	if _, err := ReadDirectory(ctx, clientSession, &ReadDirectoryParams{URI: "skill://alpha/SKILL.md"}); err == nil {
		t.Fatal("ReadDirectory accepted a file URI")
	}
	if err := os.RemoveAll(filepath.Join(dir, "beta")); err != nil {
		t.Fatal(err)
	}
	liveURIs = nil
	for skill, err := range All(ctx, clientSession, nil) {
		if err != nil {
			t.Fatal(err)
		}
		liveURIs = append(liveURIs, skill.URI)
	}
	if want := []string{"skill://alpha/SKILL.md", "skill://gamma/SKILL.md"}; !slices.Equal(liveURIs, want) {
		t.Fatalf("All() after removal = %v, want %v", liveURIs, want)
	}
}

func TestCatalogValidatorSeesAllSkills(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "Alpha skill.", nil)
	writeSkill(t, dir, "beta", "Beta skill.", nil)
	provider, err := NewDirectoryProvider(dir, &DirectoryOptions{
		PageSize: 1,
		CatalogValidators: []func(context.Context, []*Skill) error{
			func(_ context.Context, skills []*Skill) error {
				if len(skills) > 1 {
					return fmt.Errorf("too many skills")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ListSkills(context.Background(), nil, &ListSkillsParams{}); err == nil {
		t.Fatal("catalog validator did not see skills beyond the first page")
	}
}

func TestFSProviderRootSkill(t *testing.T) {
	provider, err := NewFSProvider(fstest.MapFS{
		"SKILL.md": {Data: []byte("---\nname: demo\ndescription: Demo skill.\n---\n# Demo\n")},
		"guide.md": {Data: []byte("guide")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.ListSkills(context.Background(), nil, &ListSkillsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 1 || result.Skills[0].URI != "skill://demo/SKILL.md" {
		t.Fatalf("ListSkills() = %+v", result.Skills)
	}
	directory, err := provider.ReadDirectory(context.Background(), nil, &ReadDirectoryParams{URI: "skill://demo"})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(directory.Resources); got != 2 {
		t.Fatalf("ReadDirectory() returned %d resources, want 2", got)
	}
}

func TestDirectoryProviderCatalogModes(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "Alpha skill.", nil)

	invalidate := make(chan struct{}, 1)
	startup, err := NewDirectoryProvider(dir, &DirectoryOptions{
		Cache: &DirectoryCacheOptions{Preload: true, Invalidate: invalidate},
	})
	if err != nil {
		t.Fatal(err)
	}
	lazy, err := NewDirectoryProvider(dir, &DirectoryOptions{
		Cache: &DirectoryCacheOptions{MaxAge: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeSkill(t, dir, "beta", "Beta skill.", nil)

	list := func(provider *DirectoryProvider) *ListSkillsResult {
		t.Helper()
		result, err := provider.ListSkills(context.Background(), nil, &ListSkillsParams{})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := len(list(startup).Skills); got != 1 {
		t.Fatalf("startup snapshot contains %d skills, want 1", got)
	}
	if got := len(list(lazy).Skills); got != 2 {
		t.Fatalf("lazy catalog contains %d skills, want 2", got)
	}
	writeSkill(t, dir, "gamma", "Gamma skill.", nil)
	invalidate <- struct{}{}
	if got := len(list(startup).Skills); got != 3 {
		t.Fatalf("invalidated startup catalog contains %d skills, want 3", got)
	}
	lazy.cacheMu.Lock()
	lazy.refreshedAt = time.Time{}
	lazy.cacheMu.Unlock()
	if got := len(list(lazy).Skills); got != 3 {
		t.Fatalf("refreshed lazy catalog contains %d skills, want 3", got)
	}
}

func TestDirectoryProviderExplicitRefresh(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "Alpha skill.", nil)
	provider, err := NewDirectoryProvider(dir, &DirectoryOptions{
		Cache: &DirectoryCacheOptions{Preload: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeSkill(t, dir, "beta", "Beta skill.", nil)
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := provider.ListSkills(context.Background(), nil, &ListSkillsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.Skills); got != 2 {
		t.Fatalf("refreshed catalog contains %d skills, want 2", got)
	}
}

func TestDirectoryProviderRejectsNegativeCacheMaxAge(t *testing.T) {
	fsys := fstest.MapFS{
		"SKILL.md": {Data: []byte("---\nname: demo\ndescription: Demo skill.\n---\n# Demo\n")},
	}
	options := &DirectoryOptions{Cache: &DirectoryCacheOptions{MaxAge: -time.Second}}
	if _, err := NewFSProvider(fsys, options); err == nil {
		t.Fatalf("NewFSProvider accepted options %+v", options)
	}
}

func TestDirectoryProviderRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "Alpha skill.", nil)
	if err := os.Symlink(filepath.Join(dir, "alpha", "SKILL.md"), filepath.Join(dir, "alpha", "linked.md")); err != nil {
		t.Skipf("creating symlink: %v", err)
	}
	provider, err := NewDirectoryProvider(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ListSkills(context.Background(), nil, &ListSkillsParams{}); err == nil {
		t.Fatal("provider accepted a symlink")
	}
}

func TestDirectoryProviderNestedSkillsAndEmptyDirectories(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "parent", "Parent skill.", map[string]string{
		"child/SKILL.md": "---\nname: child\ndescription: Child skill.\n---\n# Child\n",
		"child/info.txt": "child info",
	})
	if err := os.Mkdir(filepath.Join(dir, "parent", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	provider, err := NewDirectoryProvider(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, err := provider.ListSkills(context.Background(), nil, &ListSkillsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Skills) != 2 {
		t.Fatalf("got %d skills, want 2", len(list.Skills))
	}
	parent := list.Skills[0]
	if parent.URI != "skill://parent/SKILL.md" {
		t.Fatalf("first skill is %q", parent.URI)
	}
	resources, static := parent.Resources.List()
	if !static || len(resources) != 3 {
		t.Fatalf("parent resources = %v, static %v", resources, static)
	}
	empty, err := provider.ReadDirectory(context.Background(), nil, &ReadDirectoryParams{URI: "skill://parent/empty"})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Resources == nil || len(empty.Resources) != 0 {
		t.Fatalf("empty directory result = %+v", empty.Resources)
	}
}

func TestDirectoryProviderHashesNestedFilesOnce(t *testing.T) {
	fsys := &countingFS{
		FS: fstest.MapFS{
			"parent/SKILL.md":       {Data: []byte("---\nname: parent\ndescription: Parent skill.\n---\n")},
			"parent/child/SKILL.md": {Data: []byte("---\nname: child\ndescription: Child skill.\n---\n")},
			"parent/child/info.txt": {Data: []byte("child info")},
		},
		opens: make(map[string]int),
	}
	if _, err := NewFSProvider(fsys, &DirectoryOptions{
		Cache: &DirectoryCacheOptions{Preload: true},
	}); err != nil {
		t.Fatal(err)
	}
	if got := fsys.opens["parent/child/info.txt"]; got != 1 {
		t.Fatalf("nested file was opened %d times, want 1", got)
	}
}

func writeSkill(t *testing.T, root, name, description string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n# %s\n", name, description, name)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for filename, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(filename))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type countingFS struct {
	fs.FS
	opens map[string]int
}

func (f *countingFS) Open(name string) (fs.File, error) {
	f.opens[name]++
	return f.FS.Open(name)
}
