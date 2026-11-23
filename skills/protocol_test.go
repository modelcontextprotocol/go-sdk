// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testSkill() *Skill {
	return &Skill{URI: "skill://demo/SKILL.md", Frontmatter: Frontmatter{"name": "demo", "description": "Demo"},
		Resources: StaticResources(&Resource{URI: "skill://demo/SKILL.md", Digest: "sha256:" + strings.Repeat("0", 64), Size: 1})}
}

func testServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "skills-test", Version: "v1"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Resources: &mcp.ResourceCapabilities{}},
	})
}

func connectSkills(t *testing.T, server *mcp.Server, version string) *Client {
	t.Helper()
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: version >= "2026-07-28"}))
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

func TestUnknownURIsAndHandlerErrors(t *testing.T) {
	server := testServer()
	skill := testSkill()
	h := fixedHandlers(skill)
	h.Get = func(_ context.Context, _ *mcp.ServerSession, p *GetSkillParams) (*GetSkillResult, error) {
		switch p.URI {
		case "skill://nil/SKILL.md":
			return &GetSkillResult{}, nil
		case "skill://backend/SKILL.md":
			return nil, errors.New("backend unavailable")
		case "skill://wrong/SKILL.md":
			return &GetSkillResult{Skill: skill}, nil
		default:
			return nil, nil
		}
	}
	h.ReadDirectory = func(_ context.Context, _ *mcp.ServerSession, p *ReadDirectoryParams) (*ReadDirectoryResult, error) {
		if p.URI == "skill://empty" {
			return &ReadDirectoryResult{}, nil
		}
		return nil, nil
	}
	if err := AddHandlers(server, h, nil); err != nil {
		t.Fatal(err)
	}
	c := connectSkills(t, server, "2026-07-28")
	for _, uri := range []string{"skill://unknown/SKILL.md", "skill://nil/SKILL.md", "malformed"} {
		_, err := c.Get(t.Context(), &GetSkillParams{URI: uri})
		var rpc *jsonrpc.Error
		if !errors.As(err, &rpc) || rpc.Code != jsonrpc.CodeInvalidParams {
			t.Fatalf("%s: %v", uri, err)
		}
	}
	for _, uri := range []string{"skill://backend/SKILL.md", "skill://wrong/SKILL.md"} {
		_, err := c.Get(t.Context(), &GetSkillParams{URI: uri})
		var rpc *jsonrpc.Error
		if !errors.As(err, &rpc) || rpc.Code != jsonrpc.CodeInternalError {
			t.Fatalf("handler bug mislabeled: %v", err)
		}
	}
	if _, err := c.ReadDirectory(t.Context(), &ReadDirectoryParams{URI: "skill://missing"}); err == nil {
		t.Fatal("unknown directory accepted")
	} else {
		var rpc *jsonrpc.Error
		if !errors.As(err, &rpc) || rpc.Code != jsonrpc.CodeInvalidParams {
			t.Fatal(err)
		}
	}
	empty, err := c.ReadDirectory(t.Context(), &ReadDirectoryParams{URI: "skill://empty"})
	if err != nil || empty.Resources == nil || len(empty.Resources) != 0 {
		t.Fatalf("empty directory: %+v, %v", empty, err)
	}
}

func TestHandlerErrorCodes(t *testing.T) {
	coded := &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid request", Data: json.RawMessage(`{"reason":"test"}`)}
	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		for _, test := range []struct {
			name string
			err  error
			code int64
		}{
			{"backend", errors.New("backend unavailable"), jsonrpc.CodeInternalError},
			{"invalid-result", nil, jsonrpc.CodeInternalError},
			{"coded", coded, jsonrpc.CodeInvalidParams},
			{"wrapped-coded", fmt.Errorf("handler: %w", coded), jsonrpc.CodeInvalidParams},
		} {
			t.Run(version+"/"+test.name, func(t *testing.T) {
				server := testServer()
				skill := testSkill()
				skill.Frontmatter["description"] = false
				h := &Handlers{
					List: func(context.Context, *mcp.ServerSession, *ListSkillsParams) (*ListSkillsResult, error) {
						return &ListSkillsResult{Skills: []*Skill{skill}}, test.err
					},
					Get: func(context.Context, *mcp.ServerSession, *GetSkillParams) (*GetSkillResult, error) {
						return &GetSkillResult{Skill: skill}, test.err
					},
					ReadDirectory: func(context.Context, *mcp.ServerSession, *ReadDirectoryParams) (*ReadDirectoryResult, error) {
						return &ReadDirectoryResult{Resources: []*mcp.Resource{{URI: skill.URI}}}, test.err
					},
				}
				if err := AddHandlers(server, h, nil); err != nil {
					t.Fatal(err)
				}
				client := connectSkills(t, server, version)
				_, listErr := client.List(t.Context(), nil)
				_, getErr := client.Get(t.Context(), &GetSkillParams{URI: skill.URI})
				_, directoryErr := client.ReadDirectory(t.Context(), &ReadDirectoryParams{URI: "skill://demo"})
				for method, err := range map[string]error{MethodList: listErr, MethodGet: getErr, MethodReadDirectory: directoryErr} {
					var rpc *jsonrpc.Error
					if !errors.As(err, &rpc) || rpc.Code != test.code {
						t.Errorf("%s: error = %v, want code %d", method, err, test.code)
						continue
					}
					if test.code == coded.Code && string(rpc.Data) != string(coded.Data) {
						t.Errorf("%s: error data = %s, want %s", method, rpc.Data, coded.Data)
					}
				}
			})
		}
	}
}

func TestResponsesAndParamsAreNotMutated(t *testing.T) {
	server := testServer()
	skill := testSkill()
	list := &ListSkillsResult{Skills: []*Skill{skill}, ResultBase: mcp.ResultBase{Meta: mcp.Meta{"owner": "app"}}}
	get := &GetSkillResult{Skill: skill, ResultBase: mcp.ResultBase{Meta: mcp.Meta{"owner": "app"}}, Cacheable: mcp.Cacheable{TTLMs: 123, CacheScope: "private"}}
	dir := &ReadDirectoryResult{}
	h := &Handlers{
		List: func(context.Context, *mcp.ServerSession, *ListSkillsParams) (*ListSkillsResult, error) {
			return list, nil
		},
		Get: func(context.Context, *mcp.ServerSession, *GetSkillParams) (*GetSkillResult, error) { return get, nil },
		ReadDirectory: func(context.Context, *mcp.ServerSession, *ReadDirectoryParams) (*ReadDirectoryResult, error) {
			return dir, nil
		},
	}
	beforeList, _ := json.Marshal(list)
	beforeGet, _ := json.Marshal(get)
	beforeDir, _ := json.Marshal(dir)
	if err := AddHandlers(server, h, nil); err != nil {
		t.Fatal(err)
	}
	legacy := connectSkills(t, server, "2025-11-25")
	modern := connectSkills(t, server, "2026-07-28")
	var wg sync.WaitGroup
	for _, client := range []*Client{legacy, modern} {
		wg.Go(func() {
			for range 4 {
				p := &ListSkillsParams{ParamsBase: mcp.ParamsBase{Meta: mcp.Meta{"owner": "caller"}}}
				res, err := client.List(t.Context(), p)
				if err != nil {
					t.Error(err)
					return
				}
				if len(p.Meta) != 1 {
					t.Error("request metadata mutated")
				}
				// Re-encoding received legacy results need not preserve wire omission;
				// the decoder tracks actual presence separately.
				if res.cachePresent != (client == modern) {
					t.Error("wrong cache fields on wire")
				}
				gr, err := client.Get(t.Context(), &GetSkillParams{URI: skill.URI})
				if err != nil {
					t.Error(err)
					return
				}
				if gr.cachePresent != (client == modern) {
					t.Error("wrong get cache fields")
				}
				if client == modern && (gr.TTLMs != 123 || gr.CacheScope != "private") {
					t.Error("cache hints lost")
				}
				if _, err := client.ReadDirectory(t.Context(), &ReadDirectoryParams{URI: "skill://demo"}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	afterList, _ := json.Marshal(list)
	afterGet, _ := json.Marshal(get)
	afterDir, _ := json.Marshal(dir)
	if string(beforeList) != string(afterList) || string(beforeGet) != string(afterGet) || string(beforeDir) != string(afterDir) {
		t.Fatal("handler-owned result changed")
	}
}

type rawSkillResult struct {
	mcp.ResultBase
	data json.RawMessage
}

func (r *rawSkillResult) MarshalJSON() ([]byte, error) { return r.data, nil }

func TestClientRejectsMalformedResponses(t *testing.T) {
	good := testSkill()
	encoded, _ := json.Marshal(good)
	for _, tc := range []struct{ name, body string }{
		{"null-list", `{"skills":null,"resultType":"complete","ttlMs":0,"cacheScope":"public"}`},
		{"duplicate", fmt.Sprintf(`{"skills":[%s,%s],"resultType":"complete","ttlMs":0,"cacheScope":"public"}`, encoded, encoded)},
		{"missing-ttl", `{"skills":[],"resultType":"complete","cacheScope":"public"}`},
		{"missing-scope", `{"skills":[],"resultType":"complete","ttlMs":0}`},
		{"negative-ttl", `{"skills":[],"resultType":"complete","ttlMs":-1,"cacheScope":"public"}`},
		{"bad-scope", `{"skills":[],"resultType":"complete","ttlMs":0,"cacheScope":"unknown"}`},
		{"wrong-result-type", `{"skills":[],"resultType":"input_required","ttlMs":0,"cacheScope":"public"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := testServer()
			server.AddExtension(ExtensionID, nil)
			if err := mcp.AddReceivingCustomMethod(server, MethodList, func(context.Context, *mcp.ServerSession, *ListSkillsParams) (*rawSkillResult, error) {
				return &rawSkillResult{data: json.RawMessage(tc.body)}, nil
			}); err != nil {
				t.Fatal(err)
			}
			c := connectSkills(t, server, "2026-07-28")
			if _, err := c.List(t.Context(), nil); err == nil {
				t.Fatal("accepted malformed response")
			}
		})
	}
	for _, body := range []string{
		fmt.Sprintf(`{"skill":%s,"resultType":"complete","ttlMs":0}`, encoded),
		fmt.Sprintf(`{"skill":%s,"resultType":"complete","cacheScope":"public"}`, encoded),
		`{"skill":null,"resultType":"complete","ttlMs":0,"cacheScope":"public"}`,
	} {
		server := testServer()
		server.AddExtension(ExtensionID, nil)
		if err := mcp.AddReceivingCustomMethod(server, MethodGet, func(context.Context, *mcp.ServerSession, *GetSkillParams) (*rawSkillResult, error) {
			return &rawSkillResult{data: json.RawMessage(body)}, nil
		}); err != nil {
			t.Fatal(err)
		}
		c := connectSkills(t, server, "2026-07-28")
		if _, err := c.Get(t.Context(), &GetSkillParams{URI: good.URI}); err == nil {
			t.Fatal("accepted malformed get")
		}
	}
}

func TestPaginationAndIteratorOwnership(t *testing.T) {
	server := testServer()
	one := testSkill()
	two := *one
	two.URI = "skill://other/SKILL.md"
	two.Frontmatter = Frontmatter{"name": "other", "description": "Other"}
	two.Resources = DynamicResources()
	h := fixedHandlers(one)
	h.List = func(_ context.Context, _ *mcp.ServerSession, p *ListSkillsParams) (*ListSkillsResult, error) {
		page, next, err := PaginateSkills([]*Skill{&two, one}, p.Cursor, 1)
		return &ListSkillsResult{Skills: page, NextCursor: next}, err
	}
	if err := AddHandlers(server, h, nil); err != nil {
		t.Fatal(err)
	}
	c := connectSkills(t, server, "2026-07-28")
	p := &ListSkillsParams{ParamsBase: mcp.ParamsBase{Meta: mcp.Meta{"key": "value"}}}
	seq := c.All(t.Context(), p)
	for range 2 {
		count := 0
		for _, err := range seq {
			if err != nil {
				t.Fatal(err)
			}
			count++
		}
		if count != 2 {
			t.Fatalf("count=%d", count)
		}
	}
	if p.Cursor != "" || !reflect.DeepEqual(p.Meta, mcp.Meta{"key": "value"}) {
		t.Fatal("iterator mutated parameters")
	}
	calls := 0
	for _, err := range allPages("", func(string) ([]int, string, error) { calls++; return []int{1}, "repeat", nil }) {
		if err != nil {
			break
		}
	}
	if calls != 2 {
		t.Fatalf("repeated cursor: %d calls", calls)
	}
	calls = 0
	for range allPages("", func(string) ([]int, string, error) { calls++; return []int{1}, "next", nil }) {
		break
	}
	if calls != 1 {
		t.Fatal("iterator fetched after early stop")
	}
	for _, items := range [][]*Skill{{nil}, {one, one}} {
		if _, _, err := PaginateSkills(items, "", 1); err == nil {
			t.Fatal("invalid pagination input accepted")
		}
	}
}

func TestInvalidPaginationCursors(t *testing.T) {
	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		t.Run(version, func(t *testing.T) {
			server := testServer()
			skill := testSkill()
			h := fixedHandlers(skill)
			h.List = func(_ context.Context, _ *mcp.ServerSession, p *ListSkillsParams) (*ListSkillsResult, error) {
				page, next, err := PaginateSkills([]*Skill{skill}, p.Cursor, 0)
				return &ListSkillsResult{Skills: page, NextCursor: next}, err
			}
			h.ReadDirectory = func(_ context.Context, _ *mcp.ServerSession, p *ReadDirectoryParams) (*ReadDirectoryResult, error) {
				page, next, err := PaginateDirectoryResources([]*mcp.Resource{{URI: skill.URI, Name: "demo"}}, p.Cursor, 0)
				return &ReadDirectoryResult{Resources: page, NextCursor: next}, err
			}
			if err := AddHandlers(server, h, nil); err != nil {
				t.Fatal(err)
			}
			c := connectSkills(t, server, version)
			_, listErr := c.List(t.Context(), &ListSkillsParams{Cursor: "%"})
			_, directoryErr := c.ReadDirectory(t.Context(), &ReadDirectoryParams{URI: "skill://demo", Cursor: "%"})
			for method, err := range map[string]error{MethodList: listErr, MethodReadDirectory: directoryErr} {
				var rpc *jsonrpc.Error
				if !errors.As(err, &rpc) || rpc.Code != jsonrpc.CodeInvalidParams {
					t.Errorf("%s: got %v, want JSON-RPC Invalid Params", method, err)
				}
			}
		})
	}
}

func TestURIAndVerificationBoundaries(t *testing.T) {
	for _, uri := range []string{"skill://demo/../bad", "skill://demo/%2e%2e", "skill://demo/%2e", "skill://demo/file?", "skill://demo/file#", "skill:opaque"} {
		if _, err := parseURI(uri); err == nil {
			t.Fatalf("accepted %s", uri)
		}
	}
	for _, uri := range []string{"skill://demo/%2e%2e", "skill://other/a", "skill://demo/a/b", "skill://demo/a%2fb", "skill://demo/sub%2f"} {
		if err := ValidateDirectoryResult("skill://demo", &ReadDirectoryResult{Resources: []*mcp.Resource{{URI: uri, Name: "child"}}}); err == nil {
			t.Fatalf("accepted child %s", uri)
		}
	}
	if err := VerifySkillMD(nil, nil); err == nil {
		t.Fatal("nil skill accepted")
	}
	skill := testSkill()
	data := []byte("tampered")
	if err := VerifyResource(skill, "skill://demo/unlisted", data); err == nil {
		t.Fatal("unlisted file accepted")
	}
	if err := VerifyResource(skill, skill.URI, data); err == nil {
		t.Fatal("wrong content accepted")
	}
}

func TestFrontmatterAtEOF(t *testing.T) {
	for _, content := range []string{
		"---\nname: demo\ndescription: Demo\n---",
		"---\r\nname: demo\r\ndescription: Demo\r\n---",
		"---\nname: demo\ndescription: Demo\n---\n",
	} {
		got, err := parseFrontmatter([]byte(content))
		if err != nil || got["name"] != "demo" {
			t.Fatalf("frontmatter: %v, %v", got, err)
		}
	}
}

func TestDirectoryDisplayNamesNeedNotBeUnique(t *testing.T) {
	result := &ReadDirectoryResult{Resources: []*mcp.Resource{
		{URI: "skill://demo/a", Name: "Same display name"},
		{URI: "skill://demo/b", Name: "Same display name"},
	}}
	if err := ValidateDirectoryResult("skill://demo", result); err != nil {
		t.Fatal(err)
	}
}
