// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// listenRequest returns a raw 2026-07-28 subscriptions/listen POST for uri.
func listenRequest(t *testing.T, ctx context.Context, url, uri string) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  methodSubscriptionsListen,
		"params": map[string]any{
			"_meta": map[string]any{
				MetaKeyProtocolVersion:    protocolVersion20260728,
				MetaKeyClientInfo:         map[string]any{"name": "new-proto-client", "version": "9.9"},
				MetaKeyClientCapabilities: map[string]any{},
			},
			"notifications": map[string]any{"resourceSubscriptions": []string{uri}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(protocolVersionHeader, protocolVersion20260728)
	req.Header.Set(methodHeader, methodSubscriptionsListen)
	return req
}

// TestStreamKeepAlive_ListenStream checks that a quiet subscriptions/listen
// stream carries periodic SSE comments once headers are committed by the
// acknowledgment, and that the stream still tears down normally when the
// client goes away.
func TestStreamKeepAlive_ListenStream(t *testing.T) {
	const interval = 25 * time.Millisecond
	const window = 16 * interval

	subCh := make(chan string, 8)
	unsubCh := make(chan string, 8)
	server := resourceSubServer(t, subCh, unsubCh)
	handler := NewStreamableHTTPHandler(
		func(*http.Request) *Server { return server },
		&StreamableHTTPOptions{Stateless: true, StreamKeepAlive: interval},
	)
	httpServer := httptest.NewServer(mustNotPanic(t, handler))
	defer httpServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := http.DefaultClient.Do(listenRequest(t, ctx, httpServer.URL, "file:///r1"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want %q", got, "no")
	}

	// Read the stream for a while: expect the acknowledgment event, then
	// keep-alive comments and nothing else.
	deadline := time.After(window)
	lines := make(chan string)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	var comments, events int
loop:
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream ended early")
			}
			switch {
			case line == ":":
				comments++
			case strings.HasPrefix(line, "event: "):
				events++
			case line == "" || strings.HasPrefix(line, "data: ") || strings.HasPrefix(line, "id: "):
			default:
				t.Errorf("unexpected line %q", line)
			}
		case <-deadline:
			break loop
		}
	}
	if events != 1 {
		t.Errorf("got %d events, want 1 (the acknowledgment)", events)
	}
	if comments < 3 {
		t.Errorf("got %d keep-alive comments in %v, want at least 3", comments, window)
	}

	// Closing the request still unwinds the listen handler.
	cancel()
	select {
	case <-unsubCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for UnsubscribeHandler after client disconnect")
	}
}

// TestStreamKeepAlive_OnlyListenStreams checks that the keep-alive is
// confined to subscriptions/listen: a slow tools/call produces an SSE stream
// whose only content is the final response, however long it stays silent.
func TestStreamKeepAlive_OnlyListenStreams(t *testing.T) {
	const interval = 10 * time.Millisecond

	server := NewServer(testImpl, nil)
	AddTool(server, &Tool{Name: "slow"},
		func(ctx context.Context, req *CallToolRequest, args struct{}) (*CallToolResult, any, error) {
			time.Sleep(10 * interval)
			return &CallToolResult{Content: []Content{&TextContent{Text: "ok"}}}, nil, nil
		})
	handler := NewStreamableHTTPHandler(
		func(*http.Request) *Server { return server },
		&StreamableHTTPOptions{Stateless: true, StreamKeepAlive: interval},
	)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	req, err := http.NewRequest(http.MethodPost, httpServer.URL, bytes.NewReader(newProtocolBody(t, "slow", struct{}{})))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(protocolVersionHeader, protocolVersion20260728)
	req.Header.Set(methodHeader, "tools/call")
	req.Header.Set(nameHeader, "slow")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, body)
	}
	if hasSSEComment(body) {
		t.Errorf("keep-alive written on a tools/call stream:\n%s", body)
	}
}

// hasSSEComment reports whether body contains an SSE comment line.
func hasSSEComment(body []byte) bool {
	for _, line := range bytes.Split(body, []byte("\n")) {
		if bytes.HasPrefix(line, []byte(":")) {
			return true
		}
	}
	return false
}

// recordingWriter is an http.ResponseWriter that records writes and can be
// made to fail.
type recordingWriter struct {
	header http.Header
	buf    bytes.Buffer
	err    error
}

func (w *recordingWriter) Header() http.Header { return w.header }
func (w *recordingWriter) WriteHeader(int)     {}
func (w *recordingWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	return w.buf.Write(p)
}

// TestStreamKeepAlive_IdleReset checks the timer semantics directly: nothing
// is written before the first event, an event written between ticks defers
// the next comment by a full interval, and a failed write closes the stream.
func TestStreamKeepAlive_IdleReset(t *testing.T) {
	const interval = 60 * time.Millisecond

	w := &recordingWriter{header: http.Header{}}
	done := make(chan struct{})
	s := &stream{
		id:     "s",
		logger: ensureLogger(nil),
		w:      w,
		done:   done,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.mu.Lock()
	s.startKeepAliveLocked(ctx, interval)
	s.mu.Unlock()

	// Headers uncommitted: nothing is written however long we wait.
	time.Sleep(3 * interval)
	s.mu.Lock()
	if got := w.buf.Len(); got != 0 {
		t.Errorf("wrote %d bytes before the first event", got)
	}
	// Simulate a first event and let a comment land.
	s.markWrittenLocked()
	s.mu.Unlock()
	time.Sleep(2 * interval)
	s.mu.Lock()
	if got := w.buf.String(); !strings.Contains(got, ":\n\n") {
		t.Errorf("after the idle interval, wrote %q, want a comment", got)
	}
	// A fresh event resets the idle timer: the next comment must not arrive
	// within the following interval.
	w.buf.Reset()
	s.markWrittenLocked()
	s.mu.Unlock()
	time.Sleep(interval / 2)
	s.mu.Lock()
	if got := w.buf.Len(); got != 0 {
		t.Errorf("comment written %v after an event, before the idle interval elapsed", interval/2)
	}
	// Fail the next write: the stream is closed so the hanging request ends.
	w.err = errors.New("peer gone")
	s.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * interval):
		t.Fatal("stream not closed after a failed keep-alive write")
	}
	s.mu.Lock()
	if s.done != nil {
		t.Error("done not cleared after close")
	}
	s.mu.Unlock()
}

// idleTimeoutProxy forwards requests to upstream and, like nginx's
// proxy_read_timeout, drops a response whose body has been silent for idle.
func idleTimeoutProxy(t *testing.T, upstream string, idle time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		out, err := http.NewRequestWithContext(req.Context(), req.Method, upstream+req.URL.RequestURI(), req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out.Header = req.Header.Clone()
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			w.Header()[k] = vs
		}
		w.WriteHeader(resp.StatusCode)
		rc := http.NewResponseController(w)
		gone := make(chan struct{})
		defer close(gone)
		chunks := make(chan []byte)
		go func() {
			defer close(chunks)
			buf := make([]byte, 4096)
			for {
				n, err := resp.Body.Read(buf)
				if n > 0 {
					select {
					case chunks <- append([]byte(nil), buf[:n]...):
					case <-gone:
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
		for {
			select {
			case chunk, ok := <-chunks:
				if !ok {
					return
				}
				if _, err := w.Write(chunk); err != nil {
					return
				}
				_ = rc.Flush()
			case <-time.After(idle):
				return // idle timeout: closes both the downstream response and, via defer, the upstream body
			}
		}
	}))
}

// TestStreamKeepAlive_SurvivesIdleTimeoutProxy is the scenario from the
// issue: behind an intermediary that drops silent responses, a quiet listen
// stream dies without the keep-alive and outlives the timeout with it, still
// delivering the next real notification. The SDK client is used end to end,
// which also checks that it ignores the comment lines.
func TestStreamKeepAlive_SurvivesIdleTimeoutProxy(t *testing.T) {
	const idle = 600 * time.Millisecond

	for _, tc := range []struct {
		name      string
		keepAlive time.Duration
		survives  bool
	}{
		{"keep-alive disabled", -1, false},
		{"with keep-alive", idle / 6, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subCh := make(chan string, 8)
			unsubCh := make(chan string, 8)
			server := resourceSubServer(t, subCh, unsubCh)
			handler := NewStreamableHTTPHandler(
				func(*http.Request) *Server { return server },
				&StreamableHTTPOptions{Stateless: true, StreamKeepAlive: tc.keepAlive},
			)
			upstream := httptest.NewServer(mustNotPanic(t, handler))
			defer upstream.Close()
			proxy := idleTimeoutProxy(t, upstream.URL, idle)
			defer proxy.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			events := make(chan string, 8)
			client := NewClient(testImpl, &ClientOptions{
				ResourceUpdatedHandler: func(_ context.Context, req *ResourceUpdatedNotificationRequest) {
					events <- req.Params.URI
				},
			})
			cs, err := client.Connect(ctx, &StreamableClientTransport{Endpoint: proxy.URL, MaxRetries: -1},
				&ClientSessionOptions{ProtocolVersion: protocolVersion20260728})
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			if err := cs.Subscribe(ctx, &SubscribeParams{URI: "file:///r1"}); err != nil {
				t.Fatal(err)
			}
			<-subCh

			// Stay quiet for several idle periods.
			time.Sleep(3 * idle)

			server.ResourceUpdated(ctx, &ResourceUpdatedNotificationParams{URI: "file:///r1"})
			select {
			case <-events:
				if !tc.survives {
					t.Fatal("notification delivered although the proxy should have dropped the idle stream")
				}
			case <-time.After(idle):
				if tc.survives {
					t.Fatal("notification not delivered: the keep-alive did not keep the stream open")
				}
			}
			if !tc.survives {
				// The drop reached the server: the listen handler unwound.
				select {
				case <-unsubCh:
				case <-time.After(5 * time.Second):
					t.Fatal("UnsubscribeHandler not called after the proxy dropped the stream")
				}
			}
		})
	}
}

// TestStreamKeepAlive_NoGoroutineLeak checks that keep-alive goroutines end
// with their streams.
func TestStreamKeepAlive_NoGoroutineLeak(t *testing.T) {
	const interval = 10 * time.Millisecond

	subCh := make(chan string, 8)
	unsubCh := make(chan string, 8)
	server := resourceSubServer(t, subCh, unsubCh)
	handler := NewStreamableHTTPHandler(
		func(*http.Request) *Server { return server },
		&StreamableHTTPOptions{Stateless: true, StreamKeepAlive: interval},
	)
	httpServer := httptest.NewServer(mustNotPanic(t, handler))
	defer httpServer.Close()

	for range 5 {
		ctx, cancel := context.WithCancel(context.Background())
		resp, err := http.DefaultClient.Do(listenRequest(t, ctx, httpServer.URL, "file:///r1"))
		if err != nil {
			t.Fatal(err)
		}
		<-subCh
		time.Sleep(3 * interval)
		cancel()
		resp.Body.Close()
		<-unsubCh
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		if !bytes.Contains(buf[:n], []byte("(*stream).keepAlive")) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("keep-alive goroutines still running after their streams ended:\n%s", buf[:n])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStreamKeepAlive_Default checks how the option resolves: zero selects
// [DefaultStreamKeepAlive], a negative value disables the keep-alive.
func TestStreamKeepAlive_Default(t *testing.T) {
	for _, tc := range []struct {
		in, want time.Duration
	}{
		{0, DefaultStreamKeepAlive},
		{-1, -1},
		{time.Second, time.Second},
	} {
		h := NewStreamableHTTPHandler(func(*http.Request) *Server { return nil }, &StreamableHTTPOptions{StreamKeepAlive: tc.in})
		if got := h.opts.StreamKeepAlive; got != tc.want {
			t.Errorf("StreamKeepAlive %v resolved to %v, want %v", tc.in, got, tc.want)
		}
	}
	h := NewStreamableHTTPHandler(func(*http.Request) *Server { return nil }, nil)
	if got := h.opts.StreamKeepAlive; got != DefaultStreamKeepAlive {
		t.Errorf("nil options: StreamKeepAlive = %v, want %v", got, DefaultStreamKeepAlive)
	}
}
