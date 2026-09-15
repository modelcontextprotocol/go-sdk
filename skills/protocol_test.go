// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// wantRPCCode reports whether err is a JSON-RPC error with the given code.
func wantRPCCode(t *testing.T, context string, err error, code int64) *jsonrpc.Error {
	t.Helper()
	var rpc *jsonrpc.Error
	if !errors.As(err, &rpc) {
		t.Errorf("%s: error = %v, want a JSON-RPC error with code %d", context, err, code)
		return nil
	}
	if rpc.Code != code {
		t.Errorf("%s: error code = %d (%v), want %d", context, rpc.Code, err, code)
	}
	return rpc
}

// TestSpecErrorScenarios covers wire behavior that the SEP-2640 server
// conformance scenarios (sep-2640-skills-*) also check. Once those scenarios
// run in CI against ./conformance/skills-server, this test can be deleted.
func TestSpecErrorScenarios(t *testing.T) {
	server := testServer()
	skill := testSkill()
	handlers := fixedHandlers(skill)
	handlers.List = func(_ context.Context, _ *mcp.ServerSession, p *ListSkillsParams) (*ListSkillsResult, error) {
		page, next, err := PaginateSkills([]*Skill{skill}, p.Cursor, 0)
		return &ListSkillsResult{Skills: page, NextCursor: next}, err
	}
	handlers.ReadDirectory = func(_ context.Context, _ *mcp.ServerSession, p *ReadDirectoryParams) (*ReadDirectoryResult, error) {
		switch p.URI {
		case "skill://demo":
			page, next, err := PaginateDirectoryResources([]*mcp.Resource{{URI: skill.URI, Name: "demo"}}, p.Cursor, 0)
			return &ReadDirectoryResult{Resources: page, NextCursor: next}, err
		case "skill://empty":
			return &ReadDirectoryResult{}, nil
		default:
			return nil, nil // unknown directory
		}
	}
	if err := AddHandlers(server, handlers, nil); err != nil {
		t.Fatal(err)
	}
	client := connectSkills(t, server, protocolVersionCaching)

	t.Run("get", func(t *testing.T) {
		for _, test := range []struct {
			name, uri string
		}{
			{"unknown skill", "skill://unknown/SKILL.md"},
			{"malformed uri", "malformed"},
			{"not a SKILL.md", "skill://demo/other.md"},
		} {
			t.Run(test.name, func(t *testing.T) {
				_, err := client.Get(t.Context(), &GetSkillParams{URI: test.uri})
				wantRPCCode(t, test.uri, err, jsonrpc.CodeInvalidParams)
			})
		}
	})

	t.Run("directory", func(t *testing.T) {
		if _, err := client.ReadDirectory(t.Context(), &ReadDirectoryParams{URI: "skill://missing"}); err != nil {
			wantRPCCode(t, "unknown directory", err, jsonrpc.CodeInvalidParams)
		} else {
			t.Error("unknown directory accepted")
		}
		// An empty directory is a success with an empty, non-null array.
		empty, err := client.ReadDirectory(t.Context(), &ReadDirectoryParams{URI: "skill://empty"})
		if err != nil || empty.Resources == nil || len(empty.Resources) != 0 {
			t.Errorf("empty directory = %+v, %v", empty, err)
		}
	})

	t.Run("invalid cursor", func(t *testing.T) {
		_, listErr := client.List(t.Context(), &ListSkillsParams{Cursor: "%"})
		_, directoryErr := client.ReadDirectory(t.Context(), &ReadDirectoryParams{URI: "skill://demo", Cursor: "%"})
		for method, err := range map[string]error{MethodList: listErr, MethodReadDirectory: directoryErr} {
			wantRPCCode(t, method, err, jsonrpc.CodeInvalidParams)
		}
	})
}

// TestHandlerErrorMapping covers how AddHandlers translates a Go handler's
// return values into JSON-RPC errors. This mapping is SDK behavior and is not
// visible to the conformance suite.
func TestHandlerErrorMapping(t *testing.T) {
	coded := &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid request", Data: json.RawMessage(`{"reason":"test"}`)}
	for _, test := range []struct {
		name     string
		err      error
		invalid  bool // the handler also returns a structurally invalid result
		code     int64
		wantData bool
	}{
		{name: "plain error becomes internal", err: errors.New("backend unavailable"), code: jsonrpc.CodeInternalError},
		{name: "invalid result becomes internal", invalid: true, code: jsonrpc.CodeInternalError},
		{name: "coded error is preserved", err: coded, code: jsonrpc.CodeInvalidParams, wantData: true},
		{name: "wrapped coded error is preserved", err: fmt.Errorf("handler: %w", coded), code: jsonrpc.CodeInvalidParams, wantData: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := testServer()
			skill := testSkill()
			if test.invalid || test.err == nil {
				skill.Frontmatter["description"] = false // fails validateSkill
			}
			handlers := &Handlers{
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
			if err := AddHandlers(server, handlers, nil); err != nil {
				t.Fatal(err)
			}
			client := connectSkills(t, server, protocolVersionCaching)
			_, listErr := client.List(t.Context(), nil)
			_, getErr := client.Get(t.Context(), &GetSkillParams{URI: skill.URI})
			_, directoryErr := client.ReadDirectory(t.Context(), &ReadDirectoryParams{URI: "skill://demo"})
			for method, err := range map[string]error{MethodList: listErr, MethodGet: getErr, MethodReadDirectory: directoryErr} {
				rpc := wantRPCCode(t, method, err, test.code)
				if rpc != nil && test.wantData && string(rpc.Data) != string(coded.Data) {
					t.Errorf("%s: error data = %s, want %s", method, rpc.Data, coded.Data)
				}
			}
		})
	}

	t.Run("nil list result", func(t *testing.T) {
		server := testServer()
		handlers := fixedHandlers(testSkill())
		handlers.List = func(context.Context, *mcp.ServerSession, *ListSkillsParams) (*ListSkillsResult, error) {
			return nil, nil
		}
		if err := AddHandlers(server, handlers, nil); err != nil {
			t.Fatal(err)
		}
		_, err := connectSkills(t, server, protocolVersionCaching).List(t.Context(), nil)
		wantRPCCode(t, MethodList, err, jsonrpc.CodeInternalError)
	})
}

// TestClientRequiresCapabilities checks the guards that run before a request is
// sent. A server that advertises neither the extension nor directoryRead must be
// rejected locally rather than called.
func TestClientRequiresCapabilities(t *testing.T) {
	ctx := t.Context()
	if _, err := (&Client{}).List(ctx, nil); err == nil {
		t.Error("client without a session accepted List")
	}
	// A server with no Skills handlers does not advertise the extension.
	if _, err := connectSkills(t, testServer(), protocolVersionCaching).List(ctx, nil); err == nil {
		t.Error("client called a server that does not advertise the extension")
	}
	// Handlers without ReadDirectory advertise the extension but not directoryRead.
	server := testServer()
	if err := AddHandlers(server, fixedHandlers(testSkill()), nil); err != nil {
		t.Fatal(err)
	}
	client := connectSkills(t, server, protocolVersionCaching)
	if _, err := client.ReadDirectory(ctx, &ReadDirectoryParams{URI: "skill://demo"}); err == nil {
		t.Error("client called resources/directory/read without the capability")
	}
	for _, params := range []*GetSkillParams{nil, {}} {
		if _, err := client.Get(ctx, params); err == nil {
			t.Errorf("Get accepted %+v", params)
		}
	}
}

// TestResponsesAndParamsAreNotMutated checks the ownership contract: handler
// results and caller parameters are copied, never modified in place, even under
// concurrent use across two protocol versions.
func TestResponsesAndParamsAreNotMutated(t *testing.T) {
	server := testServer()
	skill := testSkill()
	list := &ListSkillsResult{Skills: []*Skill{skill}, ResultBase: mcp.ResultBase{Meta: mcp.Meta{"owner": "app"}}}
	get := &GetSkillResult{Skill: skill, ResultBase: mcp.ResultBase{Meta: mcp.Meta{"owner": "app"}}, Cacheable: mcp.Cacheable{TTLMs: 123, CacheScope: "private"}}
	dir := &ReadDirectoryResult{}
	handlers := &Handlers{
		List: func(context.Context, *mcp.ServerSession, *ListSkillsParams) (*ListSkillsResult, error) {
			return list, nil
		},
		Get: func(context.Context, *mcp.ServerSession, *GetSkillParams) (*GetSkillResult, error) { return get, nil },
		ReadDirectory: func(context.Context, *mcp.ServerSession, *ReadDirectoryParams) (*ReadDirectoryResult, error) {
			return dir, nil
		},
	}
	before := map[string][]byte{}
	for name, result := range map[string]any{"list": list, "get": get, "dir": dir} {
		before[name], _ = json.Marshal(result)
	}
	if err := AddHandlers(server, handlers, nil); err != nil {
		t.Fatal(err)
	}
	// The legacy version omits the cache hints; the modern one requires them.
	clients := map[string]*Client{"2025-11-25": connectSkills(t, server, "2025-11-25"), protocolVersionCaching: connectSkills(t, server, protocolVersionCaching)}
	var wg sync.WaitGroup
	for version, client := range clients {
		modern := version == protocolVersionCaching
		wg.Go(func() {
			for range 4 {
				params := &ListSkillsParams{ParamsBase: mcp.ParamsBase{Meta: mcp.Meta{"owner": "caller"}}}
				listResult, err := client.List(t.Context(), params)
				if err != nil {
					t.Error(err)
					return
				}
				if !reflect.DeepEqual(params.Meta, mcp.Meta{"owner": "caller"}) {
					t.Errorf("%s: request metadata mutated", version)
				}
				// Re-encoding a received legacy result need not preserve wire
				// omission; the decoder tracks actual presence separately.
				if listResult.cachePresent != modern {
					t.Errorf("%s: list cachePresent = %v", version, listResult.cachePresent)
				}
				getResult, err := client.Get(t.Context(), &GetSkillParams{URI: skill.URI})
				if err != nil {
					t.Error(err)
					return
				}
				if getResult.cachePresent != modern {
					t.Errorf("%s: get cachePresent = %v", version, getResult.cachePresent)
				}
				if modern && (getResult.TTLMs != 123 || getResult.CacheScope != "private") {
					t.Errorf("%s: cache hints lost", version)
				}
				if _, err := client.ReadDirectory(t.Context(), &ReadDirectoryParams{URI: "skill://demo"}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	for name, result := range map[string]any{"list": list, "get": get, "dir": dir} {
		after, _ := json.Marshal(result)
		if string(after) != string(before[name]) {
			t.Errorf("handler-owned %s result changed:\n got %s\nwant %s", name, after, before[name])
		}
	}
}

type rawResult struct {
	mcp.ResultBase
	data json.RawMessage
}

func (r *rawResult) MarshalJSON() ([]byte, error) { return r.data, nil }

// TestClientRejectsMalformedResponses feeds hand-built bodies past the server's
// own validation. The conformance suite drives servers, not clients, so nothing
// else covers these paths.
func TestClientRejectsMalformedResponses(t *testing.T) {
	skill := testSkill()
	encoded, err := json.Marshal(skill)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, method, body string
	}{
		{"list/null-skills", MethodList, `{"skills":null,"resultType":"complete","ttlMs":0,"cacheScope":"public"}`},
		{"list/duplicate-skill", MethodList, fmt.Sprintf(`{"skills":[%s,%s],"resultType":"complete","ttlMs":0,"cacheScope":"public"}`, encoded, encoded)},
		{"list/missing-ttl", MethodList, `{"skills":[],"resultType":"complete","cacheScope":"public"}`},
		{"list/missing-scope", MethodList, `{"skills":[],"resultType":"complete","ttlMs":0}`},
		{"list/negative-ttl", MethodList, `{"skills":[],"resultType":"complete","ttlMs":-1,"cacheScope":"public"}`},
		{"list/bad-scope", MethodList, `{"skills":[],"resultType":"complete","ttlMs":0,"cacheScope":"unknown"}`},
		{"list/wrong-result-type", MethodList, `{"skills":[],"resultType":"input_required","ttlMs":0,"cacheScope":"public"}`},
		{"list/missing-result-type", MethodList, `{"skills":[],"ttlMs":0,"cacheScope":"public"}`},
		{"get/missing-scope", MethodGet, fmt.Sprintf(`{"skill":%s,"resultType":"complete","ttlMs":0}`, encoded)},
		{"get/missing-ttl", MethodGet, fmt.Sprintf(`{"skill":%s,"resultType":"complete","cacheScope":"public"}`, encoded)},
		{"get/null-skill", MethodGet, `{"skill":null,"resultType":"complete","ttlMs":0,"cacheScope":"public"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := testServer()
			server.AddExtension(ExtensionID, nil)
			raw := &rawResult{data: json.RawMessage(test.body)}
			// Register only the method under test, bypassing AddHandlers so that
			// the body reaches the client exactly as written.
			var err error
			switch test.method {
			case MethodGet:
				err = mcp.AddReceivingCustomMethod(server, MethodGet, func(context.Context, *mcp.ServerSession, *GetSkillParams) (*rawResult, error) { return raw, nil })
			default:
				err = mcp.AddReceivingCustomMethod(server, MethodList, func(context.Context, *mcp.ServerSession, *ListSkillsParams) (*rawResult, error) { return raw, nil })
			}
			if err != nil {
				t.Fatal(err)
			}
			client := connectSkills(t, server, protocolVersionCaching)
			if test.method == MethodGet {
				_, err = client.Get(t.Context(), &GetSkillParams{URI: skill.URI})
			} else {
				_, err = client.List(t.Context(), nil)
			}
			if err == nil {
				t.Fatalf("accepted malformed %s response", test.method)
			}
		})
	}
}

// TestIteratorOwnership checks that All captures the session and parameters when
// it is called, and that the returned sequence is reusable.
func TestIteratorOwnership(t *testing.T) {
	server := testServer()
	one := testSkill()
	two := skillWith(func(s *Skill) {
		s.URI, s.Frontmatter, s.Resources = "skill://other/SKILL.md", Frontmatter{"name": "other", "description": "Other"}, DynamicResources()
	})
	handlers := fixedHandlers(one)
	handlers.List = func(_ context.Context, _ *mcp.ServerSession, p *ListSkillsParams) (*ListSkillsResult, error) {
		page, next, err := PaginateSkills([]*Skill{two, one}, p.Cursor, 1)
		return &ListSkillsResult{Skills: page, NextCursor: next}, err
	}
	if err := AddHandlers(server, handlers, nil); err != nil {
		t.Fatal(err)
	}
	client := connectSkills(t, server, protocolVersionCaching)
	params := &ListSkillsParams{ParamsBase: mcp.ParamsBase{Meta: mcp.Meta{"key": "value"}}}
	seq := client.All(t.Context(), params)
	for range 2 {
		count := 0
		for _, err := range seq {
			if err != nil {
				t.Fatal(err)
			}
			count++
		}
		if count != 2 {
			t.Fatalf("iterator yielded %d skills across pages, want 2", count)
		}
	}
	if params.Cursor != "" || !reflect.DeepEqual(params.Meta, mcp.Meta{"key": "value"}) {
		t.Fatal("iterator mutated its parameters")
	}
}
