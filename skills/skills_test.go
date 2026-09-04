// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResourcesJSON(t *testing.T) {
	static := StaticResources(&Resource{URI: "skill://a/SKILL.md", Digest: "sha256:" + fmt.Sprintf("%064x", 1), Size: 1})
	data, err := json.Marshal(static)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `[{"uri":"skill://a/SKILL.md","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000001","size":1}]`; got != want {
		t.Fatalf("Marshal() = %s, want %s", got, want)
	}
	data, err = json.Marshal(DynamicResources())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"dynamic"` {
		t.Fatalf("Marshal() = %s", data)
	}
	var resources Resources
	if err := json.Unmarshal([]byte(`"dynamic"`), &resources); err != nil {
		t.Fatal(err)
	}
	if !resources.IsDynamic() {
		t.Fatal("dynamic resources were not preserved")
	}
	if err := json.Unmarshal([]byte(`null`), &resources); err == nil {
		t.Fatal("unmarshaling null resources succeeded")
	}
}

func TestValidateAndVerifySkill(t *testing.T) {
	content := []byte("---\nname: demo\ndescription: A demo skill.\nmetadata:\n  author: go-sdk\n---\n# Demo\n")
	digest := sha256.Sum256(content)
	skill := &Skill{
		URI: "skill://demo/SKILL.md",
		Frontmatter: Frontmatter{
			"name": "demo", "description": "A demo skill.",
			"metadata": map[string]any{"author": "go-sdk"},
		},
		Resources: StaticResources(&Resource{
			URI: "skill://demo/SKILL.md", Digest: fmt.Sprintf("sha256:%x", digest), Size: int64(len(content)),
		}),
	}
	if err := ValidateSkill(skill); err != nil {
		t.Fatal(err)
	}
	if err := VerifySkillMD(skill, content); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseFrontmatter(content)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["metadata"].(map[string]any); !ok {
		t.Fatalf("metadata has type %T, want map[string]any", parsed["metadata"])
	}

	bad := *skill
	bad.Frontmatter = Frontmatter{"name": "Demo", "description": "A demo skill."}
	if err := ValidateSkill(&bad); err == nil {
		t.Fatal("ValidateSkill accepted an uppercase name")
	}
	bad = *skill
	bad.Resources = StaticResources()
	if err := ValidateSkill(&bad); err == nil {
		t.Fatal("ValidateSkill accepted a manifest without SKILL.md")
	}

	dynamic := &Skill{
		URI:         "skill://generated/SKILL.md",
		Frontmatter: Frontmatter{"name": "generated", "description": "Generated on demand."},
		Resources:   DynamicResources(),
	}
	if err := ValidateSkill(dynamic); err != nil {
		t.Fatalf("ValidateSkill rejected dynamic resources: %v", err)
	}
	if err := VerifyResource(dynamic, dynamic.URI, content); !errors.Is(err, ErrDynamicResources) {
		t.Fatalf("VerifyResource(dynamic) = %v, want ErrDynamicResources", err)
	}
}

func TestPaginateSkills(t *testing.T) {
	input := []*Skill{{URI: "skill://c/SKILL.md"}, {URI: "skill://a/SKILL.md"}, {URI: "skill://b/SKILL.md"}}
	first, cursor, err := PaginateSkills(input, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := []string{first[0].URI, first[1].URI}, []string{"skill://a/SKILL.md", "skill://b/SKILL.md"}; !slices.Equal(got, want) {
		t.Fatalf("first page = %v, want %v", got, want)
	}
	second, next, err := PaginateSkills(input, cursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].URI != "skill://c/SKILL.md" || next != "" {
		t.Fatalf("second page = %v, cursor %q", second, next)
	}
	if input[0].URI != "skill://c/SKILL.md" {
		t.Fatal("PaginateSkills modified its input")
	}
}

func TestAllPagesReusable(t *testing.T) {
	seq := allPages("", func(cursor string) ([]string, string, error) {
		switch cursor {
		case "":
			return []string{"a"}, "next", nil
		case "next":
			return []string{"b"}, "", nil
		default:
			return nil, "", fmt.Errorf("unexpected cursor %q", cursor)
		}
	})
	for range 2 {
		var got []string
		for value, err := range seq {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, value)
		}
		if !slices.Equal(got, []string{"a", "b"}) {
			t.Fatalf("iteration yielded %v", got)
		}
	}
}

func TestGenericHandlersSupportDynamicResources(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "dynamic", Version: "v1"}, nil)
	skill := &Skill{
		URI:         "skill://generated/SKILL.md",
		Frontmatter: Frontmatter{"name": "generated", "description": "Generated on demand."},
		Resources:   DynamicResources(),
	}
	err := AddHandlers(server, &Handlers{
		List: func(context.Context, *mcp.ServerSession, *ListSkillsParams) (*ListSkillsResult, error) {
			return &ListSkillsResult{Skills: []*Skill{skill}}, nil
		},
		Get: func(_ context.Context, _ *mcp.ServerSession, params *GetSkillParams) (*GetSkillResult, error) {
			if params.URI != skill.URI {
				return nil, mcp.ResourceNotFoundError(params.URI)
			}
			return &GetSkillResult{Skill: skill}, nil
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v1"}, nil)
	if err := AddClient(client); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	result, err := List(ctx, cs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 1 || !result.Skills[0].Resources.IsDynamic() {
		t.Fatalf("List() = %+v", result)
	}
	if result.ResultType != "complete" {
		t.Fatalf("List() resultType = %q, want complete", result.ResultType)
	}
}

func TestValidateListResponseRejectsMissingSkills(t *testing.T) {
	if err := validateListResponse(context.Background(), &ListSkillsResult{}); err == nil {
		t.Fatal("validateListResponse accepted missing skills")
	}
}

func TestValidateDirectoryResultAllowsDisplayName(t *testing.T) {
	result := &ReadDirectoryResult{Resources: []*mcp.Resource{{
		URI: "skill://demo/SKILL.md", Name: "demo", MIMEType: "text/markdown",
	}}}
	if err := ValidateDirectoryResult("skill://demo", result); err != nil {
		t.Fatal(err)
	}
}

func TestListSkillsResultOmitsLegacyCacheFields(t *testing.T) {
	result := &ListSkillsResult{Skills: []*Skill{}, omitCache: true}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["ttlMs"]; ok {
		t.Fatalf("legacy result contains ttlMs: %s", data)
	}
	if _, ok := fields["cacheScope"]; ok {
		t.Fatalf("legacy result contains cacheScope: %s", data)
	}
}

func TestCustomAndUnsafeValidation(t *testing.T) {
	resources := make([]*Resource, DefaultMaxResourcesPerSkill+1)
	for i := range resources {
		uri := fmt.Sprintf("skill://large/%03d.txt", i)
		if i == 0 {
			uri = "skill://large/SKILL.md"
		}
		resources[i] = &Resource{URI: uri, Digest: "sha256:" + fmt.Sprintf("%064x", i), Size: 1}
	}
	skill := &Skill{
		URI:         "skill://large/SKILL.md",
		Frontmatter: Frontmatter{"name": "large", "description": "A large skill."},
		Resources:   StaticResources(resources...),
	}
	if err := ValidateSkill(skill); err == nil {
		t.Fatal("default validation accepted too many resources")
	}
	called := false
	options := &ServerOptions{
		Unsafe: &UnsafeOptions{Limits: &Limits{MaxResourcesPerSkill: len(resources), MaxTotalSize: 1024}},
		SkillValidators: []func(context.Context, *Skill) error{func(context.Context, *Skill) error {
			called = true
			return nil
		}},
	}
	if err := validateSkillResult(context.Background(), skill, options); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("custom validator was not called")
	}
}
