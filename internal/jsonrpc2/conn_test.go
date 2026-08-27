// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package jsonrpc2

import (
	"errors"
	"io"
	"testing"
)

// TestShuttingDownWrapsWriteError verifies that when a connection shuts down
// because its write side failed, the returned error preserves the underlying
// write error in its chain so callers can classify it with errors.Is (for
// example, distinguishing io.EOF from a clean host disconnect versus a real
// failure).
func TestShuttingDownWrapsWriteError(t *testing.T) {
	s := &inFlightState{writeErr: io.EOF}
	err := s.shuttingDown(ErrServerClosing)
	if !errors.Is(err, ErrServerClosing) {
		t.Errorf("shuttingDown() error = %v, want it to wrap ErrServerClosing", err)
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("shuttingDown() error = %v, want it to wrap io.EOF", err)
	}
}

func TestShuttingDownWrapsReadError(t *testing.T) {
	s := &inFlightState{readErr: io.EOF}
	err := s.shuttingDown(ErrServerClosing)
	if !errors.Is(err, ErrServerClosing) {
		t.Errorf("shuttingDown() error = %v, want it to wrap ErrServerClosing", err)
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("shuttingDown() error = %v, want it to wrap io.EOF", err)
	}
}
