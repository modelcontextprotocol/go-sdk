// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// TestPOSTStreamFlushesHeadersEarly checks that the SSE response to a POST
// produces its first bytes while the tool is still running, rather than when
// the tool call completes.
//
// Without this, a server whose tool runs for minutes writes nothing at all in
// the meantime, and clients (and intermediaries) that apply a first-byte
// timeout treat the silence as a dead connection and hang up.
func TestPOSTStreamFlushesHeadersEarly(t *testing.T) {
	release := make(chan struct{})
	server := NewServer(testImpl, nil)
	AddTool(server, &Tool{Name: "slow"}, func(ctx context.Context, req *CallToolRequest, args map[string]any) (*CallToolResult, any, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		return &CallToolResult{Content: []Content{&TextContent{Text: "done"}}}, nil, nil
	})

	httpServer := httptest.NewServer(NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, nil))
	defer httpServer.Close()

	post := func(t *testing.T, sessionID, body string) *http.Response {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		req, err := http.NewRequestWithContext(ctx, "POST", httpServer.URL, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req.Header.Set(sessionIDHeader, sessionID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("no response headers within the deadline: %v\n"+
				"(the stream writes nothing until the tool returns, so a client's "+
				"first-byte timer fires on a call that is still running)", err)
		}
		return resp
	}

	initResp := post(t, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	sessionID := initResp.Header.Get(sessionIDHeader)
	io.Copy(io.Discard, initResp.Body)
	initResp.Body.Close()
	if sessionID == "" {
		t.Fatal("no session ID from initialize")
	}
	notifResp := post(t, sessionID, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	io.Copy(io.Discard, notifResp.Body)
	notifResp.Body.Close()

	callResp := post(t, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"slow","arguments":{}}}`)
	defer callResp.Body.Close()

	if got := baseMediaType(callResp.Header.Get("Content-Type")); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if callResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", callResp.StatusCode)
	}

	firstByte := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := callResp.Body.Read(buf)
		if err != nil && n == 0 {
			firstByte <- nil
			return
		}
		firstByte <- bytes.Clone(buf[:n])
	}()

	select {
	case got := <-firstByte:
		if len(got) == 0 {
			t.Fatal("stream closed without writing anything")
		}
		if !bytes.HasPrefix(got, []byte(":")) {
			t.Errorf("first bytes = %q, want an SSE comment", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no bytes written while the tool was still running: the client's first-byte timer would fire here")
	}

	close(release)
	rest, err := io.ReadAll(callResp.Body)
	if err != nil {
		t.Fatalf("reading result: %v", err)
	}
	if !bytes.Contains(rest, []byte("done")) {
		t.Errorf("after releasing the tool, body = %q, want it to contain the result", rest)
	}
}

// TestPOSTUnknownMethodKeeps404 checks that a protocol-level MethodNotFound
// produced before the stream exists still returns HTTP 404, not a flushed 200
// SSE stream.
func TestPOSTUnknownMethodKeeps404(t *testing.T) {
	server := NewServer(testImpl, nil)
	httpServer := httptest.NewServer(NewStreamableHTTPHandler(
		func(*http.Request) *Server { return server },
		&StreamableHTTPOptions{Stateless: true},
	))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"notamethod","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(protocolVersionHeader, protocolVersion20260728)

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if elapsed := time.Since(start); elapsed >= earlyFlushDelay {
		t.Errorf("unknown method took %v, want well under earlyFlushDelay (%v)", elapsed, earlyFlushDelay)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", resp.StatusCode, body)
	}
	if got := baseMediaType(resp.Header.Get("Content-Type")); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

// TestPOSTProtocolErrorKeepsOverrideStatus checks that a SEP-2575 protocol
// error produced after the stream exists, but before the delayed flush, still
// sets the spec-mandated HTTP status.
func TestPOSTProtocolErrorKeepsOverrideStatus(t *testing.T) {
	server := NewServer(testImpl, nil)
	httpServer := httptest.NewServer(NewStreamableHTTPHandler(
		func(*http.Request) *Server { return server },
		&StreamableHTTPOptions{Stateless: true},
	))
	defer httpServer.Close()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "prompts/get",
		"params": map[string]any{
			"_meta": map[string]any{
				MetaKeyProtocolVersion:    protocolVersion20260728,
				MetaKeyClientInfo:         map[string]any{"name": "c", "version": "1"},
				MetaKeyClientCapabilities: map[string]any{"sampling": map[string]any{}},
			},
			"name": "no-such-prompt",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(protocolVersionHeader, protocolVersion20260728)
	req.Header.Set(methodHeader, "prompts/get")
	req.Header.Set(nameHeader, "no-such-prompt")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	gotBody, _ := io.ReadAll(resp.Body)

	if elapsed := time.Since(start); elapsed >= earlyFlushDelay {
		t.Errorf("protocol error took %v, want well under earlyFlushDelay (%v)", elapsed, earlyFlushDelay)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", resp.StatusCode, gotBody)
	}
	if got := baseMediaType(resp.Header.Get("Content-Type")); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	msg, err := jsonrpc2.DecodeMessage(gotBody)
	if err != nil {
		t.Fatalf("DecodeMessage: %v; body = %s", err, gotBody)
	}
	jresp, ok := msg.(*jsonrpc.Response)
	if !ok || jresp.Error == nil {
		t.Fatalf("response is not a JSON-RPC error: %s", gotBody)
	}
	var jerr *jsonrpc.Error
	if !errors.As(jresp.Error, &jerr) {
		t.Fatalf("error is not *jsonrpc.Error: %v", jresp.Error)
	}
	if jerr.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("error code = %d, want %d", jerr.Code, jsonrpc.CodeInvalidParams)
	}
}

func TestDeliverLockedOverrideStatusWinsBeforeFlush(t *testing.T) {
	rec, w := newHeaderCounter()
	id := jsonrpc2.Int64ID(1)
	s := connectedTestStream(w, id)

	s.mu.Lock()
	done, err := s.deliverLocked(protocolErrorJSON, "", id, http.StatusNotFound)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatal("stream not done after the only request was answered")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if got := rec.codes; len(got) != 1 || got[0] != http.StatusNotFound {
		t.Errorf("WriteHeader calls = %v, want [404]", got)
	}
	if got := baseMediaType(rec.Header().Get("Content-Type")); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if !s.headersFlushed {
		t.Error("headersFlushed = false, want true")
	}
}

func TestFlushEarlyAfterSkippedOnceHeadersCommitted(t *testing.T) {
	rec, w := newHeaderCounter()
	id := jsonrpc2.Int64ID(1)
	s := connectedTestStream(w, id)

	s.mu.Lock()
	if _, err := s.deliverLocked(protocolErrorJSON, "", id, http.StatusNotFound); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()

	s.flushEarlyAfter(context.Background(), 0)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if got := rec.codes; len(got) != 1 || got[0] != http.StatusNotFound {
		t.Errorf("WriteHeader calls = %v, want [404] (early flush must not write again)", got)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(": ok")) {
		t.Errorf("body = %q, must not contain an SSE keep-alive after a protocol error", rec.Body.Bytes())
	}
}

func TestDeliverLockedAfterEarlyFlushWritesSSENotStatus(t *testing.T) {
	rec, w := newHeaderCounter()
	id := jsonrpc2.Int64ID(1)
	s := connectedTestStream(w, id)

	s.flushEarlyAfter(context.Background(), 0)

	s.mu.Lock()
	if _, err := s.deliverLocked(protocolErrorJSON, "", id, http.StatusNotFound); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (headers already committed)", rec.Code)
	}
	if got := rec.codes; len(got) != 1 || got[0] != http.StatusOK {
		t.Errorf("WriteHeader calls = %v, want [200] (no superfluous override status)", got)
	}
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, []byte(":")) {
		t.Errorf("body = %q, want an SSE comment first", body)
	}
	if !bytes.Contains(body, []byte("event: message")) {
		t.Errorf("body = %q, want the error delivered as an SSE event", body)
	}
}

func TestFlushEarlyAfterCancelled(t *testing.T) {
	rec, w := newHeaderCounter()
	id := jsonrpc2.Int64ID(1)
	s := connectedTestStream(w, id)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.flushEarlyAfter(ctx, time.Hour)

	if len(rec.codes) != 0 {
		t.Errorf("WriteHeader calls = %v, want none after cancel", rec.codes)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty after cancel", rec.Body.Bytes())
	}
	if s.headersFlushed {
		t.Error("headersFlushed = true, want false")
	}
}

func TestReleaseResetsHeadersFlushed(t *testing.T) {
	rec1, w1 := newHeaderCounter()
	id := jsonrpc2.Int64ID(1)
	s := connectedTestStream(w1, id)
	s.flushEarlyAfter(context.Background(), 0)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", rec1.Code)
	}

	s.release()

	rec2, w2 := newHeaderCounter()
	s.w = w2
	s.done = make(chan struct{})
	s.requests = map[jsonrpc.ID]struct{}{id: {}}
	s.mu.Lock()
	if _, err := s.deliverLocked(protocolErrorJSON, "", id, http.StatusNotFound); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()
	if rec2.Code != http.StatusNotFound {
		t.Errorf("reused stream status = %d, want 404 (headersFlushed must reset on release)", rec2.Code)
	}
	if got := rec2.codes; len(got) != 1 || got[0] != http.StatusNotFound {
		t.Errorf("WriteHeader calls = %v, want [404]", got)
	}
}

func TestJSONNotificationDoesNotBlockOverrideStatus(t *testing.T) {
	rec, w := newHeaderCounter()
	id := jsonrpc2.Int64ID(1)
	s := connectedTestStream(w, id)
	s.pendingJSONMessages = []json.RawMessage{}

	s.mu.Lock()
	if _, err := s.deliverLocked([]byte(`{"jsonrpc":"2.0","method":"notifications/message"}`), "", jsonrpc.ID{}, 0); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	if s.headersFlushed {
		s.mu.Unlock()
		t.Fatal("buffering a JSON notification committed headers")
	}
	if _, err := s.deliverLocked(protocolErrorJSON, "", id, http.StatusNotFound); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestCloseMarksHeadersFlushed(t *testing.T) {
	rec, w := newHeaderCounter()
	id := jsonrpc2.Int64ID(1)
	s := connectedTestStream(w, id)
	s.protocolVersion = protocolVersion20251125
	s.close(time.Second)

	s.flushEarlyAfter(context.Background(), 0)

	if bytes.Contains(rec.Body.Bytes(), []byte(": ok")) {
		t.Errorf("body = %q, close must not be followed by a keep-alive comment", rec.Body.Bytes())
	}
}

func TestFlushEarlyAfterSerializesWithDeliverLocked(t *testing.T) {
	id := jsonrpc2.Int64ID(1)
	for i := 0; i < 50; i++ {
		rec, w := newHeaderCounter()
		s := connectedTestStream(w, id)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.flushEarlyAfter(context.Background(), 0)
		}()
		go func() {
			defer wg.Done()
			s.mu.Lock()
			_, _ = s.deliverLocked(protocolErrorJSON, "", id, http.StatusNotFound)
			s.mu.Unlock()
		}()
		wg.Wait()

		if len(rec.codes) != 1 {
			t.Fatalf("iter %d: WriteHeader calls = %v, want exactly one", i, rec.codes)
		}
		switch rec.codes[0] {
		case http.StatusNotFound:
			if bytes.Contains(rec.Body.Bytes(), []byte(": ok")) {
				t.Fatalf("iter %d: 404 body also contains an SSE comment: %q", i, rec.Body.Bytes())
			}
		case http.StatusOK:
			if !bytes.Contains(rec.Body.Bytes(), []byte("event: message")) {
				t.Fatalf("iter %d: 200 body missing SSE error event: %q", i, rec.Body.Bytes())
			}
		default:
			t.Fatalf("iter %d: status %d, want 200 or 404", i, rec.codes[0])
		}
	}
}

var protocolErrorJSON = []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"nope"}}`)

func connectedTestStream(w http.ResponseWriter, id jsonrpc.ID) *stream {
	return &stream{
		id:       "s",
		w:        w,
		done:     make(chan struct{}),
		requests: map[jsonrpc.ID]struct{}{id: {}},
		lastIdx:  -1,
		logger:   ensureLogger(nil),
	}
}

type headerCounter struct {
	*httptest.ResponseRecorder
	codes []int
}

func newHeaderCounter() (*headerCounter, http.ResponseWriter) {
	h := &headerCounter{ResponseRecorder: httptest.NewRecorder()}
	return h, h
}

func (w *headerCounter) WriteHeader(code int) {
	w.codes = append(w.codes, code)
	w.ResponseRecorder.WriteHeader(code)
}
