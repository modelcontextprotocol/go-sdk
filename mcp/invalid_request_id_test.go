// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

type invalidIDWriter struct{ bytes.Buffer }

func (*invalidIDWriter) Close() error { return nil }

func TestInvalidRequestIDHTTP(t *testing.T) {
	for _, id := range []string{"true", "{}", "[]"} {
		// Arrange.
		calls := 0
		server := NewServer(&Implementation{Name: "synthetic", Version: "test"}, nil)
		server.AddTool(&Tool{Name: "probe", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *CallToolRequest) (*CallToolResult, error) {
			calls++
			return &CallToolResult{}, nil
		})
		handler := NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, &StreamableHTTPOptions{Stateless: true, JSONResponse: true})
		req := httptest.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":`+id+`,"method":"tools/call","params":{"name":"probe","arguments":{}}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		result := httptest.NewRecorder()
		// Act.
		handler.ServeHTTP(result, req)
		// Assert.
		var envelope struct {
			ID    json.RawMessage
			Error struct{ Code int }
		}
		if err := json.Unmarshal(result.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if result.Header().Get("Content-Type") != "application/json" || result.Code != 400 || string(envelope.ID) != "null" || envelope.Error.Code != -32600 || calls != 0 {
			t.Fatalf("status%d calls%d body%s", result.Code, calls, result.Body)
		}
	}
}

func TestInvalidRequestIDIORecovery(t *testing.T) {
	// Arrange: many invalid frames followed by a valid request.
	data := strings.Repeat(`{"jsonrpc":"2.0","id":true,"method":"ping"}`+"\n", 1000) + `{"jsonrpc":"2.0","id":7,"method":"ping"}` + "\n"
	writer := &invalidIDWriter{}
	conn := newIOConn(rwc{rc: io.NopCloser(strings.NewReader(data)), wc: writer})
	t.Cleanup(func() { conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Act.
	message, err := conn.Read(ctx)
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if message.(*jsonrpc.Request).ID.Raw() != int64(7) {
		t.Fatal("valid ID lost")
	}
	decoder := json.NewDecoder(bytes.NewReader(writer.Bytes()))
	for range 1000 {
		var envelope struct {
			ID    json.RawMessage
			Error struct{ Code int }
		}
		if err := decoder.Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		if string(envelope.ID) != "null" || envelope.Error.Code != -32600 {
			t.Fatal("invalid frame error")
		}
	}
}
