// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// testDigest is a syntactically valid SHA-256 digest. Tests that check content
// integrity compute a real digest instead.
var testDigest = "sha256:" + strings.Repeat("0", 64)

func testSkill() *Skill {
	return &Skill{URI: "skill://demo/SKILL.md", Frontmatter: Frontmatter{"name": "demo", "description": "Demo"},
		Resources: StaticResources(&Resource{URI: "skill://demo/SKILL.md", Digest: testDigest, Size: 1})}
}

// skillWith returns a valid skill with mutate applied, for table rows that vary
// one field at a time.
func skillWith(mutate func(*Skill)) *Skill {
	skill := testSkill()
	mutate(skill)
	return skill
}

func testServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "skills-test", Version: "v1"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Resources: &mcp.ResourceCapabilities{}},
	})
}

func connectSkills(t *testing.T, server *mcp.Server, version string) *Client {
	t.Helper()
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: version >= protocolVersionCaching}))
	t.Cleanup(httpServer.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil)
	if err := AddMethods(client); err != nil {
		t.Fatal(err)
	}
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return &Client{Session: session}
}

func fixedHandlers(skill *Skill) *Handlers {
	return &Handlers{
		List: func(context.Context, *mcp.ServerSession, *ListSkillsParams) (*ListSkillsResult, error) {
			return &ListSkillsResult{Skills: []*Skill{skill}}, nil
		},
		Get: func(_ context.Context, _ *mcp.ServerSession, p *GetSkillParams) (*GetSkillResult, error) {
			if p.URI != skill.URI {
				return nil, nil
			}
			return &GetSkillResult{Skill: skill}, nil
		},
	}
}

func TestResourcesJSON(t *testing.T) {
	for _, test := range []struct {
		name      string
		resources Resources
		want      string // "" means Marshal must fail
	}{
		{name: "static", resources: StaticResources(&Resource{URI: "skill://a/SKILL.md", Digest: testDigest, Size: 1}),
			want: `[{"uri":"skill://a/SKILL.md","digest":"` + testDigest + `","size":1}]`},
		{name: "empty static", resources: StaticResources(), want: `[]`},
		{name: "dynamic", resources: DynamicResources(), want: `"dynamic"`},
		{name: "unset", resources: Resources{}},
	} {
		t.Run("encode/"+test.name, func(t *testing.T) {
			data, err := json.Marshal(test.resources)
			if (err == nil) != (test.want != "") {
				t.Fatalf("Marshal() = %s, %v, want %q", data, err, test.want)
			}
			if err == nil && string(data) != test.want {
				t.Fatalf("Marshal() = %s, want %s", data, test.want)
			}
		})
	}

	for _, test := range []struct {
		data             string
		wantErr, wantDyn bool
	}{
		{data: `"dynamic"`, wantDyn: true},
		{data: `"dynamic"`, wantDyn: true},
		{data: ` "dynamic" `, wantDyn: true},
		{data: `[]`},
		{data: `"other"`, wantErr: true},
		{data: `null`, wantErr: true},
		{data: `42`, wantErr: true},
		{data: `{}`, wantErr: true},
	} {
		t.Run("decode/"+test.data, func(t *testing.T) {
			var resources Resources
			err := json.Unmarshal([]byte(test.data), &resources)
			if (err != nil) != test.wantErr {
				t.Fatalf("Unmarshal() error = %v, want error = %v", err, test.wantErr)
			}
			if err == nil && resources.IsDynamic() != test.wantDyn {
				t.Fatalf("IsDynamic() = %v, want %v", resources.IsDynamic(), test.wantDyn)
			}
		})
	}
}

func TestPaginate(t *testing.T) {
	cursor := func(uri string) string { return base64.RawURLEncoding.EncodeToString([]byte(uri)) }
	a, b, c := &Skill{URI: "skill://a/SKILL.md"}, &Skill{URI: "skill://b/SKILL.md"}, &Skill{URI: "skill://c/SKILL.md"}
	unsorted := []*Skill{c, a, b}

	for _, test := range []struct {
		name     string
		input    []*Skill
		cursor   string
		pageSize int
		want     []string
		wantNext string
		wantErr  bool
	}{
		{name: "sorts by uri", input: unsorted, pageSize: 2, want: []string{a.URI, b.URI}, wantNext: cursor(b.URI)},
		{name: "resumes after cursor", input: unsorted, cursor: cursor(b.URI), pageSize: 2, want: []string{c.URI}},
		{name: "cursor past the end", input: unsorted, cursor: cursor("skill://z/SKILL.md"), pageSize: 2},
		{name: "default page size", input: unsorted, want: []string{a.URI, b.URI, c.URI}},
		{name: "empty input", pageSize: 2},
		{name: "negative page size", input: unsorted, pageSize: -1, wantErr: true},
		{name: "invalid cursor", input: unsorted, cursor: "%", pageSize: 1, wantErr: true},
		{name: "nil entry", input: []*Skill{nil}, pageSize: 1, wantErr: true},
		{name: "duplicate uri", input: []*Skill{a, a}, pageSize: 1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, next, err := PaginateSkills(test.input, test.cursor, test.pageSize)
			if (err != nil) != test.wantErr {
				t.Fatalf("PaginateSkills() error = %v, want error = %v", err, test.wantErr)
			}
			if err != nil {
				return
			}
			var got []string
			for _, skill := range page {
				got = append(got, skill.URI)
			}
			if !slices.Equal(got, test.want) || next != test.wantNext || page == nil {
				t.Fatalf("PaginateSkills() = %v, %q, want %v, %q", got, next, test.want, test.wantNext)
			}
		})
	}
	if unsorted[0] != c {
		t.Error("PaginateSkills modified its input")
	}

	// PaginateDirectoryResources shares paginate; check only its key function.
	page, next, err := PaginateDirectoryResources([]*mcp.Resource{{URI: "skill://demo/b"}, {URI: "skill://demo/a"}}, "", 1)
	if err != nil || len(page) != 1 || page[0].URI != "skill://demo/a" || next != cursor("skill://demo/a") {
		t.Fatalf("PaginateDirectoryResources() = %v, %q, %v", page, next, err)
	}
	if _, _, err := PaginateDirectoryResources([]*mcp.Resource{nil}, "", 1); err == nil {
		t.Error("PaginateDirectoryResources accepted a nil resource")
	}
}

func TestAllPages(t *testing.T) {
	t.Run("reusable", func(t *testing.T) {
		calls := 0
		seq := allPages("", func(cursor string) ([]string, string, error) {
			calls++
			if cursor == "" {
				return []string{"a"}, "next", nil
			}
			return []string{"b"}, "", nil
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
		if calls != 4 {
			t.Fatalf("re-iterating made %d fetches, want 4", calls)
		}
	})

	// Each of these must stop the walk after a bounded number of fetches: a
	// server repeating a cursor would otherwise loop forever, and a consumer
	// breaking out must not trigger another fetch.
	for _, test := range []struct {
		name, initial, next string
		fetchErr            bool
		stopEarly           bool
		wantCalls           int
	}{
		{name: "repeated cursor", next: "repeat", wantCalls: 2},
		{name: "cursor equals the initial cursor", initial: "start", next: "start", wantCalls: 1},
		{name: "fetch error", fetchErr: true, wantCalls: 1},
		{name: "consumer stops early", next: "next", stopEarly: true, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls, failed := 0, false
			for _, err := range allPages(test.initial, func(string) ([]int, string, error) {
				calls++
				if test.fetchErr {
					return nil, "", fmt.Errorf("boom")
				}
				return []int{1}, test.next, nil
			}) {
				if err != nil {
					failed = true
				}
				if err != nil || test.stopEarly {
					break
				}
			}
			if failed == test.stopEarly {
				t.Errorf("yielded an error = %v, want %v", failed, !test.stopEarly)
			}
			if calls != test.wantCalls {
				t.Errorf("made %d fetches, want %d", calls, test.wantCalls)
			}
		})
	}
}

func TestResultCacheFieldsOmittedForLegacyProtocol(t *testing.T) {
	for name, result := range map[string]json.Marshaler{
		"list": &ListSkillsResult{Skills: []*Skill{}, omitCache: true},
		"get":  &GetSkillResult{Skill: testSkill(), omitCache: true},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"ttlMs", "cacheScope", "resultType"} {
				if _, ok := fields[key]; ok {
					t.Errorf("legacy result contains %s: %s", key, data)
				}
			}
		})
	}
}
