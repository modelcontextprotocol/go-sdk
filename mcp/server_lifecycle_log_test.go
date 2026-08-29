// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// syncBuffer is a bytes.Buffer safe for concurrent use, since session
// lifecycle events are logged from the connection's read goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestSessionLifecycleLogLevel verifies that routine session bookkeeping
// (connect, disconnect, initialize, setLevel) logs at info by default, and
// moves to debug when MCPGODEBUG=demotesessionlifecyclelog=1. On stateless
// streamable HTTP these events fire on every request, so at info they drown
// out the server's own logs (#1204); the demotion is gated behind
// MCPGODEBUG because it changes log output existing users may rely on.
func TestSessionLifecycleLogLevel(t *testing.T) {
	// "session initialized" is also gated but doesn't fire on this
	// connection path, so it is not asserted here.
	lifecycleMessages := []string{
		"server connecting",
		"server session connected",
		"client log level set",
		"server session disconnected",
	}

	run := func(t *testing.T, level slog.Level) string {
		var buf syncBuffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level}))
		cs, _, cleanup := basicConnection(t, func(s *Server) {
			s.opts.Logger = logger
		})
		if err := cs.SetLoggingLevel(context.Background(), &SetLoggingLevelParams{Level: "warning"}); err != nil {
			t.Fatal(err)
		}
		cleanup()
		return buf.String()
	}

	t.Run("info by default", func(t *testing.T) {
		got := run(t, slog.LevelInfo)
		for _, msg := range lifecycleMessages {
			if !strings.Contains(got, msg) {
				t.Errorf("log output missing %q at info level by default:\n%s", msg, got)
			}
		}
	})

	t.Run("demoted to debug with MCPGODEBUG=demotesessionlifecyclelog=1", func(t *testing.T) {
		old := demotesessionlifecyclelog
		demotesessionlifecyclelog = "1"
		t.Cleanup(func() { demotesessionlifecyclelog = old })

		if got := run(t, slog.LevelInfo); got != "" {
			t.Errorf("session lifecycle produced log output at info level with demotesessionlifecyclelog=1:\n%s", got)
		}
		got := run(t, slog.LevelDebug)
		for _, msg := range lifecycleMessages {
			if !strings.Contains(got, msg) {
				t.Errorf("log output missing %q at debug level with demotesessionlifecyclelog=1:\n%s", msg, got)
			}
		}
	})
}
