// Copyright 2020 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type errWriter struct {
	err error
}

func (w errWriter) Write(context.Context, Message) error {
	return w.err
}

func TestProcessResultWriteFailureReportsInternalError(t *testing.T) {
	writeErr := errors.New("write failed")
	var internalErrors []error
	c := &Connection{
		done:   make(chan struct{}),
		writer: errWriter{err: writeErr},
		onInternalError: func(err error) {
			internalErrors = append(internalErrors, err)
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	call, err := NewCall(StringID("1"), "test", nil)
	if err != nil {
		t.Fatal(err)
	}

	req := &incomingRequest{
		Request: call,
		ctx:     ctx,
		cancel:  cancel,
	}

	c.updateInFlight(func(s *inFlightState) {
		s.incoming = 1
		s.incomingByID = map[ID]*incomingRequest{call.ID: req}
	})

	if err := c.processResult("test", req, "ok", nil); err != nil {
		t.Fatalf("processResult() = %v, want nil", err)
	}

	if len(internalErrors) != 1 {
		t.Fatalf("OnInternalError calls = %d, want 1", len(internalErrors))
	}
	got := internalErrors[0].Error()
	if !strings.Contains(got, "failed to write response") {
		t.Errorf("OnInternalError = %q, want message about write failure", got)
	}
	if !errors.Is(internalErrors[0], writeErr) {
		t.Errorf("OnInternalError error does not wrap write error: %v", internalErrors[0])
	}
}
