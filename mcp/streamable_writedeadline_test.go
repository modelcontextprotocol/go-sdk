// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestStreamWriteDeadline_SurvivesServerWriteTimeout checks that a listen
// stream outlives an http.Server.WriteTimeout shorter than the stream: a write
// must land past the first deadline, however many do.
func TestStreamWriteDeadline_SurvivesServerWriteTimeout(t *testing.T) {
	const interval = 25 * time.Millisecond
	// Longer than one interval, so early keep-alives land inside the original
	// deadline and only later ones need it moved.
	const writeTimeout = 3 * interval
	const window = 5 * writeTimeout

	// Buffered: the handlers send unconditionally, so a nil or full channel
	// would block the subscribe this stream waits on.
	server := resourceSubServer(t, make(chan string, 8), make(chan string, 8))
	handler := NewStreamableHTTPHandler(
		func(*http.Request) *Server { return server },
		&StreamableHTTPOptions{Stateless: true, StreamKeepAlive: interval},
	)
	httpServer := httptest.NewUnstartedServer(mustNotPanic(t, handler))
	httpServer.Config.WriteTimeout = writeTimeout
	httpServer.Start()
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

	type line struct {
		text string
		at   time.Duration
	}
	start := time.Now()
	lines := make(chan line)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- line{text: sc.Text(), at: time.Since(start)}:
			case <-ctx.Done():
				return
			}
		}
	}()

	var last time.Duration
	var afterDeadline int
	deadline := time.After(window)
readLoop:
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("the stream ended inside the %v window, with the last keep-alive at %v: a write past the %v deadline failed",
					window, last, writeTimeout)
			}
			if !strings.HasPrefix(l.text, ":") {
				continue
			}
			last = l.at
			if l.at > writeTimeout {
				afterDeadline++
			}
		case <-deadline:
			break readLoop
		}
	}

	if afterDeadline == 0 {
		t.Errorf("no write landed after the %v deadline in a %v window; the stream stopped being written to at %v",
			writeTimeout, window, last)
	}
}
