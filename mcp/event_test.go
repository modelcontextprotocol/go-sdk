// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestScanEvents(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []Event
		wantErr string
	}{
		{
			name:  "simple event",
			input: "event: message\nid: 1\ndata: hello\n\n",
			want: []Event{
				{Name: "message", ID: "1", Data: []byte("hello")},
			},
		},
		{
			name:  "multiple data lines",
			input: "data: line 1\ndata: line 2\n\n",
			want: []Event{
				{Data: []byte("line 1\nline 2")},
			},
		},
		{
			name:  "multiple events",
			input: "data: first\n\nevent: second\ndata: second\n\n",
			want: []Event{
				{Data: []byte("first")},
				{Name: "second", Data: []byte("second")},
			},
		},
		{
			name:  "no trailing newline",
			input: "data: hello",
			want: []Event{
				{Data: []byte("hello")},
			},
		},
		{
			name:    "malformed line",
			input:   "invalid line\n\n",
			wantErr: "malformed line",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.input)
			var got []Event
			var err error
			for e, err2 := range scanEvents(r) {
				if err2 != nil {
					err = err2
					break
				}
				got = append(got, e)
			}

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("scanEvents() got nil error, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("scanEvents() error = %q, want containing %q", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("scanEvents() returned unexpected error: %v", err)
			}

			if len(got) != len(tt.want) {
				t.Fatalf("scanEvents() got %d events, want %d", len(got), len(tt.want))
			}

			for i := range got {
				if g, w := got[i].Name, tt.want[i].Name; g != w {
					t.Errorf("event %d: name = %q, want %q", i, g, w)
				}
				if g, w := got[i].ID, tt.want[i].ID; g != w {
					t.Errorf("event %d: id = %q, want %q", i, g, w)
				}
				if g, w := string(got[i].Data), string(tt.want[i].Data); g != w {
					t.Errorf("event %d: data = %q, want %q", i, g, w)
				}
			}
		})
	}
}

func TestMemoryEventStoreState(t *testing.T) {
	ctx := context.Background()

	appendEvent := func(s *MemoryEventStore, sess string, str StreamID, data string) {
		if err := s.Append(ctx, sess, str, []byte(data)); err != nil {
			t.Fatal(err)
		}
	}

	for _, tt := range []struct {
		name     string
		actions  func(*MemoryEventStore)
		want     string // output of debugString
		wantSize int    // value of nBytes
	}{
		{
			"appends",
			func(s *MemoryEventStore) {
				appendEvent(s, "S1", 1, "d1")
				appendEvent(s, "S1", 2, "d2")
				appendEvent(s, "S1", 1, "d3")
				appendEvent(s, "S2", 8, "d4")
			},
			"S1 1 first=0 d1 d3; S1 2 first=0 d2; S2 8 first=0 d4",
			8,
		},
		{
			"session close",
			func(s *MemoryEventStore) {
				appendEvent(s, "S1", 1, "d1")
				appendEvent(s, "S1", 2, "d2")
				appendEvent(s, "S1", 1, "d3")
				appendEvent(s, "S2", 8, "d4")
				s.SessionClosed(ctx, "S1")
			},
			"S2 8 first=0 d4",
			2,
		},
		{
			"purge",
			func(s *MemoryEventStore) {
				appendEvent(s, "S1", 1, "d1")
				appendEvent(s, "S1", 2, "d2")
				appendEvent(s, "S1", 1, "d3")
				appendEvent(s, "S2", 8, "d4")
				// We are using 8 bytes (d1,d2, d3, d4).
				// To purge 6, we remove the first of each stream, leaving only d3.
				s.SetMaxBytes(2)
			},
			// The other streams remain, because we may add to them.
			"S1 1 first=1 d3; S1 2 first=1; S2 8 first=1",
			2,
		},
		{
			"purge append",
			func(s *MemoryEventStore) {
				appendEvent(s, "S1", 1, "d1")
				appendEvent(s, "S1", 2, "d2")
				appendEvent(s, "S1", 1, "d3")
				appendEvent(s, "S2", 8, "d4")
				s.SetMaxBytes(2)
				// Up to here, identical to the "purge" case.
				// Each of these additions will result in a purge.
				appendEvent(s, "S1", 2, "d5") // remove d3
				appendEvent(s, "S1", 2, "d6") // remove d5
			},
			"S1 1 first=2; S1 2 first=2 d6; S2 8 first=1",
			2,
		},
		{
			"purge resize append",
			func(s *MemoryEventStore) {
				appendEvent(s, "S1", 1, "d1")
				appendEvent(s, "S1", 2, "d2")
				appendEvent(s, "S1", 1, "d3")
				appendEvent(s, "S2", 8, "d4")
				s.SetMaxBytes(2)
				// Up to here, identical to the "purge" case.
				s.SetMaxBytes(6) // make room
				appendEvent(s, "S1", 2, "d5")
				appendEvent(s, "S1", 2, "d6")
			},
			// The other streams remain, because we may add to them.
			"S1 1 first=1 d3; S1 2 first=1 d5 d6; S2 8 first=1",
			6,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := NewMemoryEventStore(nil)
			tt.actions(s)
			got := s.debugString()
			if got != tt.want {
				t.Errorf("\ngot  %s\nwant %s", got, tt.want)
			}
			if g, w := s.nBytes, tt.wantSize; g != w {
				t.Errorf("got size %d, want %d", g, w)
			}
		})
	}
}

func TestMemoryEventStoreAfter(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryEventStore(nil)
	s.SetMaxBytes(4)
	s.Append(ctx, "S1", 1, []byte("d1"))
	s.Append(ctx, "S1", 1, []byte("d2"))
	s.Append(ctx, "S1", 1, []byte("d3"))
	s.Append(ctx, "S1", 2, []byte("d4")) // will purge d1
	want := "S1 1 first=1 d2 d3; S1 2 first=0 d4"
	if got := s.debugString(); got != want {
		t.Fatalf("got state %q, want %q", got, want)
	}

	for _, tt := range []struct {
		sessionID string
		streamID  StreamID
		index     int
		want      []string
		wantErr   string // if non-empty, error should contain this string
	}{
		{"S1", 1, 0, []string{"d2", "d3"}, ""},
		{"S1", 1, 1, []string{"d3"}, ""},
		{"S1", 1, 2, nil, ""},
		{"S1", 2, 0, nil, ""},
		{"S1", 3, 0, nil, "unknown stream ID"},
		{"S2", 0, 0, nil, "unknown session ID"},
	} {
		t.Run(fmt.Sprintf("%s-%d-%d", tt.sessionID, tt.streamID, tt.index), func(t *testing.T) {
			var got []string
			for d, err := range s.After(ctx, tt.sessionID, tt.streamID, tt.index) {
				if err != nil {
					if tt.wantErr == "" {
						t.Fatalf("unexpected error %q", err)
					} else if g := err.Error(); !strings.Contains(g, tt.wantErr) {
						t.Fatalf("got error %q, want it to contain %q", g, tt.wantErr)
					} else {
						return
					}
				}
				got = append(got, string(d))
			}
			if tt.wantErr != "" {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
