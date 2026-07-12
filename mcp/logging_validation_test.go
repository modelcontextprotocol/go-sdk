// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestSetLevelValidLoggingLevels(t *testing.T) {
	ss := &ServerSession{server: NewServer(testImpl, nil)}
	for _, level := range []LoggingLevel{
		"debug",
		"info",
		"notice",
		"warning",
		"error",
		"critical",
		"alert",
		"emergency",
	} {
		if _, err := ss.setLevel(context.Background(), &SetLoggingLevelParams{Level: level}); err != nil {
			t.Errorf("setLevel(%q) returned error: %v", level, err)
		}
		if got := ss.state.LogLevel; got != level {
			t.Errorf("LogLevel after setLevel(%q) = %q, want %q", level, got, level)
		}
	}
}

func TestSetLevelInvalidLoggingLevel(t *testing.T) {
	ss := &ServerSession{server: NewServer(testImpl, nil)}
	ss.state.LogLevel = "info"

	_, err := ss.setLevel(context.Background(), &SetLoggingLevelParams{Level: "verbose"})
	if err == nil {
		t.Fatal("setLevel(\"verbose\") returned nil error")
	}
	if !strings.Contains(err.Error(), `invalid logging level "verbose"`) {
		t.Errorf("error = %q, want invalid level message", err)
	}
	if got := ss.state.LogLevel; got != "info" {
		t.Errorf("LogLevel after invalid setLevel = %q, want %q", got, "info")
	}
}

func TestSetLevelEmptyLoggingLevel(t *testing.T) {
	ss := &ServerSession{server: NewServer(testImpl, nil)}

	_, err := ss.setLevel(context.Background(), &SetLoggingLevelParams{Level: ""})
	if err == nil {
		t.Fatal("setLevel(\"\") returned nil error")
	}
	if !strings.Contains(err.Error(), `invalid logging level ""`) {
		t.Errorf("error = %q, want invalid level message", err)
	}
	if got := ss.state.LogLevel; got != "" {
		t.Errorf("LogLevel after empty setLevel = %q, want empty", got)
	}
}
