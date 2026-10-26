// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestCallErrorsDoNotWrapConnectionClosed(t *testing.T) {
	tests := []struct {
		name     string
		rpcError error
		result   Result
		cancel   bool
		want     error
	}{
		{name: "context cancellation", cancel: true, want: context.Canceled},
		{name: "JSON-RPC error", rpcError: jsonrpc2.ErrInvalidParams, want: jsonrpc2.ErrInvalidParams},
		{name: "decoding EOF", result: &unmarshalErrorResult{err: io.EOF}, want: io.EOF},
		{name: "decoding unexpected EOF", result: &unmarshalErrorResult{err: io.ErrUnexpectedEOF}, want: io.ErrUnexpectedEOF},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn, peer := newTestJSONRPCConnection(t, nil)
			done := make(chan error, 1)
			go func() {
				done <- call(ctx, conn, "test", nil, test.result)
			}()
			msg, err := peer.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if test.cancel {
				cancel()
			} else {
				resp := &jsonrpc.Response{
					ID:     msg.(*jsonrpc.Request).ID,
					Result: []byte("{}"),
					Error:  test.rpcError,
				}
				if err := peer.Write(ctx, resp); err != nil {
					t.Fatal(err)
				}
			}
			err = <-done
			if !errors.Is(err, test.want) {
				t.Errorf("call() = %v, want an error wrapping %v", err, test.want)
			}
			if errors.Is(err, ErrConnectionClosed) {
				t.Errorf("call() = %v, must not wrap ErrConnectionClosed", err)
			}
		})
	}
}

func TestCallConnectionClosed(t *testing.T) {
	for _, test := range connectionClosedTests() {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			conn, peer := newTestJSONRPCConnection(t, test.readErr)
			if test.close {
				if err := conn.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				done := make(chan error, 1)
				go func() { done <- call(ctx, conn, "test", nil, nil) }()
				if _, err := peer.Read(ctx); err != nil {
					t.Fatal(err)
				}
				if err := peer.Close(); err != nil {
					t.Fatal(err)
				}
				checkConnectionClosedError(t, <-done, test.causes...)
				_ = conn.Wait()
			}
			err := call(ctx, conn, "test", nil, nil)
			checkConnectionClosedError(t, err, test.causes...)
			if err != nil && !strings.Contains(err.Error(), `calling "test"`) {
				t.Errorf("call() = %v, want the method name in the error", err)
			}
		})
	}
}

type connectionClosedTest struct {
	name    string
	readErr error
	causes  []error
	close   bool
}

func connectionClosedTests() []connectionClosedTest {
	readErr := errors.New("read failed")
	wrappedReadErr := fmt.Errorf("reading: %w", readErr)
	sameMessageErr := errors.New("client is closing")
	return []connectionClosedTest{
		{name: "local close", close: true},
		{name: "EOF", readErr: io.EOF, causes: []error{io.EOF}},
		{name: "unexpected EOF", readErr: io.ErrUnexpectedEOF, causes: []error{io.ErrUnexpectedEOF}},
		{name: "read error", readErr: readErr, causes: []error{readErr}},
		{name: "wrapped read error", readErr: wrappedReadErr, causes: []error{wrappedReadErr, readErr}},
		{name: "client closing", readErr: jsonrpc2.NewError(-32003, "client is closing")},
		{name: "server closing", readErr: jsonrpc2.NewError(-32004, "server is closing")},
		{
			name:    "wrapped closing error",
			readErr: fmt.Errorf("outer: %w", fmt.Errorf("%w: %w", jsonrpc2.ErrServerClosing, wrappedReadErr)),
			causes:  []error{wrappedReadErr, readErr},
		},
		{
			name:    "joined closing errors",
			readErr: errors.Join(jsonrpc2.ErrClientClosing, errors.Join(jsonrpc2.ErrServerClosing, readErr), io.ErrUnexpectedEOF),
			causes:  []error{readErr, io.ErrUnexpectedEOF},
		},
		{name: "wire error cause", readErr: jsonrpc2.ErrInvalidParams, causes: []error{jsonrpc2.ErrInvalidParams}},
		{name: "same message cause", readErr: sameMessageErr, causes: []error{sameMessageErr}},
	}
}

func checkConnectionClosedError(t *testing.T, err error, causes ...error) {
	t.Helper()
	for _, want := range append([]error{ErrConnectionClosed}, causes...) {
		if !errors.Is(err, want) {
			t.Errorf("error = %v, want an error wrapping %v", err, want)
		}
	}
	for _, closing := range []error{jsonrpc2.ErrClientClosing, jsonrpc2.ErrServerClosing} {
		if errors.Is(err, closing) {
			t.Errorf("error = %v, must not wrap %v", err, closing)
		}
	}
	var wireErr *jsonrpc2.WireError
	if errors.As(err, &wireErr) && (wireErr.Code == -32003 || wireErr.Code == -32004) {
		t.Errorf("error = %v, exposes internal closing code %d", err, wireErr.Code)
	}
}

type unmarshalErrorResult struct {
	ResultBase
	err error
}

func (r *unmarshalErrorResult) UnmarshalJSON([]byte) error { return r.err }

func newTestJSONRPCConnection(t *testing.T, readErr error) (*jsonrpc2.Connection, Connection) {
	t.Helper()
	ctx := context.Background()
	clientT, serverT := NewInMemoryTransports()
	peer, err := serverT.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client, err := clientT.Connect(ctx)
	if err != nil {
		peer.Close()
		t.Fatal(err)
	}
	if readErr != nil {
		client = &readErrorConnection{Connection: client, err: readErr}
	}
	conn := jsonrpc2.NewConnection(ctx, jsonrpc2.ConnectionConfig{
		Reader: client,
		Writer: client,
		Closer: client,
		Bind: func(*jsonrpc2.Connection) jsonrpc2.Handler {
			return jsonrpc2.HandlerFunc(func(context.Context, *jsonrpc2.Request) (any, error) {
				return nil, jsonrpc2.ErrNotHandled
			})
		},
	})
	t.Cleanup(func() {
		peer.Close()
		conn.Close()
	})
	return conn, peer
}

type readErrorConnection struct {
	Connection
	err error
}

func (c *readErrorConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	msg, err := c.Connection.Read(ctx)
	if err != nil {
		return nil, c.err
	}
	return msg, nil
}

func TestBatchFraming(t *testing.T) {
	// This test checks that the ndjsonFramer can read and write JSON batches.
	//
	// The framer is configured to write a batch size of 2, and we confirm that
	// nothing is sent over the wire until the second write, at which point both
	// messages become available.
	ctx := context.Background()

	r, w := io.Pipe()
	tport := newIOConn(rwc{r, w})
	tport.outgoingBatch = make([]jsonrpc.Message, 0, 2)
	t.Cleanup(func() { tport.Close() })

	// Read the two messages into a channel, for easy testing later.
	read := make(chan jsonrpc.Message)
	go func() {
		for range 2 {
			msg, _ := tport.Read(ctx)
			read <- msg
		}
	}()

	// The first write should not yet be observed by the reader.
	tport.Write(ctx, &jsonrpc.Request{ID: jsonrpc2.Int64ID(1), Method: "test"})
	select {
	case got := <-read:
		t.Fatalf("after one write, got message %v", got)
	default:
	}

	// ...but the second write causes both messages to be observed.
	tport.Write(ctx, &jsonrpc.Request{ID: jsonrpc2.Int64ID(2), Method: "test"})
	for _, want := range []int64{1, 2} {
		got := <-read
		if got := got.(*jsonrpc.Request).ID.Raw(); got != want {
			t.Errorf("got message #%d, want #%d", got, want)
		}
	}
}

func TestIOConnRead(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		// protocolVersion is the version negotiated by 'initialize'; empty
		// means no session state was pushed to the connection.
		protocolVersion string
		// requested is the version the client asked for, which differs from
		// protocolVersion when the server counter-offered.
		requested string
	}{
		{
			name:  "valid json input",
			input: `{"jsonrpc":"2.0","id":1,"method":"test","params":{}}`,
			want:  "",
		},
		{
			name: "newline at the end of first valid json input",
			input: `{"jsonrpc":"2.0","id":1,"method":"test","params":{}}
			`,
			want: "",
		},
		{
			name:  "bad data at the end of first valid json input",
			input: `{"jsonrpc":"2.0","id":1,"method":"test","params":{}},`,
			want:  "invalid trailing data at the end of stream",
		},
		{
			name:            "batching unknown protocol",
			input:           `[{"jsonrpc":"2.0","id":1,"method":"test1"},{"jsonrpc":"2.0","id":2,"method":"test2"}]`,
			want:            "",
			protocolVersion: "",
		},
		{
			name:  "windows newline at the end of first valid json input",
			input: "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"test\",\"params\":{}}\r\n",
			want:  "",
		},
		{
			name:            "batching old protocol",
			input:           `[{"jsonrpc":"2.0","id":1,"method":"test1"},{"jsonrpc":"2.0","id":2,"method":"test2"}]`,
			want:            "",
			protocolVersion: protocolVersion20241105,
		},
		{
			name:            "batching new protocol",
			input:           `[{"jsonrpc":"2.0","id":1,"method":"test1"},{"jsonrpc":"2.0","id":2,"method":"test2"}]`,
			want:            "JSON-RPC batching is not supported in 2025-06-18 and later (request version: 2025-06-18)",
			protocolVersion: protocolVersion20250618,
		},
		{
			// The client asked for a version the server does not negotiate, so
			// the connection must follow the server's counter-offer (which
			// forbids batching) rather than the client's request.
			name:            "batching at a counter-offered version",
			input:           `[{"jsonrpc":"2.0","id":1,"method":"test1"},{"jsonrpc":"2.0","id":2,"method":"test2"}]`,
			want:            "JSON-RPC batching is not supported in 2025-06-18 and later (request version: 2025-11-25)",
			requested:       protocolVersion20241105,
			protocolVersion: protocolVersion20251125,
		},
		{
			// A SEP-2575 session runs no initialize. The version its first call
			// declared is recorded as negotiated once the server accepts it, and
			// the connection follows it like any version from 2025-06-18 on.
			name:            "batching on a new-protocol session",
			input:           `[{"jsonrpc":"2.0","id":1,"method":"test1"},{"jsonrpc":"2.0","id":2,"method":"test2"}]`,
			want:            "JSON-RPC batching is not supported in 2025-06-18 and later (request version: 2026-07-28)",
			protocolVersion: protocolVersion20260728,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newIOConn(rwc{
				rc: io.NopCloser(strings.NewReader(tt.input)),
			})
			t.Cleanup(func() { tr.Close() })
			if tt.protocolVersion != "" {
				requested := tt.requested
				if requested == "" {
					requested = tt.protocolVersion
				}
				tr.sessionUpdated(ServerSessionState{
					InitializeParams: &InitializeParams{
						ProtocolVersion: requested,
					},
					NegotiatedProtocolVersion: tt.protocolVersion,
				})
			}
			_, err := tr.Read(context.Background())
			if err == nil && tt.want != "" {
				t.Errorf("ioConn.Read() got nil error but wanted %v", tt.want)
			}
			if err != nil && err.Error() != tt.want {
				t.Errorf("ioConn.Read() = %v, want %v", err.Error(), tt.want)
			}
		})
	}
}

// go-sdk#976: stdio must surface empty-method calls as requests, not responses.
func TestIOConnRead_EmptyMethod(t *testing.T) {
	tr := newIOConn(rwc{
		rc: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":5,"method":"","params":{}}`)),
	})
	t.Cleanup(func() { tr.Close() })

	msg, err := tr.Read(context.Background())
	if err != nil {
		t.Fatalf("ioConn.Read() error = %v", err)
	}
	req, ok := msg.(*jsonrpc.Request)
	if !ok {
		t.Fatalf("message type = %T, want *jsonrpc.Request", msg)
	}
	if req.Method != "" {
		t.Errorf("Method = %q, want empty string", req.Method)
	}
	if req.ID != jsonrpc2.Int64ID(5) {
		t.Errorf("ID = %v, want 5", req.ID.Raw())
	}
}

// TestIOConnReadBatchNotifications is a regression test for go-sdk#1256:
// notifications carried inside a JSON-RPC batch must not be tracked as awaiting
// a response. Only calls belong in a batch's response array, so a batch's
// notifications must neither keep it from completing nor pollute the
// per-connection duplicate-ID tracking.
func TestIOConnReadBatchNotifications(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		// input is one or more newline-separated batch payloads.
		input string
		// wantCalls records, for each message Read returns in order, whether it
		// is a call (true) or a notification (false). Reading every message
		// must succeed.
		wantCalls []bool
		// wantTrackedCalls is the number of in-flight call requests tracked across
		// batches awaiting a response once every message has been read, before any
		// response is written.
		wantTrackedCalls int
		// respondIDs are the call IDs to respond to, in order.
		respondIDs []int64
		// wantWrites are the batch response payloads expected on the wire, each
		// matched as a substring.
		wantWrites []string
	}{
		{
			name:             "call and notification is answered",
			input:            `[{"jsonrpc":"2.0","id":2,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}]`,
			wantCalls:        []bool{true, false},
			wantTrackedCalls: 1,
			respondIDs:       []int64{2},
			wantWrites:       []string{`[{"jsonrpc":"2.0","id":2,"result":{}}]`},
		},
		{
			name:             "only notifications keeps the connection open",
			input:            `[{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":98}},{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}]`,
			wantCalls:        []bool{false, false},
			wantTrackedCalls: 0,
		},
		{
			name: "repeated batches holding a notification",
			input: `[{"jsonrpc":"2.0","id":2,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}]` + "\n" +
				`[{"jsonrpc":"2.0","id":3,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}]`,
			wantCalls:        []bool{true, false, true, false},
			wantTrackedCalls: 2,
			respondIDs:       []int64{2, 3},
			wantWrites: []string{
				`[{"jsonrpc":"2.0","id":2,"result":{}}]`,
				`[{"jsonrpc":"2.0","id":3,"result":{}}]`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := new(bytes.Buffer)
			tr := newIOConn(rwc{
				rc: io.NopCloser(strings.NewReader(tt.input)),
				wc: nopCloserWriter{sink},
			})
			t.Cleanup(func() { tr.Close() })

			for i, wantCall := range tt.wantCalls {
				msg, err := tr.Read(ctx)
				if err != nil {
					t.Fatalf("Read() #%d: unexpected error: %v", i, err)
				}
				req, ok := msg.(*jsonrpc.Request)
				if !ok {
					t.Fatalf("Read() #%d = %T, want *jsonrpc.Request", i, msg)
				}
				if req.IsCall() != wantCall {
					t.Fatalf("Read() #%d IsCall() = %t, want %t", i, req.IsCall(), wantCall)
				}
			}

			// Only calls are tracked as awaiting a response; notifications must
			// not linger in the batch bookkeeping.
			tr.batchMu.Lock()
			gotTrackedCalls := len(tr.batches)
			tr.batchMu.Unlock()
			if gotTrackedCalls != tt.wantTrackedCalls {
				t.Errorf("tracked calls = %d, want %d", gotTrackedCalls, tt.wantTrackedCalls)
			}

			for _, id := range tt.respondIDs {
				resp := &jsonrpc.Response{ID: jsonrpc2.Int64ID(id), Result: []byte("{}")}
				if err := tr.Write(ctx, resp); err != nil {
					t.Fatalf("Write(response id=%d): %v", id, err)
				}
			}

			got := sink.String()
			for _, want := range tt.wantWrites {
				if !strings.Contains(got, want) {
					t.Errorf("batch responses = %q, missing %q", got, want)
				}
			}
		})
	}
}

// TestIOConnFrameCap verifies single inbound frame cap enforcement.
func TestIOConnFrameCap(t *testing.T) {
	tests := []struct {
		name             string
		r                io.ReadCloser
		limit            int
		wantMessageCount int
		wantErr          bool
	}{
		{
			name:    "infinite string",
			r:       &endlessReader{prefix: `"`, repeat: "A"},
			limit:   1024,
			wantErr: true,
		},
		{
			name:    "cap does not reset per new line",
			r:       &endlessReader{prefix: "[", repeat: "0,\n"},
			limit:   1024,
			wantErr: true,
		},
		{
			name:  "cap resets per message",
			limit: 1024, // < 600 * 3
			r: io.NopCloser(strings.NewReader(func() string {
				var b strings.Builder
				for i := range 3 {
					fmt.Fprintf(&b, `{"jsonrpc":"2.0","id":%d,"method":"test","params":{"pad":"`, i)
					b.WriteString(strings.Repeat("A", 600))
					b.WriteString(`"}}`)
					b.WriteByte('\n')
				}
				return b.String()
			}())),
			wantMessageCount: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newIOConnLimited(rwc{rc: tt.r}, tt.limit)
			t.Cleanup(func() { tr.Close() })

			read := func() (jsonrpc.Message, error) {
				type result struct {
					msg jsonrpc.Message
					err error
				}
				done := make(chan result, 1)

				go func() {
					msg, err := tr.Read(context.Background())
					done <- result{msg, err}
				}()

				select {
				case r := <-done:
					return r.msg, r.err
				case <-time.After(10 * time.Second):
					t.Fatal("Read() did not return: frame cap failed to trip")
					return nil, nil
				}
			}

			for i := range tt.wantMessageCount {
				msg, err := read()
				if err != nil {
					t.Fatalf("Read() #%d error = %v, want nil", i, err)
				}
				if msg == nil {
					t.Fatalf("Read() #%d returned nil message", i)
				}
			}
			if tt.wantErr {
				if _, err := read(); !errors.Is(err, errFrameTooLarge) {
					t.Fatalf("Read() error = %v, want errFrameTooLarge", err)
				}
			}
		})
	}
}

// TestIOConnDropResponse checks that ioConn discards the response to a call
// the peer cancelled on a session using protocol version 2026-07-28 or later,
// and still sends it on earlier versions.
func TestIOConnDropResponse(t *testing.T) {
	tests := []struct {
		name    string
		version string
		drop    bool  // whether the peer cancelled call 1
		want    []any // the IDs of the responses on the wire, in order
	}{
		{"new protocol, cancelled", protocolVersion20260728, true, []any{int64(2)}},
		{"new protocol, not cancelled", protocolVersion20260728, false, []any{int64(1), int64(2)}},
		{"earlier protocol, cancelled", protocolVersion20251125, true, []any{int64(1), int64(2)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var out bytes.Buffer
			tr := newIOConn(rwc{
				rc: io.NopCloser(strings.NewReader("")),
				wc: nopCloserWriter{&out},
			})
			t.Cleanup(func() { tr.Close() })
			tr.sessionUpdated(ServerSessionState{NegotiatedProtocolVersion: test.version})

			if test.drop {
				tr.DropResponse(jsonrpc2.Int64ID(1))
			}
			for _, id := range []int64{1, 2} {
				if err := tr.Write(ctx, &jsonrpc.Response{ID: jsonrpc2.Int64ID(id), Result: []byte("{}")}); err != nil {
					t.Fatalf("Write(response %d): %v", id, err)
				}
			}

			var got []any
			for line := range strings.Lines(out.String()) {
				msg, err := jsonrpc2.DecodeMessage([]byte(line))
				if err != nil {
					t.Fatalf("decoding %q: %v", line, err)
				}
				got = append(got, msg.(*jsonrpc.Response).ID.Raw())
			}
			if fmt.Sprint(got) != fmt.Sprint(test.want) {
				t.Errorf("responses on the wire: %v, want %v", got, test.want)
			}
			if len(tr.dropped) != 0 {
				t.Errorf("after the responses were written, %d dropped IDs are still recorded", len(tr.dropped))
			}
		})
	}
}

// TestIOConnCancelledCallGetsNoResponse checks end to end, over an in-memory
// transport wrapped for logging, that on a 2026-07-28 session a call the
// client cancels with notifications/cancelled gets no response.
func TestIOConnCancelledCallGetsNoResponse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	started := make(chan struct{})
	server := NewServer(testImpl, nil)
	AddTool(server, &Tool{Name: "slow"}, func(ctx context.Context, _ *CallToolRequest, _ struct{}) (*CallToolResult, any, error) {
		close(started)
		<-ctx.Done()
		return nil, nil, ctx.Err()
	})
	AddTool(server, &Tool{Name: "fast"}, func(context.Context, *CallToolRequest, struct{}) (*CallToolResult, any, error) {
		return &CallToolResult{}, nil, nil
	})
	serverT, clientT := NewInMemoryTransports()
	var log safeBuffer
	ss, err := server.Connect(ctx, &LoggingTransport{Transport: serverT, Writer: &log}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	peer := clientT.rwc
	received := make(chan jsonrpc.Message, 10)
	go func() {
		defer close(received)
		scanner := bufio.NewScanner(peer)
		for scanner.Scan() {
			msg, err := jsonrpc2.DecodeMessage(scanner.Bytes())
			if err != nil {
				t.Errorf("decoding %q: %v", scanner.Bytes(), err)
				return
			}
			received <- msg
		}
	}()
	send := func(msg jsonrpc.Message) {
		t.Helper()
		data, err := jsonrpc2.EncodeMessage(msg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := peer.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	// awaitResponse returns once the response to id arrives, and fails if a
	// response to the cancelled call arrives first.
	awaitResponse := func(id int64) {
		t.Helper()
		for {
			select {
			case msg := <-received:
				resp, ok := msg.(*jsonrpc.Response)
				if !ok {
					continue
				}
				switch resp.ID.Raw() {
				case int64(2):
					t.Fatalf("the cancelled call got a response: %+v", resp)
				case id:
					return
				}
			case <-ctx.Done():
				t.Fatalf("timed out waiting for the response to %d", id)
			}
		}
	}

	// A 2026-07-28 session has no initialize: each request declares the
	// protocol version in its _meta.
	meta := newProtocolMeta("")
	send(req(2, "tools/call", &CallToolParams{Meta: meta, Name: "slow"}))
	<-started
	send(req(0, notificationCancelled, &CancelledParams{RequestID: int64(2)}))

	// The logging wrapper logs the response once ioConn has dropped it; after
	// that, a response sent on the wire would precede the next call's.
	for !strings.Contains(string(log.Bytes()), `write: {"jsonrpc":"2.0","id":2,`) {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for the cancelled call's handler to return; log:\n%s", log.Bytes())
		case <-time.After(5 * time.Millisecond):
		}
	}
	send(req(3, "tools/call", &CallToolParams{Meta: meta, Name: "fast"}))
	awaitResponse(3)
}

// droppingConn is a Connection that records the ids it was told will receive
// no response. It implements [jsonrpc2.ResponseDropper]; plainConn, below,
// deliberately does not.
type droppingConn struct {
	dropped []jsonrpc.ID
}

func (c *droppingConn) SessionID() string                             { return "" }
func (c *droppingConn) Read(context.Context) (jsonrpc.Message, error) { return nil, io.EOF }
func (c *droppingConn) Write(context.Context, jsonrpc.Message) error  { return nil }
func (c *droppingConn) Close() error                                  { return nil }
func (c *droppingConn) DropResponse(id jsonrpc.ID)                    { c.dropped = append(c.dropped, id) }

// plainConn is a Connection that holds no per-call state, so it implements no
// ResponseDropper.
type plainConn struct{}

func (plainConn) SessionID() string                             { return "" }
func (plainConn) Read(context.Context) (jsonrpc.Message, error) { return nil, io.EOF }
func (plainConn) Write(context.Context, jsonrpc.Message) error  { return nil }
func (plainConn) Close() error                                  { return nil }

// TestLoggingConnDropResponse checks that wrapping a transport for logging
// does not lose the "this call gets no response" signal: loggingConn is the
// one Connection wrapper here, and it used to leave the POST stream open.
func TestLoggingConnDropResponse(t *testing.T) {
	t.Run("forwards to a delegate that drops responses", func(t *testing.T) {
		delegate := &droppingConn{}
		var conn Connection = &loggingConn{delegate: delegate, w: io.Discard}

		dropper, ok := conn.(jsonrpc2.ResponseDropper)
		if !ok {
			t.Fatal("loggingConn does not implement jsonrpc2.ResponseDropper, so a cancelled call's response is never dropped")
		}
		dropper.DropResponse(jsonrpc.ID{})
		if len(delegate.dropped) != 1 {
			t.Errorf("the delegate was told about %d dropped responses, want 1", len(delegate.dropped))
		}
	})

	t.Run("does nothing for a delegate that does not", func(t *testing.T) {
		conn := &loggingConn{delegate: plainConn{}, w: io.Discard}
		conn.DropResponse(jsonrpc.ID{})
	})
}
