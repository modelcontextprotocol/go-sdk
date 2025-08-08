// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

	sessionsMu sync.Mutex
	sessions   map[string]*StreamableServerTransport // keyed by IDs (from Mcp-Session-Id header)
}

// StreamableHTTPOptions is a placeholder options struct for future
// configuration of the StreamableHTTP handler.
type StreamableHTTPOptions struct {
	// TODO: support configurable session ID generation (?)
	// TODO: support session retention (?)
}

// NewStreamableHTTPHandler returns a new [StreamableHTTPHandler].
//
// The getServer function is used to create or look up servers for new
// sessions. It is OK for getServer to return the same server multiple times.
// If getServer returns nil, a 400 Bad Request will be served.
func NewStreamableHTTPHandler(getServer func(*http.Request) *Server, opts *StreamableHTTPOptions) *StreamableHTTPHandler {
	return &StreamableHTTPHandler{
		getServer: getServer,
		sessions:  make(map[string]*StreamableServerTransport),
	}
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
		s := NewStreamableServerTransport(randText(), nil)
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
		sessionID:       sessionID,
		incoming:        make(chan jsonrpc.Message, 10),
		done:            make(chan struct{}),
		streams:         make(map[StreamID]*stream),
		requestStreams:  make(map[jsonrpc.ID]StreamID),
		defaultStreamID: StreamID(randText()),
	}
	t.streams[t.defaultStreamID] = newStream(t.defaultStreamID)
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
	sessionID       string
	defaultStreamID StreamID
	opts            StreamableServerTransportOptions
	incoming        chan jsonrpc.Message // messages from the client to the server
	done            chan struct{}

	mu sync.Mutex
	// Sessions are closed exactly once.
	isDone bool

	// Sessions can have multiple logical connections, corresponding to HTTP
	// requests. Additionally, logical sessions may be resumed by subsequent HTTP
	// requests, when the session is terminated unexpectedly.
	//
	// Therefore, we use a logical connection ID to key the connection state, and
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
	// Lifecycle: requestStreams persists for the duration of the session.
	//
	// TODO(rfindley): clean up once requests are handled. See the TODO for streams
	// above.
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
	// defaultStreamID is used for messages that don't correlate with an incoming request.
	id StreamID

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

func newStream(id StreamID) *stream {
	return &stream{
		id:       id,
		requests: make(map[jsonrpc.ID]struct{}),
	}
}

func signalChanPtr() *chan struct{} {
	c := make(chan struct{}, 1)
	return &c
}

// A StreamID identifies a stream of SSE events. It is a random string
// 26 characters in length
// [ServerSession].
type StreamID string

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
	status := 0
	message := ""
	switch req.Method {
	case http.MethodGet:
		status, message = t.serveGET(w, req)
	case http.MethodPost:
		status, message = t.servePOST(w, req)
	default:
		// Should not be reached, as this is checked in StreamableHTTPHandler.ServeHTTP.
		w.Header().Set("Allow", "GET, POST")
		status = http.StatusMethodNotAllowed
		message = "unsupported method"
	}
	if status != 0 && status != http.StatusOK {
		http.Error(w, message, status)
	}
}

func (t *StreamableServerTransport) serveGET(w http.ResponseWriter, req *http.Request) (int, string) {
	// connID = t.defaultStreamID corresponds to the default GET request.
	id := t.defaultStreamID
	// By default, we haven't seen a last index. Since indices start at 0, we represent
	// that by -1. This is incremented just before each event is written, in streamResponse
	// around L407.
	lastIdx := -1
	if len(req.Header.Values("Last-Event-ID")) > 0 {
		eid := req.Header.Get("Last-Event-ID")
		var ok bool
		id, lastIdx, ok = parseEventID(eid)
		if !ok {
			return http.StatusBadRequest, fmt.Sprintf("malformed Last-Event-ID %q", eid)
		}
	}

	t.mu.Lock()
	stream, ok := t.streams[id]
	t.mu.Unlock()
	if !ok {
		return http.StatusBadRequest, "unknown stream"
	}
	if !stream.signal.CompareAndSwap(nil, signalChanPtr()) {
		// The CAS returned false, meaning that the comparison failed: stream.signal is not nil.
		return http.StatusBadRequest, "stream ID conflicts with ongoing stream"
	}
	return t.streamResponse(stream, w, req, lastIdx)
}

func (t *StreamableServerTransport) servePOST(w http.ResponseWriter, req *http.Request) (int, string) {
	if len(req.Header.Values("Last-Event-ID")) > 0 {
		return http.StatusBadRequest, "can't send Last-Event-ID for POST request"
	}

	// Read incoming messages.
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return http.StatusBadRequest, "failed to read body"
	}
	if len(body) == 0 {
		return http.StatusBadRequest, "POST requires a non-empty body"
	}
	incoming, _, err := readBatch(body)
	if err != nil {
		return http.StatusBadRequest, fmt.Sprintf("malformed payload: %v", err)
	}
	requests := make(map[jsonrpc.ID]struct{})
	for _, msg := range incoming {
		if req, ok := msg.(*jsonrpc.Request); ok {
			// Preemptively check that this is a valid request, so that we can fail
			// the HTTP request. If we didn't do this, a request with a bad method or
			// missing ID could be silently swallowed.
			if _, err := checkRequest(req, serverMethodInfos); err != nil {
				return http.StatusBadRequest, err.Error()
			}
			if req.ID.IsValid() {
				requests[req.ID] = struct{}{}
			}
		}
	}

	// Update accounting for this request.
	stream := newStream(StreamID(randText()))
	t.mu.Lock()
	t.streams[stream.id] = stream
	if len(requests) > 0 {
		stream.requests = make(map[jsonrpc.ID]struct{})
	}
	for reqID := range requests {
		t.requestStreams[reqID] = stream.id
		stream.requests[reqID] = struct{}{}
	}
	t.mu.Unlock()
	stream.signal.Store(signalChanPtr())

	// Publish incoming messages.
	for _, msg := range incoming {
		t.incoming <- msg
	}

	// TODO(rfindley): consider optimizing for a single incoming request, by
	// responding with application/json when there is only a single message in
	// the response.
	// (But how would we know there is only a single message? For example, couldn't
	//  a progress notification be sent before a response on the same context?)
	return t.streamResponse(stream, w, req, -1)
}

// lastIndex is the index of the last seen event if resuming, else -1.
func (t *StreamableServerTransport) streamResponse(stream *stream, w http.ResponseWriter, req *http.Request, lastIndex int) (int, string) {
	defer stream.signal.Store(nil)

	writes := 0

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

	w.Header().Set(sessionIDHeader, t.sessionID)
	w.Header().Set("Content-Type", "text/event-stream") // Accept checked in [StreamableHTTPHandler]
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")

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
				return status, err.Error()
			}
			// The iterator yields events beginning just after lastIndex, or it would have
			// yielded an error.
			if !write(data) {
				return 0, ""
			}
		}
	}

stream:
	// Repeatedly collect pending outgoing events and send them.
	for {
		t.mu.Lock()
		outgoing := stream.outgoing
		stream.outgoing = nil
		nOutstanding := len(stream.requests)
		t.mu.Unlock()

		for _, data := range outgoing {
			if err := t.opts.EventStore.Append(req.Context(), t.SessionID(), stream.id, data); err != nil {
				return http.StatusInternalServerError, err.Error()
			}
			if !write(data) {
				return 0, ""
			}
		}

		// If all requests have been handled and replied to, we should terminate this connection.
		// "After the JSON-RPC response has been sent, the server SHOULD close the SSE stream."
		// §6.4, https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#sending-messages-to-the-server
		// We only want to terminate POSTs, and GETs that are replaying. The general-purpose GET
		// (stream ID 0) will never have requests, and should remain open indefinitely.
		// TODO: implement the GET case.
		if req.Method == http.MethodPost && nOutstanding == 0 {
			if writes == 0 {
				// Spec: If the server accepts the input, the server MUST return HTTP
				// status code 202 Accepted with no body.
				w.WriteHeader(http.StatusAccepted)
			}
			return 0, ""
		}

		select {
		case <-*stream.signal.Load(): // there are new outgoing messages
			// return to top of loop
		case <-t.done: // session is closed
			if writes == 0 {
				return http.StatusGone, "session terminated"
			}
			break stream
		case <-req.Context().Done():
			if writes == 0 {
				w.WriteHeader(http.StatusNoContent)
			}
			break stream
		}
	}
	return 0, ""
}

// Event IDs: encode both the logical connection ID and the index, as
// <streamID>_<idx>, to be consistent with the typescript implementation.

// formatEventID returns the event ID to use for the logical connection ID
// streamID and message index idx.
//
// See also [parseEventID].
func formatEventID(sid StreamID, idx int) string {
	return fmt.Sprintf("%s_%d", sid, idx)
}

// parseEventID parses a Last-Event-ID value into a logical stream id and
// index.
//
// See also [formatEventID].
func parseEventID(eventID string) (sid StreamID, idx int, ok bool) {
	parts := strings.Split(eventID, "_")
	if len(parts) != 2 {
		return "", 0, false
	}
	stream := StreamID(parts[0])
	if len(stream) == 0 {
		return "", 0, false
	}
	idx, err := strconv.Atoi(parts[1])
	if err != nil || idx < 0 {
		return "", 0, false
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
		// ongoing request. This may not be the case if the request way made with
		// an unrelated context.
		if v := ctx.Value(idContextKey{}); v != nil {
			forRequest = v.(jsonrpc.ID)
		}
	}

	// Find the logical connection corresponding to this request.
	//
	// For messages sent outside of a request context, this is the default
	// connection.
	var forConn StreamID
	if forRequest.IsValid() {
		t.mu.Lock()
		forConn = t.requestStreams[forRequest]
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

	stream := t.streams[forConn]
	if forConn == "" {
		stream = t.streams[t.defaultStreamID]
	}
	if stream == nil {
		return fmt.Errorf("no stream with ID %s", forConn)
	}
	if len(stream.requests) == 0 && forConn != "" {
		// No outstanding requests for this connection, which means it is logically
		// done. This is a sequencing violation from the server, so we should report
		// a side-channel error here. Put the message on the general queue to avoid
		// dropping messages.
		stream = t.streams[t.defaultStreamID]
	}

	// TODO: if there is nothing to send these messages to (as would happen, for example, if forConn == ""
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
	}
	// Start the persistent SSE listener right away.
	// Section 2.2: The client MAY issue an HTTP GET to the MCP endpoint.
	// This can be used to open an SSE stream, allowing the server to
	// communicate to the client, without the client first sending data via HTTP POST.
	go conn.handleSSE(nil, true)

	return conn, nil
}

type streamableClientConn struct {
	url              string
	client           *http.Client
	incoming         chan []byte
	done             chan struct{}
	ReconnectOptions *StreamableReconnectOptions

	closeOnce sync.Once
	closeErr  error
	ctx       context.Context
	cancel    context.CancelFunc

	mu              sync.Mutex
	protocolVersion string
	_sessionID      string
	err             error
}

func (c *streamableClientConn) setProtocolVersion(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.protocolVersion = s
}

func (c *streamableClientConn) SessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c._sessionID
}

// Read implements the [Connection] interface.
func (s *streamableClientConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, io.EOF
	case data := <-s.incoming:
		return jsonrpc2.DecodeMessage(data)
	}
}

// Write implements the [Connection] interface.
func (s *streamableClientConn) Write(ctx context.Context, msg jsonrpc.Message) error {
	s.mu.Lock()
	if s.err != nil {
		s.mu.Unlock()
		return s.err
	}

	sessionID := s._sessionID
	if sessionID == "" {
		// Hold lock for the first request.
		defer s.mu.Unlock()
	} else {
		s.mu.Unlock()
	}

	gotSessionID, err := s.postMessage(ctx, sessionID, msg)
	if err != nil {
		if sessionID != "" {
			// unlocked; lock to set err
			s.mu.Lock()
			defer s.mu.Unlock()
		}
		if s.err != nil {
			s.err = err
		}
		return err
	}

	if sessionID == "" {
		// locked
		s._sessionID = gotSessionID
	}

	return nil
}

// postMessage POSTs msg to the server and reads the response.
// It returns the session ID from the response.
func (s *streamableClientConn) postMessage(ctx context.Context, sessionID string, msg jsonrpc.Message) (string, error) {
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	if s.protocolVersion != "" {
		req.Header.Set(protocolVersionHeader, s.protocolVersion)
	}
	if sessionID != "" {
		req.Header.Set(sessionIDHeader, sessionID)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// TODO: do a best effort read of the body here, and format it in the error.
		resp.Body.Close()
		return "", fmt.Errorf("broken session: %v", resp.Status)
	}

	sessionID = resp.Header.Get(sessionIDHeader)
	switch ct := resp.Header.Get("Content-Type"); ct {
	case "text/event-stream":
		// Section 2.1: The SSE stream is initiated after a POST.
		go s.handleSSE(resp, false)
	case "application/json":
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", err
		}
		select {
		case s.incoming <- body:
		case <-s.done:
			// The connection was closed by the client; exit gracefully.
		}
		return sessionID, nil
	default:
		resp.Body.Close()
		return "", fmt.Errorf("unsupported content type %q", ct)
	}
	return sessionID, nil
}

// handleSSE manages the lifecycle of an SSE connection. It can be either
// persistent (for the main GET listener) or temporary (for a POST response).
func (s *streamableClientConn) handleSSE(initialResp *http.Response, persistent bool) {
	resp := initialResp
	var lastEventID string
	for {
		eventID, clientClosed := s.processStream(resp)
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
		newResp, err := s.reconnect(lastEventID)
		if err != nil {
			// All reconnection attempts failed. Set the final error, close the
			// connection, and exit the goroutine.
			s.mu.Lock()
			s.err = err
			s.mu.Unlock()
			s.Close()
			return
		}

		// Reconnection was successful. Continue the loop with the new response.
		resp = newResp
	}
}

// processStream reads from a single response body, sending events to the
// incoming channel. It returns the ID of the last processed event, any error
// that occurred, and a flag indicating if the connection was closed by the client.
// If resp is nil, it returns "", false.
func (s *streamableClientConn) processStream(resp *http.Response) (lastEventID string, clientClosed bool) {
	if resp == nil {
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
		case s.incoming <- evt.Data:
		case <-s.done:
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
func (s *streamableClientConn) reconnect(lastEventID string) (*http.Response, error) {
	var finalErr error

	for attempt := 0; attempt < s.ReconnectOptions.MaxRetries; attempt++ {
		select {
		case <-s.done:
			return nil, fmt.Errorf("connection closed by client during reconnect")
		case <-time.After(calculateReconnectDelay(s.ReconnectOptions, attempt)):
			resp, err := s.establishSSE(lastEventID)
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
		return nil, fmt.Errorf("connection failed after %d attempts: %w", s.ReconnectOptions.MaxRetries, finalErr)
	}
	return nil, fmt.Errorf("connection failed after %d attempts", s.ReconnectOptions.MaxRetries)
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
func (s *streamableClientConn) Close() error {
	s.closeOnce.Do(func() {
		// Cancel any hanging network requests.
		s.cancel()
		close(s.done)

		req, err := http.NewRequest(http.MethodDelete, s.url, nil)
		if err != nil {
			s.closeErr = err
		} else {
			// TODO(jba): confirm that we don't need a lock here, or add locking.
			if s.protocolVersion != "" {
				req.Header.Set(protocolVersionHeader, s.protocolVersion)
			}
			req.Header.Set(sessionIDHeader, s._sessionID)
			if _, err := s.client.Do(req); err != nil {
				s.closeErr = err
			}
		}
	})
	return s.closeErr
}

// establishSSE establishes the persistent SSE listening stream.
// It is used for reconnect attempts using the Last-Event-ID header to
// resume a broken stream where it left off.
func (s *streamableClientConn) establishSSE(lastEventID string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s._sessionID != "" {
		req.Header.Set("Mcp-Session-Id", s._sessionID)
	}
	s.mu.Unlock()
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	req.Header.Set("Accept", "text/event-stream")

	return s.client.Do(req)
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
