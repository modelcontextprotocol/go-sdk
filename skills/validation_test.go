// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestValidateSkill(t *testing.T) {
	set := func(key string, value any) func(*Skill) {
		return func(s *Skill) { s.Frontmatter[key] = value }
	}
	resources := func(entries ...*Resource) func(*Skill) {
		return func(s *Skill) { s.Resources = StaticResources(entries...) }
	}
	self := &Resource{URI: "skill://demo/SKILL.md", Digest: testDigest, Size: 1}

	for _, test := range []struct {
		name    string
		skill   *Skill
		wantErr bool
	}{
		{name: "valid", skill: testSkill()},
		{name: "dynamic", skill: skillWith(func(s *Skill) { s.Resources = DynamicResources() })},
		{name: "nil", wantErr: true},
		{name: "uri is not a SKILL.md", skill: skillWith(func(s *Skill) { s.URI = "skill://demo/other.md" }), wantErr: true},
		{name: "uri name does not match frontmatter", skill: skillWith(func(s *Skill) { s.URI = "skill://other/SKILL.md" }), wantErr: true},
		{name: "no frontmatter", skill: skillWith(func(s *Skill) { s.Frontmatter = nil }), wantErr: true},
		{name: "frontmatter is not JSON-compatible", skill: skillWith(set("extra", make(chan int))), wantErr: true},
		{name: "name is not a string", skill: skillWith(set("name", 1)), wantErr: true},
		{name: "description is missing", skill: skillWith(func(s *Skill) { delete(s.Frontmatter, "description") }), wantErr: true},
		{name: "description is empty", skill: skillWith(set("description", "")), wantErr: true},
		{name: "description is too long", skill: skillWith(set("description", strings.Repeat("é", 1025))), wantErr: true},
		{name: "compatibility is not a string", skill: skillWith(set("compatibility", 1)), wantErr: true},
		{name: "compatibility is too long", skill: skillWith(set("compatibility", strings.Repeat("é", 501))), wantErr: true},
		{name: "license is not a string", skill: skillWith(set("license", 1)), wantErr: true},
		{name: "metadata as map[string]string", skill: skillWith(set("metadata", map[string]string{"author": "go-sdk"}))},
		{name: "metadata is not an object", skill: skillWith(set("metadata", "author")), wantErr: true},
		{name: "metadata value is not a string", skill: skillWith(set("metadata", map[string]any{"count": 1})), wantErr: true},
		{name: "allowed-tools is not a string", skill: skillWith(set("allowed-tools", []string{"Bash"})), wantErr: true},
		{name: "resources unset", skill: skillWith(func(s *Skill) { s.Resources = Resources{} }), wantErr: true},
		{name: "resources omit SKILL.md", skill: skillWith(resources(&Resource{URI: "skill://demo/a.md", Digest: testDigest, Size: 1})), wantErr: true},
		{name: "nil resource", skill: skillWith(resources(self, nil)), wantErr: true},
		{name: "duplicate resource", skill: skillWith(resources(self, self)), wantErr: true},
		{name: "resource outside the skill root", skill: skillWith(resources(self, &Resource{URI: "skill://other/a.md", Digest: testDigest, Size: 1})), wantErr: true},
		{name: "resource is a directory", skill: skillWith(resources(self, &Resource{URI: "skill://demo/sub/", Digest: testDigest, Size: 1})), wantErr: true},
		{name: "invalid digest", skill: skillWith(resources(&Resource{URI: self.URI, Digest: "sha256:" + strings.Repeat("A", 64), Size: 1})), wantErr: true},
		{name: "negative size", skill: skillWith(resources(&Resource{URI: self.URI, Digest: testDigest, Size: -1})), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateSkill(test.skill); (err != nil) != test.wantErr {
				t.Fatalf("ValidateSkill() = %v, want error = %v", err, test.wantErr)
			}
		})
	}
}

func TestSkillNames(t *testing.T) {
	for _, test := range []struct {
		name string
		ok   bool
	}{
		{"demo", true}, {"café", true}, {"中文", true}, {"résumé-٢", true},
		{strings.Repeat("é", 64), true}, {strings.Repeat("é", 65), false},
		{"", false}, {"CAFÉ", false}, {"-demo", false}, {"demo-", false},
		{"demo--test", false}, {"demo_test", false}, {"demo/test", false},
		{"demo\xff", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			skill := &Skill{
				URI:         "skill://org/" + test.name + "/SKILL.md",
				Frontmatter: Frontmatter{"name": test.name, "description": "Demo"},
				Resources:   DynamicResources(),
			}
			if err := ValidateSkill(skill); (err == nil) != test.ok {
				t.Fatalf("ValidateSkill() = %v, want success = %v", err, test.ok)
			}
		})
	}
}

func TestParseURI(t *testing.T) {
	for _, test := range []struct {
		uri string
		ok  bool
	}{
		{"skill://demo/SKILL.md", true},
		{"https://example.com/a", true},
		{"skill://demo/../bad", false},
		{"skill://demo/%2e%2e", false},
		{"skill://demo/%2e", false},
		{"skill://demo/file?", false},
		{"skill://demo/file#", false},
		{"skill:opaque", false},
		{"/no-scheme", false},
		{"skill:///no-host", false},
		{"skill://user@demo/a", false},
		{"skill://demo:8080/a", false},
	} {
		t.Run(test.uri, func(t *testing.T) {
			if _, err := parseURI(test.uri); (err == nil) != test.ok {
				t.Fatalf("parseURI(%q) = %v, want success = %v", test.uri, err, test.ok)
			}
		})
	}
}

func TestValidateDirectoryResult(t *testing.T) {
	child := func(uri, name string) *mcp.Resource { return &mcp.Resource{URI: uri, Name: name} }
	for _, test := range []struct {
		name      string
		uri       string
		resources []*mcp.Resource
		nilResult bool
		wantErr   bool
	}{
		{name: "direct children", uri: "skill://demo", resources: []*mcp.Resource{child("skill://demo/SKILL.md", "demo"), child("skill://demo/sub", "sub")}},
		{name: "empty", uri: "skill://demo", resources: []*mcp.Resource{}},
		// Display names identify a child to a human, not to the protocol.
		{name: "duplicate display names", uri: "skill://demo", resources: []*mcp.Resource{child("skill://demo/a", "same"), child("skill://demo/b", "same")}},
		{name: "nil result", uri: "skill://demo", nilResult: true, wantErr: true},
		{name: "null resources", uri: "skill://demo", wantErr: true},
		{name: "trailing slash on the directory", uri: "skill://demo/", resources: []*mcp.Resource{}, wantErr: true},
		{name: "traversal child", uri: "skill://demo", resources: []*mcp.Resource{child("skill://demo/%2e%2e", "child")}, wantErr: true},
		{name: "child of another skill", uri: "skill://demo", resources: []*mcp.Resource{child("skill://other/a", "child")}, wantErr: true},
		{name: "grandchild", uri: "skill://demo", resources: []*mcp.Resource{child("skill://demo/a/b", "child")}, wantErr: true},
		{name: "encoded separator", uri: "skill://demo", resources: []*mcp.Resource{child("skill://demo/a%2fb", "child")}, wantErr: true},
		{name: "nil child", uri: "skill://demo", resources: []*mcp.Resource{nil}, wantErr: true},
		{name: "child without a name", uri: "skill://demo", resources: []*mcp.Resource{child("skill://demo/a", "")}, wantErr: true},
		{name: "duplicate child", uri: "skill://demo", resources: []*mcp.Resource{child("skill://demo/a", "a"), child("skill://demo/a", "a")}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var result *ReadDirectoryResult
			if !test.nilResult {
				result = &ReadDirectoryResult{Resources: test.resources}
			}
			if err := ValidateDirectoryResult(test.uri, result); (err != nil) != test.wantErr {
				t.Fatalf("ValidateDirectoryResult() = %v, want error = %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateListAndGetResult(t *testing.T) {
	skill := testSkill()
	for _, test := range []struct {
		name    string
		result  *ListSkillsResult
		wantErr bool
	}{
		{name: "ok", result: &ListSkillsResult{Skills: []*Skill{skill}}},
		{name: "nil result", wantErr: true},
		{name: "missing skills", result: &ListSkillsResult{}, wantErr: true},
		{name: "invalid skill", result: &ListSkillsResult{Skills: []*Skill{{URI: skill.URI}}}, wantErr: true},
		{name: "duplicate skill", result: &ListSkillsResult{Skills: []*Skill{skill, skill}}, wantErr: true},
	} {
		t.Run("list/"+test.name, func(t *testing.T) {
			if err := validateListResult(test.result, Limits{}); (err != nil) != test.wantErr {
				t.Fatalf("validateListResult() = %v, want error = %v", err, test.wantErr)
			}
		})
	}

	other := skillWith(func(s *Skill) {
		s.URI, s.Frontmatter = "skill://other/SKILL.md", Frontmatter{"name": "other", "description": "Other"}
	})
	for _, test := range []struct {
		name    string
		result  *GetSkillResult
		wantErr bool
	}{
		{name: "ok", result: &GetSkillResult{Skill: skill}},
		{name: "nil result", wantErr: true},
		{name: "nil skill", result: &GetSkillResult{}, wantErr: true},
		{name: "different uri", result: &GetSkillResult{Skill: other}, wantErr: true},
	} {
		t.Run("get/"+test.name, func(t *testing.T) {
			if err := validateGetResult(skill.URI, test.result, Limits{}); (err != nil) != test.wantErr {
				t.Fatalf("validateGetResult() = %v, want error = %v", err, test.wantErr)
			}
		})
	}
}

func TestResourceSizeRequired(t *testing.T) {
	for _, test := range []struct {
		name string
		size string
		ok   bool
	}{
		{"missing", "", false},
		{"null", `,"size":null`, false},
		{"zero", `,"size":0`, true},
		{"positive", `,"size":1`, true},
		{"negative", `,"size":-1`, false},
		{"fraction", `,"size":0.5`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := `{"uri":"skill://demo/SKILL.md","frontmatter":{"name":"demo","description":"Demo"},"resources":[{"uri":"skill://demo/SKILL.md","digest":"` + testDigest + `"` + test.size + `}]}`
			var skill Skill
			err := json.Unmarshal([]byte(data), &skill)
			if err == nil {
				err = ValidateSkill(&skill)
			}
			if (err == nil) != test.ok {
				t.Fatalf("decode and validate: %v, want success = %v", err, test.ok)
			}
		})
	}
}

func TestParseFrontmatter(t *testing.T) {
	for _, test := range []struct {
		name, content string
		wantErr       bool
	}{
		{name: "closing delimiter at EOF", content: "---\nname: demo\ndescription: Demo\n---"},
		{name: "crlf", content: "---\r\nname: demo\r\ndescription: Demo\r\n---"},
		{name: "trailing newline", content: "---\nname: demo\ndescription: Demo\n---\n"},
		{name: "body", content: "---\nname: demo\ndescription: Demo\n---\n# Demo\n"},
		{name: "no frontmatter", content: "# Demo\n", wantErr: true},
		{name: "no closing delimiter", content: "---\nname: demo\n", wantErr: true},
		{name: "empty frontmatter", content: "---\n\n---\n", wantErr: true},
		{name: "malformed yaml", content: "---\nname: [\n---\n", wantErr: true},
		{name: "non-string mapping key", content: "---\nname: demo\nextra:\n  1: a\n---\n", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseFrontmatter([]byte(test.content))
			if (err != nil) != test.wantErr {
				t.Fatalf("parseFrontmatter() = %v, %v, want error = %v", got, err, test.wantErr)
			}
			if err == nil && got["name"] != "demo" {
				t.Fatalf("parseFrontmatter() name = %v", got["name"])
			}
		})
	}

	// Nested YAML mappings normalize to map[string]any so that the result is
	// JSON-compatible and comparable with a decoded manifest.
	got, err := parseFrontmatter([]byte("---\nname: demo\nmetadata:\n  author: go-sdk\nlist:\n  - key: value\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["metadata"].(map[string]any); !ok {
		t.Errorf("metadata has type %T, want map[string]any", got["metadata"])
	}
	if _, ok := got["list"].([]any)[0].(map[string]any); !ok {
		t.Errorf("list item has type %T, want map[string]any", got["list"].([]any)[0])
	}
}

func TestVerify(t *testing.T) {
	content := []byte("---\nname: demo\ndescription: A demo skill.\nmetadata:\n  author: go-sdk\n---\n# Demo\n")
	frontmatter := Frontmatter{"name": "demo", "description": "A demo skill.", "metadata": map[string]any{"author": "go-sdk"}}
	static := &Skill{URI: "skill://demo/SKILL.md", Frontmatter: frontmatter, Resources: StaticResources(&Resource{
		URI: "skill://demo/SKILL.md", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(content)), Size: int64(len(content)),
	})}
	dynamic := &Skill{URI: static.URI, Frontmatter: frontmatter, Resources: DynamicResources()}

	t.Run("VerifyResource", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			skill   *Skill
			uri     string
			content []byte
			wantErr bool
		}{
			{name: "matching", skill: static, uri: static.URI, content: content},
			{name: "unlisted", skill: static, uri: "skill://demo/unlisted.md", content: content, wantErr: true},
			{name: "outside the root", skill: static, uri: "skill://other/a.md", content: content, wantErr: true},
			{name: "wrong size", skill: static, uri: static.URI, content: content[:len(content)-1], wantErr: true},
			{name: "wrong digest", skill: static, uri: static.URI, content: append(content[:len(content)-1:len(content)-1], '!'), wantErr: true},
			{name: "invalid skill", skill: skillWith(func(s *Skill) { s.Frontmatter = nil }), uri: static.URI, content: content, wantErr: true},
		} {
			t.Run(test.name, func(t *testing.T) {
				if err := VerifyResource(test.skill, test.uri, test.content); (err != nil) != test.wantErr {
					t.Fatalf("VerifyResource() = %v, want error = %v", err, test.wantErr)
				}
			})
		}
		if err := VerifyResource(dynamic, dynamic.URI, content); !errors.Is(err, ErrDynamicResources) {
			t.Fatalf("VerifyResource(dynamic) = %v, want ErrDynamicResources", err)
		}
	})

	t.Run("VerifySkillMD", func(t *testing.T) {
		if err := VerifySkillMD(nil, nil); err == nil {
			t.Error("VerifySkillMD accepted a nil skill")
		}
		if err := VerifySkillMD(static, content); err != nil {
			t.Error(err)
		}
		if err := VerifySkillMD(static, []byte("---\nname: demo\ndescription: Different.\n---\n")); err == nil {
			t.Error("VerifySkillMD accepted mismatched content")
		}

		// A dynamic manifest cannot be integrity-checked, so VerifySkillMD still
		// compares frontmatter and reports ErrDynamicResources only on a match.
		for _, test := range []struct {
			name    string
			content string
			matches bool
		}{
			{"matching", string(content), true},
			{"changed-description", "---\nname: demo\ndescription: Different instructions.\nmetadata:\n  author: go-sdk\n---\n", false},
			{"missing-metadata", "---\nname: demo\ndescription: A demo skill.\n---\n", false},
			{"extra-field", "---\nname: demo\ndescription: A demo skill.\nmetadata:\n  author: go-sdk\nallowed-tools: Bash\n---\n", false},
			{"malformed", "---\nname: [\n---\n", false},
		} {
			t.Run("dynamic/"+test.name, func(t *testing.T) {
				err := VerifySkillMD(dynamic, []byte(test.content))
				if err == nil {
					t.Fatal("dynamic content passed integrity verification")
				}
				if got := errors.Is(err, ErrDynamicResources); got != test.matches {
					t.Fatalf("VerifySkillMD() = %v; want ErrDynamicResources only for matching frontmatter", err)
				}
			})
		}
	})
}

func TestVerifyFrontmatterNumbers(t *testing.T) {
	for _, test := range []struct {
		name, advertised, yaml string
		matches                bool
	}{
		{"large-integer", "9007199254740993", "9007199254740993", true},
		{"changed-large-integer", "9007199254740992", "9007199254740993", false},
		{"uint64", "18446744073709551615", "18446744073709551615", true},
		{"exponent", "9007199254740993e0", "9007199254740993", true},
		{"decimal", "1.00", "1", true},
		{"fraction", "0.00100", "0.001", true},
		{"negative", "-12.30", "-12.3", true},
		{"zero", "-0.00e1000000000", "0", true},
		{"large-exponent", "1e1000000000", "1", false},
		{"small-exponent", "1e-1000000000", "0", false},
		{"string-is-not-number", `"1"`, "1", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := []byte("---\nname: demo\ndescription: Demo\nextra:\n  values: [" + test.yaml + "]\n---\nBody\n")
			data := `{"name":"demo","description":"Demo","extra":{"values":[` + test.advertised + `]}}`
			var fields Frontmatter
			if err := json.Unmarshal([]byte(data), &fields); err != nil {
				t.Fatal(err)
			}
			skill := &Skill{URI: "skill://demo/SKILL.md", Frontmatter: fields}
			for _, dynamic := range []bool{false, true} {
				skill.Resources = StaticResources(&Resource{
					URI: skill.URI, Size: int64(len(content)), Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(content)),
				})
				if dynamic {
					skill.Resources = DynamicResources()
				}
				err := VerifySkillMD(skill, content)
				matched := err == nil || dynamic && errors.Is(err, ErrDynamicResources)
				if matched != test.matches {
					t.Fatalf("dynamic=%v: VerifySkillMD() = %v, want match = %v", dynamic, err, test.matches)
				}
			}
		})
	}
}
