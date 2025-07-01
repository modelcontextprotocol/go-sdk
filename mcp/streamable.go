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
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
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
	// TODO(rfindley): support configurable session ID generation and event
	// store, session retention, and event retention.
}

// NewStreamableHTTPHandler returns a new [StreamableHTTPHandler].
//
// The getServer function is used to create or look up servers for new
// sessions. It is OK for getServer to return the same server multiple times.
func NewStreamableHTTPHandler(getServer func(*http.Request) *Server, opts *StreamableHTTPOptions) *StreamableHTTPHandler {
	return &StreamableHTTPHandler{
		getServer: getServer,
		sessions:  make(map[string]*StreamableServerTransport),
	}
}

// closeAll closes all ongoing sessions.
//
// TODO(rfindley): investigate the best API for callers to configure their
// session lifecycle.
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
	if id := req.Header.Get("Mcp-Session-Id"); id != "" {
		h.sessionsMu.Lock()
		session = h.sessions[id]
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
		delete(h.sessions, session.id)
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
		s := NewStreamableServerTransport(randText())
		server := h.getServer(req)
		// Pass req.Context() here, to allow middleware to add context values.
		// The context is detached in the jsonrpc2 library when handling the
		// long-running stream.
		if _, err := server.Connect(req.Context(), s); err != nil {
			http.Error(w, "failed connection", http.StatusInternalServerError)
			return
		}
		h.sessionsMu.Lock()
		h.sessions[s.id] = s
		h.sessionsMu.Unlock()
		session = s
	}

	session.ServeHTTP(w, req)
}

// NewStreamableServerTransport returns a new [StreamableServerTransport] with
// the given session ID.
//
// A StreamableServerTransport implements the server-side of the streamable
// transport.
//
// TODO(rfindley): consider adding options here, to configure event storage
// policy.
func NewStreamableServerTransport(sessionID string) *StreamableServerTransport {
	return &StreamableServerTransport{
		id:               sessionID,
		incoming:         make(chan JSONRPCMessage, 10),
		done:             make(chan struct{}),
		outgoingMessages: make(map[streamID][]*streamableMsg),
		signals:          make(map[streamID]chan struct{}),
		requestStreams:   make(map[JSONRPCID]streamID),
		streamRequests:   make(map[streamID]map[JSONRPCID]struct{}),
	}
}

func (t *StreamableServerTransport) SessionID() string {
	return t.id
}

// A StreamableServerTransport implements the [Transport] interface for a
// single session.
type StreamableServerTransport struct {
	nextStreamID atomic.Int64 // incrementing next stream ID

	id       string
	incoming chan JSONRPCMessage // messages from the client to the server

	mu sync.Mutex

	// Sessions are closed exactly once.
	isDone bool
	done   chan struct{}

	// Sessions can have multiple logical connections, corresponding to HTTP
	// requests. Additionally, logical sessions may be resumed by subsequent HTTP
	// requests, when the session is terminated unexpectedly.
	//
	// Therefore, we use a logical connection ID to key the connection state, and
	// perform the accounting described below when incoming HTTP requests are
	// handled.
	//
	// The accounting is complicated. It is tempting to merge some of the maps
	// below, but they each have different lifecycles, as indicated by Lifecycle:
	// comments.
	//
	// TODO(rfindley): simplify.

	// outgoingMessages is the collection of outgoingMessages messages, keyed by the logical
	// stream ID where they should be delivered.
	//
	// streamID 0 is used for messages that don't correlate with an incoming
	// request.
	//
	// Lifecycle: outgoingMessages persists for the duration of the session.
	//
	// TODO(rfindley): garbage collect this data. For now, we save all outgoingMessages
	// messages for the lifespan of the transport.
	outgoingMessages map[streamID][]*streamableMsg

	// signals maps a logical stream ID to a 1-buffered channel, owned by an
	// incoming HTTP request, that signals that there are messages available to
	// write into the HTTP response. Signals guarantees that at most one HTTP
	// response can receive messages for a logical stream. After claiming
	// the stream, incoming requests should read from outgoing, to ensure
	// that no new messages are missed.
	//
	// Lifecycle: signals persists for the duration of an HTTP POST or GET
	// request for the given streamID.
	signals map[streamID]chan struct{}

	// requestStreams maps incoming requests to their logical stream ID.
	//
	// Lifecycle: requestStreams persists for the duration of the session.
	//
	// TODO(rfindley): clean up once requests are handled.
	requestStreams map[JSONRPCID]streamID

	// outstandingRequests tracks the set of unanswered incoming RPCs for each logical
	// stream.
	//
	// When the server has responded to each request, the stream should be
	// closed.
	//
	// Lifecycle: outstandingRequests values persist as until the requests have been
	// replied to by the server. Notably, NOT until they are sent to an HTTP
	// response, as delivery is not guaranteed.
	streamRequests map[streamID]map[JSONRPCID]struct{}
}

type streamID int64

// a streamableMsg is an SSE event with an index into its logical stream.
type streamableMsg struct {
	idx   int
	event event
}

// Connect implements the [Transport] interface.
//
// TODO(rfindley): Connect should return a new object.
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
//  3. Add a `func ForRequest(context.Context) JSONRPCID` accessor that lets
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

func (t *StreamableServerTransport) serveGET(w http.ResponseWriter, req *http.Request) {
	// connID 0 corresponds to the default GET request.
	id, nextIdx := streamID(0), 0
	if len(req.Header.Values("Last-Event-ID")) > 0 {
		eid := req.Header.Get("Last-Event-ID")
		var ok bool
		id, nextIdx, ok = parseEventID(eid)
		if !ok {
			http.Error(w, fmt.Sprintf("malformed Last-Event-ID %q", eid), http.StatusBadRequest)
			return
		}
		nextIdx++
	}

	t.mu.Lock()
	if _, ok := t.signals[id]; ok {
		http.Error(w, "stream ID conflicts with ongoing stream", http.StatusBadRequest)
		t.mu.Unlock()
		return
	}
	signal := make(chan struct{}, 1)
	t.signals[id] = signal
	t.mu.Unlock()

	t.streamResponse(w, req, id, nextIdx, signal)
}

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
	incoming, _, err := readBatch(body)
	if err != nil {
		http.Error(w, fmt.Sprintf("malformed payload: %v", err), http.StatusBadRequest)
		return
	}
	requests := make(map[JSONRPCID]struct{})
	for _, msg := range incoming {
		if req, ok := msg.(*JSONRPCRequest); ok && req.ID.IsValid() {
			requests[req.ID] = struct{}{}
		}
	}

	// Update accounting for this request.
	id := streamID(t.nextStreamID.Add(1))
	signal := make(chan struct{}, 1)
	t.mu.Lock()
	if len(requests) > 0 {
		t.streamRequests[id] = make(map[JSONRPCID]struct{})
	}
	for reqID := range requests {
		t.requestStreams[reqID] = id
		t.streamRequests[id][reqID] = struct{}{}
	}
	t.signals[id] = signal
	t.mu.Unlock()

	// Publish incoming messages.
	for _, msg := range incoming {
		t.incoming <- msg
	}

	// TODO(rfindley): consider optimizing for a single incoming request, by
	// responding with application/json when there is only a single message in
	// the response.
	t.streamResponse(w, req, id, 0, signal)
}

func (t *StreamableServerTransport) streamResponse(w http.ResponseWriter, req *http.Request, id streamID, nextIndex int, signal chan struct{}) {
	defer func() {
		t.mu.Lock()
		delete(t.signals, id)
		t.mu.Unlock()
	}()

	// Stream resumption: adjust outgoing index based on what the user says
	// they've received.
	if nextIndex > 0 {
		t.mu.Lock()
		// Clamp nextIndex to outgoing messages.
		outgoing := t.outgoingMessages[id]
		if nextIndex > len(outgoing) {
			nextIndex = len(outgoing)
		}
		t.mu.Unlock()
	}

	w.Header().Set("Mcp-Session-Id", t.id)
	w.Header().Set("Content-Type", "text/event-stream") // Accept checked in [StreamableHTTPHandler]
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")

	writes := 0
stream:
	for {
		// Send outgoing messages
		t.mu.Lock()
		outgoing := t.outgoingMessages[id][nextIndex:]
		t.mu.Unlock()

		for _, msg := range outgoing {
			if _, err := writeEvent(w, msg.event); err != nil {
				// Connection closed or broken.
				return
			}
			writes++
			nextIndex++
		}

		t.mu.Lock()
		nOutstanding := len(t.streamRequests[id])
		nOutgoing := len(t.outgoingMessages[id])
		t.mu.Unlock()
		// If all requests have been handled and replied to, we can terminate this
		// connection. However, in the case of a sequencing violation from the server
		// (a send on the request context after the request has been handled), we
		// loop until we've written all messages.
		//
		// TODO(rfindley): should we instead refuse to send messages after the last
		// response? Decide, write a test, and change the behavior.
		if nextIndex < nOutgoing {
			continue // more to send
		}
		if req.Method == http.MethodPost && nOutstanding == 0 {
			if writes == 0 {
				// Spec: If the server accepts the input, the server MUST return HTTP
				// status code 202 Accepted with no body.
				w.WriteHeader(http.StatusAccepted)
			}
			return
		}

		select {
		case <-signal:
		case <-t.done:
			if writes == 0 {
				http.Error(w, "session terminated", http.StatusGone)
			}
			break stream
		case <-req.Context().Done():
			if writes == 0 {
				w.WriteHeader(http.StatusNoContent)
			}
			break stream
		}
	}
}

// Event IDs: encode both the logical connection ID and the index, as
// <streamID>_<idx>, to be consistent with the typescript implementation.

// formatEventID returns the event ID to use for the logical connection ID
// streamID and message index idx.
//
// See also [parseEventID].
func formatEventID(sid streamID, idx int) string {
	return fmt.Sprintf("%d_%d", sid, idx)
}

// parseEventID parses a Last-Event-ID value into a logical stream id and
// index.
//
// See also [formatEventID].
func parseEventID(eventID string) (sid streamID, idx int, ok bool) {
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
	return streamID(stream), idx, true
}

// Read implements the [Connection] interface.
func (t *StreamableServerTransport) Read(ctx context.Context) (JSONRPCMessage, error) {
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
func (t *StreamableServerTransport) Write(ctx context.Context, msg JSONRPCMessage) error {
	// Find the incoming request that this write relates to, if any.
	var forRequest, replyTo JSONRPCID
	if resp, ok := msg.(*JSONRPCResponse); ok {
		// If the message is a response, it relates to its request (of course).
		forRequest = resp.ID
		replyTo = resp.ID
	} else {
		// Otherwise, we check to see if it request was made in the context of an
		// ongoing request. This may not be the case if the request way made with
		// an unrelated context.
		if v := ctx.Value(idContextKey{}); v != nil {
			forRequest = v.(JSONRPCID)
		}
	}

	// Find the logical connection corresponding to this request.
	//
	// For messages sent outside of a request context, this is the default
	// connection 0.
	var forConn streamID
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
		return fmt.Errorf("session is closed") // TODO: should this be EOF?
	}

	if _, ok := t.streamRequests[forConn]; !ok && forConn != 0 {
		// No outstanding requests for this connection, which means it is logically
		// done. This is a sequencing violation from the server, so we should report
		// a side-channel error here. Put the message on the general queue to avoid
		// dropping messages.
		forConn = 0
	}

	idx := len(t.outgoingMessages[forConn])
	t.outgoingMessages[forConn] = append(t.outgoingMessages[forConn], &streamableMsg{
		idx: idx,
		event: event{
			name: "message",
			id:   formatEventID(forConn, idx),
			data: data,
		},
	})
	if replyTo.IsValid() {
		// Once we've put the reply on the queue, it's no longer outstanding.
		delete(t.streamRequests[forConn], replyTo)
		if len(t.streamRequests[forConn]) == 0 {
			delete(t.streamRequests, forConn)
		}
	}

	// Signal work.
	if c, ok := t.signals[forConn]; ok {
		select {
		case c <- struct{}{}:
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
	}
	return nil
}

// A StreamableClientTransport is a [Transport] that can communicate with an MCP
// endpoint serving the streamable HTTP transport defined by the 2025-03-26
// version of the spec.
//
// TODO(rfindley): support retries and resumption tokens.
type StreamableClientTransport struct {
	url  string
	opts StreamableClientTransportOptions
}

// StreamableClientTransportOptions provides options for the
// [NewStreamableClientTransport] constructor.
type StreamableClientTransportOptions struct {
	// HTTPClient is the client to use for making HTTP requests. If nil,
	// http.DefaultClient is used.
	HTTPClient *http.Client
	// MaxRetries specifies the maximum number of retries for sending a message
	// or re-establishing a hanging GET connection. If 0, no retries are performed
	// beyond the initial attempt.
	MaxRetries int

	// InitialBackoff is the initial duration to wait before the first retry
	// attempt. Subsequent retries use exponential backoff. If 0, a default
	// of 1 second is used.
	InitialBackoff time.Duration
}

// NewStreamableClientTransport returns a new client transport that connects to
// the streamable HTTP server at the provided URL.
func NewStreamableClientTransport(url string, opts *StreamableClientTransportOptions) *StreamableClientTransport {
	t := &StreamableClientTransport{url: url}
	if opts != nil {
		t.opts = *opts
	} else {
		t.opts = StreamableClientTransportOptions{}
	}

	// Set default initial backoff if not specified.
	if t.opts.InitialBackoff == 0 {
		t.opts.InitialBackoff = time.Second
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
	conn := &streamableClientConn{
		url:             t.url,
		client:          client,
		incoming:        make(chan []byte, 100),
		done:            make(chan struct{}),
		pendingMessages: make(chan JSONRPCMessage, 100), // Buffer pending messages
		maxRetries:      t.opts.MaxRetries,
		initialBackoff:  t.opts.InitialBackoff,
		randSource:      rand.New(rand.NewSource(time.Now().UnixNano())), // Seed for jitter
	}
	conn.sessionID.Store("")

	// Start the goroutines that handle sending messages and receiving SSE events.
	go conn.startMessageWriter()
	go conn.startEventStreamReceiver()

	return conn, nil
}

type streamableClientConn struct {
	url string
	// sessionID stores the current session ID.
	sessionID atomic.Value
	client    *http.Client
	incoming  chan []byte
	done      chan struct{}

	closeOnce sync.Once
	closeErr  error

	mu sync.Mutex // Protects lastEventID and err
	// lastEventID stores the ID of the last successfully processed SSE event,
	// used for resuming the stream.
	lastEventID string
	// bodies map[*http.Response]io.Closer
	// err stores the last error that caused the connection to be deemed unhealthy.
	err error

	// pendingMessages is a buffered channel for messages waiting to be sent.
	pendingMessages chan JSONRPCMessage

	// Retry configuration
	maxRetries     int
	initialBackoff time.Duration
	randSource     *rand.Rand // For adding jitter to backoff

	// cancelHangingGet is a context.CancelFunc for the currently active
	// hanging GET request. Used to cancel the request if the connection needs
	// to be closed or a new hanging GET is initiated.
	cancelHangingGet context.CancelFunc
}

func (c *streamableClientConn) SessionID() string {
	return c.sessionID.Load().(string)
}

// Read implements the [Connection] interface.
func (s *streamableClientConn) Read(ctx context.Context) (JSONRPCMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.err != nil {
			return nil, s.err // Return explicit error if connection closed due to error
		}
		return nil, io.EOF
	case data := <-s.incoming:
		return jsonrpc2.DecodeMessage(data)
	}
}

// Write implements the [Connection] interface by enqueuing the message
// for an asynchronous send operation. The actual sending, including retries,
// is handled by the startMessageWriter goroutine.
func (s *streamableClientConn) Write(ctx context.Context, msg JSONRPCMessage) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.err != nil {
			return s.err
		}
		return io.EOF // Connection closed
	case s.pendingMessages <- msg: // Enqueue the message for sending
		return nil
	}
}

// startMessageWriter continuously sends messages from the pendingMessages channel,
// applying retry logic for transient errors.
func (s *streamableClientConn) startMessageWriter() {
	for {
		select {
		case <-s.done:
			return // Connection is closed
		case msg := <-s.pendingMessages:
			// Use a new context for each send attempt to allow individual retries to be cancelled
			// if the overall connection context is cancelled.
			// This context is cancelled by the inner goroutine once the send attempt (including retries) is done.
			ctx, cancel := context.WithCancel(context.Background())

			go func(msgToSend JSONRPCMessage) {
				defer cancel() // Ensure context is cancelled when this goroutine finishes

				currentSessionID := s.sessionID.Load().(string)
				var lastErr error
				for i := 0; i <= s.maxRetries; i++ {
					// Check if the main connection has been closed during retries
					select {
					case <-s.done:
						return
					case <-ctx.Done(): // Check if the individual send context was cancelled
						return
					default:
						// Continue
					}

					gotSessionID, sendErr := s.postMessage(ctx, currentSessionID, msgToSend)
					if sendErr == nil {
						// If sessionID was not set and we got one, update it.
						if currentSessionID == "" && gotSessionID != "" {
							s.sessionID.Store(gotSessionID)
						}
						// Undefined behavior when currentSessionID != gotSessionID
						return
					}

					lastErr = sendErr // Store the latest error
					if !isRetryable(sendErr) || i == s.maxRetries {
						break // Not a retryable error or max retries reached
					}

					// Apply exponential backoff with jitter
					backoffDuration := s.initialBackoff * time.Duration(1<<uint(i))
					jitter := time.Duration(s.randSource.Int63n(int64(backoffDuration / 2))) // Jitter up to half of backoff
					delay := backoffDuration + jitter

					select {
					case <-ctx.Done():
						return // Context cancelled during backoff
					case <-time.After(delay):
						// Continue to next retry attempt
					}
				}
				// If all retries fail, set the connection error and close it
				s.mu.Lock()
				s.err = fmt.Errorf("failed to send message after %d retries: %w", s.maxRetries, lastErr)
				s.mu.Unlock()
				s.Close() // Close the connection due to persistent send failure
			}(msg)
		}
	}
}

// postMessage sends a single JSON-RPC message via an HTTP POST request.
// It returns the session ID from the response header or an error.
func (s *streamableClientConn) postMessage(ctx context.Context, currentSessionID string, msg JSONRPCMessage) (string, error) {
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return "", fmt.Errorf("failed to encode message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("failed to create POST request: %w", err)
	}
	if currentSessionID != "" {
		req.Header.Set("Mcp-Session-Id", currentSessionID)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST request failed: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body) // Try to read body for more context
		resp.Body.Close()
		// Wrap the error with httpStatusError for easier status code checking
		return "", &httpStatusError{
			StatusCode: resp.StatusCode,
			Err:        fmt.Errorf("POST request returned unexpected status %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(bodyBytes))),
		}
	}

	newSessionID := resp.Header.Get("Mcp-Session-Id")
	if currentSessionID == "" && newSessionID == "" {
		resp.Body.Close()
		// This should ideally not happen if server correctly sets session ID on first POST.
		return "", fmt.Errorf("initial POST request did not return an Mcp-Session-Id")
	}
	if newSessionID == "" {
		// If the server didn't explicitly send a new one, assume the existing one is still valid.
		newSessionID = currentSessionID
	}

	if resp.Header.Get("Content-Type") == "text/event-stream" {
		go s.handleSSE(resp)
	} else {
		resp.Body.Close()
	}

	return newSessionID, nil
}

// startEventStreamReceiver continuously attempts to establish and maintain
// the hanging GET connection for receiving Server-Sent Events (SSE).
func (s *streamableClientConn) startEventStreamReceiver() {
	backoffDuration := s.initialBackoff
	retries := 0

	for {
		select {
		case <-s.done:
			return // Connection is closed.
		default:
			// Continue
		}

		sessionID := s.sessionID.Load().(string)
		if sessionID == "" {
			// Session ID not yet established (first POST hasn't completed).
			// Wait and retry.
			time.Sleep(100 * time.Millisecond) // Avoid busy-waiting
			continue
		}

		// Create a context for the current hanging GET request.
		ctx, cancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.cancelHangingGet = cancel  // Store cancel function to allow external cancellation
		lastEventID := s.lastEventID // Get the last processed event ID for replay
		s.mu.Unlock()

		// Perform the hanging GET request
		err := s.performHangingGet(ctx, sessionID, lastEventID)

		// Clean up after the hanging GET attempt
		s.mu.Lock()
		s.cancelHangingGet = nil // Clear the cancel function
		s.mu.Unlock()
		cancel() // Ensure the context for this specific GET is cancelled

		if err == nil {
			// Successful hanging GET, reset retry state
			retries = 0
			backoffDuration = s.initialBackoff
			// Loop immediately to re-establish connection if it closed gracefully
			continue
		}

		// Error occurred during hanging GET, check for retry
		if retries >= s.maxRetries {
			s.mu.Lock()
			s.err = fmt.Errorf("failed to maintain SSE connection after %d retries: %w", s.maxRetries, err)
			s.mu.Unlock()
			s.Close() // Close the connection if persistent failure
			return
		}

		// Apply exponential backoff with jitter
		delay := backoffDuration + time.Duration(s.randSource.Int63n(int64(backoffDuration/2)))
		select {
		case <-s.done:
			return // Connection closed during backoff
		case <-time.After(delay):
			retries++
			backoffDuration *= 2                  // Exponential increase
			if backoffDuration > 30*time.Second { // Cap backoff duration
				backoffDuration = 30 * time.Second
			}
		}
	}
}

// performHangingGet makes a single HTTP GET request for the SSE stream.
// It returns nil on graceful stream termination or an error on failure.
func (s *streamableClientConn) performHangingGet(ctx context.Context, sessionID, lastEventID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return fmt.Errorf("failed to create GET request: %w", err)
	}
	req.Header.Set("Mcp-Session-Id", sessionID)
	req.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID) // Replay from this event
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("GET request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		// Wrap the error with httpStatusError for easier status code checking
		return &httpStatusError{
			StatusCode: resp.StatusCode,
			Err:        fmt.Errorf("GET request returned unexpected status %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(bodyBytes))),
		}
	}

	// Handle the SSE stream from the response body.
	return s.handleSSE(resp)
}

// handleSSE processes Server-Sent Events from the provided HTTP response body.
// It pushes decoded messages to the incoming channel and updates the lastEventID.
func (s *streamableClientConn) handleSSE(resp *http.Response) error {
	defer resp.Body.Close()
	for evt, err := range scanEvents(resp.Body) {
		if err != nil {
			if err == io.EOF {
				return nil // Stream ended gracefully
			}
			return fmt.Errorf("error scanning SSE events: %w", err)
		}
		// Update lastEventID on successful event receipt, crucial for replayability
		if evt.id != "" {
			s.mu.Lock()
			s.lastEventID = evt.id
			s.mu.Unlock()
		}
		select {
		case s.incoming <- evt.data:
			// Message successfully sent to incoming channel
		case <-s.done:
			// Connection closed while trying to send incoming message
			return io.EOF
		}
	}
	return nil // Stream finished without error
}

// isRetryable checks if a given error indicates a transient condition
// that warrants a retry.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check if the error is an httpStatusError and if its status code is retryable.
	var httpErr *httpStatusError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusRequestTimeout, // 408
			http.StatusTooEarly,            // 425
			http.StatusTooManyRequests,     // 429
			http.StatusInternalServerError, // 500
			http.StatusBadGateway,          // 502
			http.StatusServiceUnavailable,  // 503
			http.StatusGatewayTimeout:      // 504
			return true
		default:
			return false // Non-retryable HTTP status code
		}
	}

	// Check for network-related errors
	if netErr, ok := err.(net.Error); ok {
		if netErr.Timeout() {
			return true // Retry on timeout errors
		}
	}

	// Context cancellation should be non-retryable if it's explicitly from the caller.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	return false // Default to not retry for unknown errors
}

// Close implements the [Connection] interface.
// It ensures that all background goroutines are stopped and
// sends a DELETE request to the server to terminate the logical session.
func (s *streamableClientConn) Close() error {
	s.closeOnce.Do(func() {
		close(s.done) // Signal all goroutines to stop

		// Cancel any ongoing hanging GET request
		s.mu.Lock()
		if s.cancelHangingGet != nil {
			s.cancelHangingGet()
		}
		s.mu.Unlock()
		close(s.pendingMessages)

		// Send DELETE request to terminate the session on the server
		sessionID := s.sessionID.Load().(string)
		if sessionID != "" {
			req, err := http.NewRequest(http.MethodDelete, s.url, nil)
			if err != nil {
				s.closeErr = fmt.Errorf("failed to create DELETE request: %w", err)
			} else {
				req.Header.Set("Mcp-Session-Id", sessionID)
				if _, err := s.client.Do(req); err != nil {
					// Log the error but don't prevent close, as session termination is best effort.
					s.closeErr = fmt.Errorf("failed to send DELETE request to terminate session: %w", err)
				}
			}
		}
	})
	return s.closeErr
}

// httpStatusError wraps an error and includes an HTTP status code.
type httpStatusError struct {
	StatusCode int
	Err        error
}

func (e *httpStatusError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("HTTP status %d: %v", e.StatusCode, e.Err)
	}
	return fmt.Sprintf("HTTP status %d", e.StatusCode)
}

func (e *httpStatusError) Unwrap() error {
	return e.Err
}
