// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

type extensionParams struct {
	ParamsBase
	Text string `json:"text"`
}

type extensionResult struct {
	ResultBase
	Text string `json:"text"`
}

func TestCustomMethod(t *testing.T) {
	for _, version := range SupportedProtocolVersions() {
		t.Run(version, func(t *testing.T) {
			method := NewCustomMethod[*extensionParams, *extensionResult]("test/echo")
			if got := method.Name(); got != "test/echo" {
				t.Fatalf("Name() = %q, want test/echo", got)
			}
			s := NewServer(testImpl, nil)
			if err := method.RegisterServer(s, func(_ context.Context, ss *ServerSession, p *extensionParams) (*extensionResult, error) {
				if ss == nil {
					t.Error("handler received a nil session")
				}
				if version >= protocolVersion20260728 && p.GetMeta()[MetaKeyProtocolVersion] != version {
					t.Errorf("request metadata = %v, want protocol version %s", p.GetMeta(), version)
				}
				if p == nil {
					return &extensionResult{}, nil
				}
				if p.Text == "fail" {
					return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid text"}
				}
				return &extensionResult{Text: p.Text}, nil
			}); err != nil {
				t.Fatal(err)
			}
			c := NewClient(testImpl, nil)
			if err := method.RegisterClient(c); err != nil {
				t.Fatal(err)
			}
			ct, st := NewInMemoryTransports()
			ss, err := s.Connect(t.Context(), st, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ss.Close() })
			cs, err := c.Connect(t.Context(), ct, &ClientSessionOptions{ProtocolVersion: version})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cs.Close() })
			for _, p := range []*extensionParams{{Text: "hello"}, nil} {
				want := ""
				if p != nil {
					want = p.Text
				}
				got, err := method.Call(t.Context(), cs, p)
				if err != nil {
					t.Fatal(err)
				}
				if got.Text != want {
					t.Errorf("Call() text = %q, want %q", got.Text, want)
				}
			}
			_, err = method.Call(t.Context(), cs, &extensionParams{Text: "fail"})
			var rpcErr *jsonrpc.Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeInvalidParams {
				t.Errorf("Call() error = %v, want invalid params", err)
			}
			unregistered := NewCustomMethod[*extensionParams, *extensionResult]("test/unregistered")
			if _, err := unregistered.Call(t.Context(), cs, nil); err == nil || !strings.Contains(err.Error(), "not registered") {
				t.Errorf("unregistered Call() error = %v, want registration error", err)
			}
		})
	}
}

func TestCustomMethodRejectsStandardMethods(t *testing.T) {
	method := NewCustomMethod[*extensionParams, *extensionResult]("tools/call")
	if err := method.RegisterClient(NewClient(testImpl, nil)); err == nil {
		t.Error("RegisterClient accepted a standard method")
	}
	if err := method.RegisterServer(NewServer(testImpl, nil), func(context.Context, *ServerSession, *extensionParams) (*extensionResult, error) {
		return &extensionResult{}, nil
	}); err == nil {
		t.Error("RegisterServer accepted a standard method")
	}
}

// Registry tests must not run in parallel with tests that construct clients or servers.
func isolateExtensions(t *testing.T) {
	t.Helper()
	extensionsMu.Lock()
	saved := extensions
	extensions = nil
	extensionsMu.Unlock()
	t.Cleanup(func() {
		extensionsMu.Lock()
		extensions = saved
		extensionsMu.Unlock()
	})
}

func TestExtensionsOrderAndScope(t *testing.T) {
	isolateExtensions(t)
	method := NewCustomMethod[*extensionParams, *extensionResult]("test/extension")
	var calls []string
	extension := func(name string) Extension {
		return Extension{
			Server: func(s *Server) error {
				calls = append(calls, "server/"+name)
				return method.RegisterServer(s, func(context.Context, *ServerSession, *extensionParams) (*extensionResult, error) {
					return &extensionResult{Text: name}, nil
				})
			},
			Client: func(c *Client) error {
				calls = append(calls, "client/"+name)
				return method.RegisterClient(c)
			},
		}
	}
	beforeServer, beforeClient := NewServer(testImpl, nil), NewClient(testImpl, nil)
	RegisterExtension(extension("global-1"))
	RegisterExtension(extension("global-2"))
	local := []Extension{{}, extension("local-1"), extension("local-2")}
	s := NewServer(testImpl, &ServerOptions{Extensions: local})
	c := NewClient(testImpl, &ClientOptions{Extensions: local})
	want := []string{"server/global-1", "server/global-2", "server/local-1", "server/local-2", "client/global-1", "client/global-2", "client/local-1", "client/local-2"}
	if !slices.Equal(calls, want) {
		t.Errorf("extension order = %v, want %v", calls, want)
	}
	for _, test := range []struct {
		name   string
		server *Server
		client *Client
		want   string
	}{
		{"local", s, c, "local-2"},
		{"global", NewServer(testImpl, nil), NewClient(testImpl, nil), "global-2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cs, _, cleanup := basicClientServerConnection(t, test.client, test.server, nil)
			defer cleanup()
			got, err := method.Call(t.Context(), cs, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.Text != test.want {
				t.Errorf("handler result = %q, want %q", got.Text, test.want)
			}
		})
	}
	if _, ok := beforeServer.receiveMethods[method.Name()]; ok {
		t.Error("registration changed an existing server")
	}
	if _, ok := beforeClient.sendMethods[method.Name()]; ok {
		t.Error("registration changed an existing client")
	}
}

func TestExtensionsErrors(t *testing.T) {
	for _, side := range []string{"server", "client"} {
		for _, global := range []bool{false, true} {
			scope := "local"
			if global {
				scope = "global"
			}
			t.Run(side+"/"+scope, func(t *testing.T) {
				isolateExtensions(t)
				want := errors.New("extension failed")
				ext := Extension{}
				if side == "server" {
					ext.Server = func(*Server) error { return want }
				} else {
					ext.Client = func(*Client) error { return want }
				}
				local := []Extension{ext}
				if global {
					RegisterExtension(ext)
					local = nil
				}
				defer func() {
					err, ok := recover().(error)
					if !ok || !errors.Is(err, want) {
						t.Errorf("constructor panic = %v, want %v", err, want)
					}
				}()
				if side == "server" {
					NewClient(testImpl, &ClientOptions{Extensions: local})
					NewServer(testImpl, &ServerOptions{Extensions: local})
				} else {
					NewServer(testImpl, &ServerOptions{Extensions: local})
					NewClient(testImpl, &ClientOptions{Extensions: local})
				}
			})
		}
	}
}

func TestExtensionsRegistrationDuringApply(t *testing.T) {
	isolateExtensions(t)
	var calls int
	RegisterExtension(Extension{Server: func(*Server) error {
		RegisterExtension(Extension{Server: func(*Server) error {
			calls++
			return nil
		}})
		return nil
	}})
	NewServer(testImpl, nil)
	if calls != 0 {
		t.Errorf("new extension applied to the current constructor: %d calls", calls)
	}
	NewServer(testImpl, nil)
	if calls != 1 {
		t.Errorf("new extension calls = %d, want 1", calls)
	}
}

func TestExtensionsConcurrentRegistration(t *testing.T) {
	isolateExtensions(t)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			RegisterExtension(Extension{})
			NewServer(testImpl, nil)
			NewClient(testImpl, nil)
		})
	}
	wg.Wait()
	if len(extensions) != 10 {
		t.Errorf("registered %d extensions, want 10", len(extensions))
	}
}
