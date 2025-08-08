// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

const (
	protocolVersionHeader = "Mcp-Protocol-Version"
	sessionIDHeader       = "Mcp-Session-Id"
)

// A StreamableHTTPHandler is an http.Handler that serves streamable MCP
// sessions, as defined by the [MCP spec].
//
// [MCP spec]: https://modelcontextprotocol.io/2025/03/26/streamable-http-transport.html
type StreamableHTTPHandler struct {
	getServer func(*http.Request) *Server
	opts      StreamableHTTPOptions

	sessionsMu sync.Mutex
	sessions   map[string]*StreamableServerTransport // keyed by IDs (from Mcp-Session-Id header)
}

// StreamableHTTPOptions is a placeholder options struct for future
// configuration of the StreamableHTTP handler.
type StreamableHTTPOptions struct {
	// TODO: support configurable session ID generation (?)
	// TODO: support session retention (?)

	// transportOptions sets the streamable server transport options to use when
	// establishing a new session.
	transportOptions *StreamableServerTransportOptions
}

// NewStreamableHTTPHandler returns a new [StreamableHTTPHandler].
//
// The getServer function is used to create or look up servers for new
// sessions. It is OK for getServer to return the same server multiple times.
// If getServer returns nil, a 400 Bad Request will be served.
func NewStreamableHTTPHandler(getServer func(*http.Request) *Server, opts *StreamableHTTPOptions) *StreamableHTTPHandler {
	h := &StreamableHTTPHandler{
		getServer: getServer,
		sessions:  make(map[string]*StreamableServerTransport),
	}
	if opts != nil {
		h.opts = *opts
	}
	return h
}

// closeAll closes all ongoing sessions.
//
// TODO(rfindley): investigate the best API for callers to configure their
// session lifecycle. (?)
//
// Should we allow passing in a session store? That would allow the handler to
// be stateless.
func (h *StreamableHTTPHandler) closeAll() {
	h.sessionsMu.Lock()
	defer h.sessionsMu.Unlock()
	for _, s := range h.sessions {
		s.Close()
	}
	h.sessions = nil
}

func (h *StreamableHTTPHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Allow multiple 'Accept' headers.
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Accept#syntax
	accept := strings.Split(strings.Join(req.Header.Values("Accept"), ","), ",")
	var jsonOK, streamOK bool
	for _, c := range accept {
		switch strings.TrimSpace(c) {
		case "application/json":
			jsonOK = true
		case "text/event-stream":
			streamOK = true
		}
	}

	if req.Method == http.MethodGet {
		if !streamOK {
			http.Error(w, "Accept must contain 'text/event-stream' for GET requests", http.StatusBadRequest)
			return
		}
	} else if !jsonOK || !streamOK {
		http.Error(w, "Accept must contain both 'application/json' and 'text/event-stream'", http.StatusBadRequest)
		return
	}

	var session *StreamableServerTransport
	if id := req.Header.Get(sessionIDHeader); id != "" {
		h.sessionsMu.Lock()
		session, _ = h.sessions[id]
		h.sessionsMu.Unlock()
		if session == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
	}

	// TODO(rfindley): simplify the locking so that each request has only one
	// critical section.
	if req.Method == http.MethodDelete {
		if session == nil {
			// => Mcp-Session-Id was not set; else we'd have returned NotFound above.
			http.Error(w, "DELETE requires an Mcp-Session-Id header", http.StatusBadRequest)
			return
		}
		h.sessionsMu.Lock()
		delete(h.sessions, session.sessionID)
		h.sessionsMu.Unlock()
		session.Close()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	switch req.Method {
	case http.MethodPost, http.MethodGet:
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
		return
	}

	if session == nil {
		s := NewStreamableServerTransport(randText(), h.opts.transportOptions)
		server := h.getServer(req)
		if server == nil {
			// The getServer argument to NewStreamableHTTPHandler returned nil.
			http.Error(w, "no server available", http.StatusBadRequest)
			return
		}
		// Pass req.Context() here, to allow middleware to add context values.
		// The context is detached in the jsonrpc2 library when handling the
		// long-running stream.
		if _, err := server.Connect(req.Context(), s); err != nil {
			http.Error(w, "failed connection", http.StatusInternalServerError)
			return
		}
		h.sessionsMu.Lock()
		h.sessions[s.sessionID] = s
		h.sessionsMu.Unlock()
		session = s
	}

	session.ServeHTTP(w, req)
}

type StreamableServerTransportOptions struct {
	// Storage for events, to enable stream resumption.
	// If nil, a [MemoryEventStore] with the default maximum size will be used.
	EventStore EventStore

	// jsonResponse, if set, tells the server to prefer to respond to requests
	// using application/json responses rather than text/event-stream.
	//
	// Specifically, responses will be application/json whenever incoming POST
	// request contain only a single message. In this case, notifications or
	// requests made within the context of a server request will be sent to the
	// hanging GET request, if any.
	jsonResponse bool
}

// NewStreamableServerTransport returns a new [StreamableServerTransport] with
// the given session ID and options.
// The session ID must be globally unique, that is, different from any other
// session ID anywhere, past and future. (We recommend using a crypto random number
// generator to produce one, as with [crypto/rand.Text].)
//
// A StreamableServerTransport implements the server-side of the streamable
// transport.
func NewStreamableServerTransport(sessionID string, opts *StreamableServerTransportOptions) *StreamableServerTransport {
	if opts == nil {
		opts = &StreamableServerTransportOptions{}
	}
	t := &StreamableServerTransport{
		sessionID:      sessionID,
		incoming:       make(chan jsonrpc.Message, 10),
		done:           make(chan struct{}),
		streams:        make(map[StreamID]*stream),
		requestStreams: make(map[jsonrpc.ID]StreamID),
	}
	// Stream 0 corresponds to the hanging 'GET'.
	//
	// It is always text/event-stream, since it must carry arbitrarily many
	// messages.
	t.streams[0] = newStream(0, false)
	if opts != nil {
		t.opts = *opts
	}
	if t.opts.EventStore == nil {
		t.opts.EventStore = NewMemoryEventStore(nil)
	}
	return t
}

func (t *StreamableServerTransport) SessionID() string {
	return t.sessionID
}

// A StreamableServerTransport implements the [Transport] interface for a
// single session.
type StreamableServerTransport struct {
	lastStreamID atomic.Int64 // last stream ID used, atomically incremented

	sessionID string
	opts      StreamableServerTransportOptions
	incoming  chan jsonrpc.Message // messages from the client to the server
	done      chan struct{}

	mu sync.Mutex
	// Sessions are closed exactly once.
	isDone bool

	// Sessions can have multiple logical connections (which we call streams),
	// corresponding to HTTP requests. Additionally, streams may be resumed by
	// subsequent HTTP requests, when the HTTP connection is terminated
	// unexpectedly.
	//
	// Therefore, we use a logical stream ID to key the stream state, and
	// perform the accounting described below when incoming HTTP requests are
	// handled.

	// streams holds the logical streams for this session, keyed by their ID.
	// TODO: streams are never deleted, so the memory for a connection grows without
	// bound. If we deleted a stream when the response is sent, we would lose the ability
	// to replay if there was a cut just before the response was transmitted.
	// Perhaps we could have a TTL for streams that starts just after the response.
	streams map[StreamID]*stream

	// requestStreams maps incoming requests to their logical stream ID.
	//
	// Lifecycle: requestStreams persist for the duration of the session.
	//
	// TODO: clean up once requests are handled. See the TODO for streams above.
	requestStreams map[jsonrpc.ID]StreamID
}

// A stream is a single logical stream of SSE events within a server session.
// A stream begins with a client request, or with a client GET that has
// no Last-Event-ID header.
// A stream ends only when its session ends; we cannot determine its end otherwise,
// since a client may send a GET with a Last-Event-ID that references the stream
// at any time.
type stream struct {
	// id is the logical ID for the stream, unique within a session.
	// ID 0 is used for messages that don't correlate with an incoming request.
	id StreamID

	// jsonResponse records whether this stream should respond with application/json
	// instead of text/event-stream.
	//
	// See [StreamableServerTransportOptions.jsonResponse].
	jsonResponse bool

	// signal is a 1-buffered channel, owned by an incoming HTTP request, that signals
	// that there are messages available to write into the HTTP response.
	// In addition, the presence of a channel guarantees that at most one HTTP response
	// can receive messages for a logical stream. After claiming the stream, incoming
	// requests should read from outgoing, to ensure that no new messages are missed.
	//
	// To simplify locking, signal is an atomic. We need an atomic.Pointer, because
	// you can't set an atomic.Value to nil.
	//
	// Lifecycle: each channel value persists for the duration of an HTTP POST or
	// GET request for the given streamID.
	signal atomic.Pointer[chan struct{}]

	// The following mutable fields are protected by the mutex of the containing
	// StreamableServerTransport.

	// outgoing is the list of outgoing messages, enqueued by server methods that
	// write notifications and responses, and dequeued by streamResponse.
	outgoing [][]byte

	// streamRequests is the set of unanswered incoming RPCs for the stream.
	//
	// Requests persist until their response data has been added to outgoing.
	requests map[jsonrpc.ID]struct{}
}

func newStream(id StreamID, jsonResponse bool) *stream {
	return &stream{
		id:           id,
		jsonResponse: jsonResponse,
		requests:     make(map[jsonrpc.ID]struct{}),
	}
}

func signalChanPtr() *chan struct{} {
	c := make(chan struct{}, 1)
	return &c
}

// A StreamID identifies a stream of SSE events. It is unique within the stream's
// [ServerSession].
type StreamID int64

// Connect implements the [Transport] interface.
//
// TODO(rfindley): Connect should return a new object. (Why?)
func (s *StreamableServerTransport) Connect(context.Context) (Connection, error) {
	return s, nil
}

// We track the incoming request ID inside the handler context using
// idContextValue, so that notifications and server->client calls that occur in
// the course of handling incoming requests are correlated with the incoming
// request that caused them, and can be dispatched as server-sent events to the
// correct HTTP request.
//
// Currently, this is implemented in [ServerSession.handle]. This is not ideal,
// because it means that a user of the MCP package couldn't implement the
// streamable transport, as they'd lack this privileged access.
//
// If we ever wanted to expose this mechanism, we have a few options:
//  1. Make ServerSession an interface, and provide an implementation of
//     ServerSession to handlers that closes over the incoming request ID.
//  2. Expose a 'HandlerTransport' interface that allows transports to provide
//     a handler middleware, so that we don't hard-code this behavior in
//     ServerSession.handle.
//  3. Add a `func ForRequest(context.Context) jsonrpc.ID` accessor that lets
//     any transport access the incoming request ID.
//
// For now, by giving only the StreamableServerTransport access to the request
// ID, we avoid having to make this API decision.
type idContextKey struct{}

// ServeHTTP handles a single HTTP request for the session.
func (t *StreamableServerTransport) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		t.serveGET(w, req)
	case http.MethodPost:
		t.servePOST(w, req)
	default:
		// Should not be reached, as this is checked in StreamableHTTPHandler.ServeHTTP.
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
	}
}

// serveGET streams messages to a hanging http GET, with stream ID and last
// message parsed from the Last-Event-ID header.
//
// It returns an HTTP status code and error message.
func (t *StreamableServerTransport) serveGET(w http.ResponseWriter, req *http.Request) {
	// connID 0 corresponds to the default GET request.
	id := StreamID(0)
	// By default, we haven't seen a last index. Since indices start at 0, we represent
	// that by -1. This is incremented just before each event is written, in streamResponse
	// around L407.
	lastIdx := -1
	if len(req.Header.Values("Last-Event-ID")) > 0 {
		eid := req.Header.Get("Last-Event-ID")
		var ok bool
		id, lastIdx, ok = parseEventID(eid)
		if !ok {
			http.Error(w, fmt.Sprintf("malformed Last-Event-ID %q", eid), http.StatusBadRequest)
			return
		}
	}

	t.mu.Lock()
	stream, ok := t.streams[id]
	t.mu.Unlock()
	if !ok {
		http.Error(w, "unknown stream", http.StatusBadRequest)
		return
	}
	if !stream.signal.CompareAndSwap(nil, signalChanPtr()) {
		// The CAS returned false, meaning that the comparison failed: stream.signal is not nil.
		http.Error(w, "stream ID conflicts with ongoing stream", http.StatusConflict)
		return
	}
	defer stream.signal.Store(nil)
	persistent := id == 0 // Only the special stream 0 is a hanging get.
	t.respondSSE(stream, w, req, lastIdx, persistent)
}

// servePOST handles an incoming message, and replies with either an outgoing
// message stream or single response object, depending on whether the
// jsonResponse option is set.
//
// It returns an HTTP status code and error message.
func (t *StreamableServerTransport) servePOST(w http.ResponseWriter, req *http.Request) {
	if len(req.Header.Values("Last-Event-ID")) > 0 {
		http.Error(w, "can't send Last-Event-ID for POST request", http.StatusBadRequest)
		return
	}

	// Read incoming messages.
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		http.Error(w, "POST requires a non-empty body", http.StatusBadRequest)
		return
	}
	// TODO(#21): if the negotiated protocol version is 2025-06-18 or later,
	// we should not allow batching here.
	//
	// This also requires access to the negotiated version, which would either be
	// set by the MCP-Protocol-Version header, or would require peeking into the
	// session.
	incoming, _, err := readBatch(body)
	if err != nil {
		http.Error(w, fmt.Sprintf("malformed payload: %v", err), http.StatusBadRequest)
		return
	}
	requests := make(map[jsonrpc.ID]struct{})
	for _, msg := range incoming {
		if req, ok := msg.(*jsonrpc.Request); ok {
			// Preemptively check that this is a valid request, so that we can fail
			// the HTTP request. If we didn't do this, a request with a bad method or
			// missing ID could be silently swallowed.
			if _, err := checkRequest(req, serverMethodInfos); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if req.ID.IsValid() {
				requests[req.ID] = struct{}{}
			}
		}
	}

	var stream *stream // if non-nil, used to handle requests

	// If we have requests, we need to handle responses along with any
	// notifications or server->client requests made in the course of handling.
	// Update accounting for this incoming payload.
	if len(requests) > 0 {
		stream = newStream(StreamID(t.lastStreamID.Add(1)), t.opts.jsonResponse)
		t.mu.Lock()
		t.streams[stream.id] = stream
		stream.requests = requests
		for reqID := range requests {
			t.requestStreams[reqID] = stream.id
		}
		t.mu.Unlock()
		stream.signal.Store(signalChanPtr())
	}

	// Publish incoming messages.
	for _, msg := range incoming {
		t.incoming <- msg
	}

	if stream == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if stream.jsonResponse {
		t.respondJSON(stream, w, req)
	} else {
		t.respondSSE(stream, w, req, -1, false)
	}
}

func (t *StreamableServerTransport) respondJSON(stream *stream, w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(sessionIDHeader, t.sessionID)

	var msgs []json.RawMessage
	ctx := req.Context()
	for msg, ok := range t.messages(ctx, stream, false) {
		if !ok {
			if ctx.Err() != nil {
				w.WriteHeader(http.StatusNoContent)
				return
			} else {
				http.Error(w, http.StatusText(http.StatusGone), http.StatusGone)
				return
			}
		}
		msgs = append(msgs, msg)
	}
	var data []byte
	if len(msgs) == 1 {
		data = []byte(msgs[0])
	} else {
		// TODO: add tests for batch responses, or disallow them entirely.
		var err error
		data, err = json.Marshal(msgs)
		if err != nil {
			http.Error(w, fmt.Sprintf("internal error marshalling response: %v", err), http.StatusInternalServerError)
			return
		}
	}
	_, _ = w.Write(data) // ignore error: client disconnected
}

// lastIndex is the index of the last seen event if resuming, else -1.
func (t *StreamableServerTransport) respondSSE(stream *stream, w http.ResponseWriter, req *http.Request, lastIndex int, persistent bool) {
	writes := 0

	// Accept checked in [StreamableHTTPHandler]
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Content-Type", "text/event-stream") // Accept checked in [StreamableHTTPHandler]
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set(sessionIDHeader, t.sessionID)

	// write one event containing data.
	write := func(data []byte) bool {
		lastIndex++
		e := Event{
			Name: "message",
			ID:   formatEventID(stream.id, lastIndex),
			Data: data,
		}
		if _, err := writeEvent(w, e); err != nil {
			// Connection closed or broken.
			// TODO(#170): log when we add server-side logging.
			return false
		}
		writes++
		return true
	}

	errorf := func(code int, format string, args ...any) {
		if writes == 0 {
			http.Error(w, fmt.Sprintf(format, args...), code)
		} else {
			// TODO(#170): log when we add server-side logging
		}
	}

	if lastIndex >= 0 {
		// Resume.
		for data, err := range t.opts.EventStore.After(req.Context(), t.SessionID(), stream.id, lastIndex) {
			if err != nil {
				// TODO: reevaluate these status codes.
				// Maybe distinguish between storage errors, which are 500s, and missing
				// session or stream ID--can these arise from bad input?
				status := http.StatusInternalServerError
				if errors.Is(err, ErrEventsPurged) {
					status = http.StatusInsufficientStorage
				}
				errorf(status, "failed to read events: %v", err)
				return
			}
			// The iterator yields events beginning just after lastIndex, or it would have
			// yielded an error.
			if !write(data) {
				return
			}
		}
	}

	// Repeatedly collect pending outgoing events and send them.
	ctx := req.Context()
	for msg, ok := range t.messages(ctx, stream, persistent) {
		if !ok {
			if ctx.Err() != nil && writes == 0 {
				// This probably doesn't matter, but respond with NoContent if the client disconnected.
				w.WriteHeader(http.StatusNoContent)
			} else {
				errorf(http.StatusGone, "stream terminated")
			}
			return
		}
		if err := t.opts.EventStore.Append(req.Context(), t.SessionID(), stream.id, msg); err != nil {
			errorf(http.StatusInternalServerError, "storing event: %v", err.Error())
			return
		}
		if !write(msg) {
			return
		}
	}
}

// messages iterates over messages sent to the current stream.
//
// The first iterated value is the received JSON message. The second iterated
// value is an OK value indicating whether the stream terminated normally.
//
// If the stream did not terminate normally, it is either because ctx was
// cancelled, or the connection is closed: check the ctx.Err() to differentiate
// these cases.
func (t *StreamableServerTransport) messages(ctx context.Context, stream *stream, persistent bool) iter.Seq2[json.RawMessage, bool] {
	return func(yield func(json.RawMessage, bool) bool) {
		for {
			t.mu.Lock()
			outgoing := stream.outgoing
			stream.outgoing = nil
			nOutstanding := len(stream.requests)
			t.mu.Unlock()

			for _, data := range outgoing {
				if !yield(data, true) {
					return
				}
			}

			// If all requests have been handled and replied to, we should terminate this connection.
			// "After the JSON-RPC response has been sent, the server SHOULD close the SSE stream."
			// §6.4, https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#sending-messages-to-the-server
			// We only want to terminate POSTs, and GETs that are replaying. The general-purpose GET
			// (stream ID 0) will never have requests, and should remain open indefinitely.
			if nOutstanding == 0 && !persistent {
				return
			}

			select {
			case <-*stream.signal.Load(): // there are new outgoing messages
				// return to top of loop
			case <-t.done: // session is closed
				yield(nil, false)
				return
			case <-ctx.Done():
				yield(nil, false)
				return
			}
		}
	}
}

// Event IDs: encode both the logical connection ID and the index, as
// <streamID>_<idx>, to be consistent with the typescript implementation.

// formatEventID returns the event ID to use for the logical connection ID
// streamID and message index idx.
//
// See also [parseEventID].
func formatEventID(sid StreamID, idx int) string {
	return fmt.Sprintf("%d_%d", sid, idx)
}

// parseEventID parses a Last-Event-ID value into a logical stream id and
// index.
//
// See also [formatEventID].
func parseEventID(eventID string) (sid StreamID, idx int, ok bool) {
	parts := strings.Split(eventID, "_")
	if len(parts) != 2 {
		return 0, 0, false
	}
	stream, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || stream < 0 {
		return 0, 0, false
	}
	idx, err = strconv.Atoi(parts[1])
	if err != nil || idx < 0 {
		return 0, 0, false
	}
	return StreamID(stream), idx, true
}

// Read implements the [Connection] interface.
func (t *StreamableServerTransport) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-t.incoming:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-t.done:
		return nil, io.EOF
	}
}

// Write implements the [Connection] interface.
func (t *StreamableServerTransport) Write(ctx context.Context, msg jsonrpc.Message) error {
	// Find the incoming request that this write relates to, if any.
	var forRequest jsonrpc.ID
	isResponse := false
	if resp, ok := msg.(*jsonrpc.Response); ok {
		// If the message is a response, it relates to its request (of course).
		forRequest = resp.ID
		isResponse = true
	} else {
		// Otherwise, we check to see if it request was made in the context of an
		// ongoing request. This may not be the case if the request was made with
		// an unrelated context.
		if v := ctx.Value(idContextKey{}); v != nil {
			forRequest = v.(jsonrpc.ID)
		}
	}

	// Find the logical connection corresponding to this request.
	//
	// For messages sent outside of a request context, this is the default
	// connection 0.
	var forStream StreamID
	if forRequest.IsValid() {
		t.mu.Lock()
		forStream = t.requestStreams[forRequest]
		t.mu.Unlock()
	}

	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.isDone {
		return errors.New("session is closed")
	}

	stream := t.streams[forStream]
	if stream == nil {
		return fmt.Errorf("no stream with ID %d", forStream)
	}

	// Special case a few conditions where we fall back on stream 0 (the hanging GET):
	//
	//  - if forStream is known, but the associated stream is logically complete
	//  - if the stream is application/json, but the message is not a response
	//
	// TODO(rfindley): either of these, particularly the first, might be
	// considered a bug in the server. Report it through a side-channel?
	if len(stream.requests) == 0 && forStream != 0 || stream.jsonResponse && !isResponse {
		stream = t.streams[0]
	}

	// TODO: if there is nothing to send these messages to (as would happen, for example, if forConn == 0
	// and the client never did a GET), then memory will grow without bound. Consider a mitigation.
	stream.outgoing = append(stream.outgoing, data)
	if isResponse {
		// Once we've put the reply on the queue, it's no longer outstanding.
		delete(stream.requests, forRequest)
	}

	// Signal streamResponse that new work is available.
	signalp := stream.signal.Load()
	if signalp != nil {
		select {
		case *signalp <- struct{}{}:
		default:
		}
	}
	return nil
}

// Close implements the [Connection] interface.
func (t *StreamableServerTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isDone {
		t.isDone = true
		close(t.done)
		// TODO: find a way to plumb a context here, or an event store with a long-running
		// close operation can take arbitrary time. Alternative: impose a fixed timeout here.
		return t.opts.EventStore.SessionClosed(context.TODO(), t.sessionID)
	}
	return nil
}

// A StreamableClientTransport is a [Transport] that can communicate with an MCP
// endpoint serving the streamable HTTP transport defined by the 2025-03-26
// version of the spec.
type StreamableClientTransport struct {
	url  string
	opts StreamableClientTransportOptions
}

// StreamableReconnectOptions defines parameters for client reconnect attempts.
type StreamableReconnectOptions struct {
	// MaxRetries is the maximum number of times to attempt a reconnect before giving up.
	// A value of 0 or less means never retry.
	MaxRetries int

	// growFactor is the multiplicative factor by which the delay increases after each attempt.
	// A value of 1.0 results in a constant delay, while a value of 2.0 would double it each time.
	// It must be 1.0 or greater if MaxRetries is greater than 0.
	growFactor float64

	// initialDelay is the base delay for the first reconnect attempt.
	initialDelay time.Duration

	// maxDelay caps the backoff delay, preventing it from growing indefinitely.
	maxDelay time.Duration
}

// DefaultReconnectOptions provides sensible defaults for reconnect logic.
var DefaultReconnectOptions = &StreamableReconnectOptions{
	MaxRetries:   5,
	growFactor:   1.5,
	initialDelay: 1 * time.Second,
	maxDelay:     30 * time.Second,
}

// StreamableClientTransportOptions provides options for the
// [NewStreamableClientTransport] constructor.
type StreamableClientTransportOptions struct {
	// HTTPClient is the client to use for making HTTP requests. If nil,
	// http.DefaultClient is used.
	HTTPClient       *http.Client
	ReconnectOptions *StreamableReconnectOptions
}

// NewStreamableClientTransport returns a new client transport that connects to
// the streamable HTTP server at the provided URL.
func NewStreamableClientTransport(url string, opts *StreamableClientTransportOptions) *StreamableClientTransport {
	t := &StreamableClientTransport{url: url}
	if opts != nil {
		t.opts = *opts
	}
	return t
}

// Connect implements the [Transport] interface.
//
// The resulting [Connection] writes messages via POST requests to the
// transport URL with the Mcp-Session-Id header set, and reads messages from
// hanging requests.
//
// When closed, the connection issues a DELETE request to terminate the logical
// session.
func (t *StreamableClientTransport) Connect(ctx context.Context) (Connection, error) {
	client := t.opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	reconnOpts := t.opts.ReconnectOptions
	if reconnOpts == nil {
		reconnOpts = DefaultReconnectOptions
	}
	// Create a new cancellable context that will manage the connection's lifecycle.
	// This is crucial for cleanly shutting down the background SSE listener by
	// cancelling its blocking network operations, which prevents hangs on exit.
	connCtx, cancel := context.WithCancel(context.Background())
	conn := &streamableClientConn{
		url:              t.url,
		client:           client,
		incoming:         make(chan []byte, 100),
		done:             make(chan struct{}),
		ReconnectOptions: reconnOpts,
		ctx:              connCtx,
		cancel:           cancel,
		failed:           make(chan struct{}),
	}
	return conn, nil
}

type streamableClientConn struct {
	url              string
	ReconnectOptions *StreamableReconnectOptions
	client           *http.Client
	ctx              context.Context
	cancel           context.CancelFunc
	incoming         chan []byte

	// Guard calls to Close, as it may be called multiple times.
	closeOnce sync.Once
	closeErr  error
	done      chan struct{} // signal graceful termination

	// Logical reads are distributed across multiple http requests. Whenever any
	// of them fails to process their response, we must break the connection, by
	// failing the pending Read.
	//
	// Achieve this by storing the failure message, and signalling when reads are
	// broken. See also [streamableClientConn.fail] and
	// [streamableClientConn.failure].
	failOnce sync.Once
	_failure error
	failed   chan struct{} // signal failure

	// Guard the initialization state.
	mu                sync.Mutex
	initializedResult *InitializeResult
	sessionID         string
}

func (c *streamableClientConn) initialized(res *InitializeResult) {
	c.mu.Lock()
	c.initializedResult = res
	c.mu.Unlock()

	// Start the persistent SSE listener as soon as we have the initialized
	// result.
	//
	// § 2.2: The client MAY issue an HTTP GET to the MCP endpoint. This can be
	// used to open an SSE stream, allowing the server to communicate to the
	// client, without the client first sending data via HTTP POST.
	//
	// We have to wait for initialized, because until we've received
	// initialized, we don't know whether the server requires a sessionID.
	//
	// § 2.5: A server using the Streamable HTTP transport MAY assign a session
	// ID at initialization time, by including it in an Mcp-Session-Id header
	// on the HTTP response containing the InitializeResult.
	go c.handleSSE(nil, true)
}

// fail handles an asynchronous error while reading.
//
// If err is non-nil, it is terminal, and subsequent (or pending) Reads will
// fail.
func (c *streamableClientConn) fail(err error) {
	if err != nil {
		c.failOnce.Do(func() {
			c._failure = err
			close(c.failed)
		})
	}
}

func (c *streamableClientConn) failure() error {
	select {
	case <-c.failed:
		return c._failure
	default:
		return nil
	}
}

func (c *streamableClientConn) SessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID
}

// Read implements the [Connection] interface.
func (c *streamableClientConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	if err := c.failure(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.failed:
		return nil, c.failure()
	case <-c.done:
		return nil, io.EOF
	case data := <-c.incoming:
		return jsonrpc2.DecodeMessage(data)
	}
}

// Write implements the [Connection] interface.
func (c *streamableClientConn) Write(ctx context.Context, msg jsonrpc.Message) error {
	if err := c.failure(); err != nil {
		return err
	}

	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	c.setMCPHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// TODO: do a best effort read of the body here, and format it in the error.
		resp.Body.Close()
		return fmt.Errorf("broken session: %v", resp.Status)
	}

	if sessionID := resp.Header.Get(sessionIDHeader); sessionID != "" {
		c.mu.Lock()
		hadSessionID := c.sessionID
		if hadSessionID == "" {
			c.sessionID = sessionID
		}
		c.mu.Unlock()
		if hadSessionID != "" && hadSessionID != sessionID {
			resp.Body.Close()
			return fmt.Errorf("mismatching session IDs %q and %q", hadSessionID, sessionID)
		}
	}
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusAccepted {
		resp.Body.Close()
		return nil
	}

	switch ct := resp.Header.Get("Content-Type"); ct {
	case "application/json":
		go c.handleJSON(resp)

	case "text/event-stream":
		go c.handleSSE(resp, false)

	default:
		resp.Body.Close()
		return fmt.Errorf("unsupported content type %q", ct)
	}
	return nil
}

func (c *streamableClientConn) setMCPHeaders(req *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.initializedResult != nil {
		req.Header.Set(protocolVersionHeader, c.initializedResult.ProtocolVersion)
	}
	if c.sessionID != "" {
		req.Header.Set(sessionIDHeader, c.sessionID)
	}
}

func (c *streamableClientConn) handleJSON(resp *http.Response) {
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		c.fail(err)
		return
	}
	select {
	case c.incoming <- body:
	case <-c.done:
		// The connection was closed by the client; exit gracefully.
	}
}

// handleSSE manages the lifecycle of an SSE connection. It can be either
// persistent (for the main GET listener) or temporary (for a POST response).
func (c *streamableClientConn) handleSSE(initialResp *http.Response, persistent bool) {
	resp := initialResp
	var lastEventID string
	for {
		eventID, clientClosed := c.processStream(resp)
		lastEventID = eventID

		// If the connection was closed by the client, we're done.
		if clientClosed {
			return
		}
		// If the stream has ended, then do not reconnect if the stream is
		// temporary (POST initiated SSE).
		if lastEventID == "" && !persistent {
			return
		}

		// The stream was interrupted or ended by the server. Attempt to reconnect.
		newResp, err := c.reconnect(lastEventID)
		if err != nil {
			// All reconnection attempts failed: fail the connection.
			c.fail(err)
			return
		}

		// Reconnection was successful. Continue the loop with the new response.
		resp = newResp
	}
}

// processStream reads from a single response body, sending events to the
// incoming channel. It returns the ID of the last processed event and a flag
// indicating if the connection was closed by the client. If resp is nil, it
// returns "", false.
func (c *streamableClientConn) processStream(resp *http.Response) (lastEventID string, clientClosed bool) {
	if resp == nil {
		// TODO(rfindley): avoid this special handling.
		return "", false
	}

	defer resp.Body.Close()
	for evt, err := range scanEvents(resp.Body) {
		if err != nil {
			return lastEventID, false
		}

		if evt.ID != "" {
			lastEventID = evt.ID
		}

		select {
		case c.incoming <- evt.Data:
		case <-c.done:
			// The connection was closed by the client; exit gracefully.
			return "", true
		}
	}
	// The loop finished without an error, indicating the server closed the stream.
	return "", false
}

// reconnect handles the logic of retrying a connection with an exponential
// backoff strategy. It returns a new, valid HTTP response if successful, or
// an error if all retries are exhausted.
func (c *streamableClientConn) reconnect(lastEventID string) (*http.Response, error) {
	var finalErr error

	for attempt := 0; attempt < c.ReconnectOptions.MaxRetries; attempt++ {
		select {
		case <-c.done:
			return nil, fmt.Errorf("connection closed by client during reconnect")
		case <-time.After(calculateReconnectDelay(c.ReconnectOptions, attempt)):
			resp, err := c.establishSSE(lastEventID)
			if err != nil {
				finalErr = err // Store the error and try again.
				continue
			}

			if !isResumable(resp) {
				// The server indicated we should not continue.
				resp.Body.Close()
				return nil, fmt.Errorf("reconnection failed with unresumable status: %s", resp.Status)
			}

			return resp, nil
		}
	}
	// If the loop completes, all retries have failed.
	if finalErr != nil {
		return nil, fmt.Errorf("connection failed after %d attempts: %w", c.ReconnectOptions.MaxRetries, finalErr)
	}
	return nil, fmt.Errorf("connection failed after %d attempts", c.ReconnectOptions.MaxRetries)
}

// isResumable checks if an HTTP response indicates a valid SSE stream that can be processed.
func isResumable(resp *http.Response) bool {
	// Per the spec, a 405 response means the server doesn't support SSE streams at this endpoint.
	if resp.StatusCode == http.StatusMethodNotAllowed {
		return false
	}

	return strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
}

// Close implements the [Connection] interface.
func (c *streamableClientConn) Close() error {
	c.closeOnce.Do(func() {
		// Cancel any hanging network requests.
		c.cancel()
		close(c.done)

		req, err := http.NewRequest(http.MethodDelete, c.url, nil)
		if err != nil {
			c.closeErr = err
		} else {
			c.setMCPHeaders(req)
			if _, err := c.client.Do(req); err != nil {
				c.closeErr = err
			}
		}
	})
	return c.closeErr
}

// establishSSE establishes the persistent SSE listening stream.
// It is used for reconnect attempts using the Last-Event-ID header to
// resume a broken stream where it left off.
func (c *streamableClientConn) establishSSE(lastEventID string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	c.setMCPHeaders(req)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	req.Header.Set("Accept", "text/event-stream")

	return c.client.Do(req)
}

// calculateReconnectDelay calculates a delay using exponential backoff with full jitter.
func calculateReconnectDelay(opts *StreamableReconnectOptions, attempt int) time.Duration {
	// Calculate the exponential backoff using the grow factor.
	backoffDuration := time.Duration(float64(opts.initialDelay) * math.Pow(opts.growFactor, float64(attempt)))
	// Cap the backoffDuration at maxDelay.
	backoffDuration = min(backoffDuration, opts.maxDelay)

	// Use a full jitter using backoffDuration
	jitter := rand.N(backoffDuration)

	return backoffDuration + jitter
}
