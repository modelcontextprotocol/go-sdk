// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestRejectedRequestID_StreamableHTTP(t *testing.T) {
	for _, invalidID := range []string{"true", "false", "{}", "[]"} {
		t.Run("id_"+invalidID, func(t *testing.T) {
			server := NewServer(&Implementation{Name: "test-server", Version: "1.0.0"}, nil)
			handler := NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, nil)

			body := strings.NewReader(`{"jsonrpc":"2.0","id":` + invalidID + `,"method":"ping"}`)
			req := httptest.NewRequest(http.MethodPost, "/", body)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d (body: %s)", http.StatusBadRequest, rec.Code, rec.Body.String())
			}
			contentType := rec.Header().Get("Content-Type")
			if !strings.HasPrefix(contentType, "application/json") {
				t.Fatalf("expected application/json Content-Type, got %q (body: %s)", contentType, rec.Body.String())
			}

			var resp struct {
				JSONRPC string `json:"jsonrpc"`
				ID      any    `json:"id"`
				Error   *struct {
					Code    int64  `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to decode JSON response: %v; body = %s", err, rec.Body.String())
			}

			if resp.JSONRPC != "2.0" {
				t.Errorf("expected jsonrpc 2.0, got %q", resp.JSONRPC)
			}
			if resp.ID != nil {
				t.Errorf("expected id null, got %v", resp.ID)
			}
			if resp.Error == nil || resp.Error.Code != jsonrpc.CodeInvalidRequest {
				t.Errorf("expected error code %d, got %v", jsonrpc.CodeInvalidRequest, resp.Error)
			}
		})
	}
}

func TestRejectedRequestID_IOTransport(t *testing.T) {
	rClient, wServer := io.Pipe()
	rServer, wClient := io.Pipe()

	serverConn := newIOConn(rwc{rc: rClient, wc: wClient})
	defer serverConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const invalidCount = 50
	go func() {
		defer wServer.Close()
		for i := 0; i < invalidCount; i++ {
			var payload string
			switch i % 3 {
			case 0:
				payload = "{\"jsonrpc\":\"2.0\",\"id\":true,\"method\":\"ping\"}\n"
			case 1:
				payload = "{\"jsonrpc\":\"2.0\",\"id\":{},\"method\":\"ping\"}\n"
			case 2:
				payload = "{\"jsonrpc\":\"2.0\",\"id\":[],\"method\":\"ping\"}\n"
			}
			_, _ = wServer.Write([]byte(payload))
		}
		_, _ = wServer.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"ping\"}\n"))
	}()

	decoder := json.NewDecoder(rServer)

	readMsgCh := make(chan jsonrpc.Message, 1)
	readErrCh := make(chan error, 1)
	go func() {
		msg, err := serverConn.Read(ctx)
		if err != nil {
			readErrCh <- err
			return
		}
		readMsgCh <- msg
	}()

	for i := 0; i < invalidCount; i++ {
		var errResp struct {
			JSONRPC string `json:"jsonrpc"`
			ID      any    `json:"id"`
			Error   *struct {
				Code    int64  `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := decoder.Decode(&errResp); err != nil {
			t.Fatalf("[%d] failed to decode error response: %v", i, err)
		}
		if errResp.ID != nil {
			t.Errorf("[%d] expected id null, got %v", i, errResp.ID)
		}
		if errResp.Error == nil || errResp.Error.Code != jsonrpc.CodeInvalidRequest {
			t.Errorf("[%d] expected error code %d, got %v", i, jsonrpc.CodeInvalidRequest, errResp.Error)
		}
	}

	// Verify that serverConn.Read continued and returned the subsequent valid request
	select {
	case err := <-readErrCh:
		t.Fatalf("serverConn.Read failed: %v", err)
	case msg := <-readMsgCh:
		req, ok := msg.(*jsonrpc.Request)
		if !ok {
			t.Fatalf("expected *jsonrpc.Request, got %T", msg)
		}
		if req.Method != "ping" {
			t.Errorf("expected method ping, got %q", req.Method)
		}
		if req.ID != jsonrpc2.Int64ID(7) {
			t.Errorf("expected id 7, got %v", req.ID)
		}
	}
}
