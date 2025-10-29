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
	"log/slog"
	"maps"
	"math"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
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

	onTransportDeletion func(sessionID string) // for testing

	mu       sync.Mutex
	sessions map[string]*sessionInfo // keyed by session ID
}

type sessionInfo struct {
	session   *ServerSession
	transport *StreamableServerTransport

	// If timeout is set, automatically close the session after an idle period.
	timeout time.Duration
	timerMu sync.Mutex
	refs    int // reference count
	timer   *time.Timer
}

// startPOST signals that a POST request for this session is starting (which
// carries a client->server message), pausing the session timeout if it was
// running.
//
// TODO: we may want to also pause the timer when resuming non-standalone SSE
// streams, but that is tricy to implement. Clients should generally make
// keepalive pings if they want to keep the session live.
func (i *sessionInfo) startPOST() {
	if i.timeout <= 0 {
		return
	}

	i.timerMu.Lock()
	defer i.timerMu.Unlock()

	if i.timer == nil {
		return // timer stopped permanently
	}
	if i.refs == 0 {
		i.timer.Stop()
	}
	i.refs++
}

// endPOST sigals that a request for this session is ending, starting the
// timeout if there are no other requests running.
func (i *sessionInfo) endPOST() {
	if i.timeout <= 0 {
		return
	}

	i.timerMu.Lock()
	defer i.timerMu.Unlock()

	if i.timer == nil {
		return // timer stopped permanently
	}

	i.refs--
	assert(i.refs >= 0, "negative ref count")
	if i.refs == 0 {
		i.timer.Reset(i.timeout)
	}
}

// stopTimer stops the inactivity timer permanently.
func (i *sessionInfo) stopTimer() {
	i.timerMu.Lock()
	defer i.timerMu.Unlock()
	if i.timer != nil {
		i.timer.Stop()
		i.timer = nil
	}
}

// StreamableHTTPOptions configures the StreamableHTTPHandler.
type StreamableHTTPOptions struct {
	// Stateless controls whether the session is 'stateless'.
	//
	// A stateless server does not validate the Mcp-Session-Id header, and uses a
	// temporary session with default initialization parameters. Any
	// server->client request is rejected immediately as there's no way for the
	// client to respond. Server->Client notifications may reach the client if
	// they are made in the context of an incoming request, as described in the
	// documentation for [StreamableServerTransport].
	Stateless bool

	// TODO(#148): support session retention (?)

	// JSONResponse causes streamable responses to return application/json rather
	// than text/event-stream ([§2.1.5] of the spec).
	//
	// [§2.1.5]: https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#sending-messages-to-the-server
	JSONResponse bool

	// Logger specifies the logger to use.
	// If nil, do not log.
	Logger *slog.Logger

	// EventStore enables stream resumption.
	//
	// If set, EventStore will be used to persist stream events and replay them
	// upon stream resumption.
	EventStore EventStore

	// SessionTimeout configures a timeout for idle sessions.
	//
	// When sessions receive no new HTTP requests from the client for this
	// duration, they are automatically closed.
	//
	// If SessionTimeout is the zero value, idle sessions are never closed.
	SessionTimeout time.Duration
}

// NewStreamableHTTPHandler returns a new [StreamableHTTPHandler].
//
// The getServer function is used to create or look up servers for new
// sessions. It is OK for getServer to return the same server multiple times.
// If getServer returns nil, a 400 Bad Request will be served.
func NewStreamableHTTPHandler(getServer func(*http.Request) *Server, opts *StreamableHTTPOptions) *StreamableHTTPHandler {
	h := &StreamableHTTPHandler{
		getServer: getServer,
		sessions:  make(map[string]*sessionInfo),
	}
	if opts != nil {
		h.opts = *opts
	}

	if h.opts.Logger == nil { // ensure we have a logger
		h.opts.Logger = ensureLogger(nil)
	}

	return h
}

// closeAll closes all ongoing sessions, for tests.
//
// TODO(rfindley): investigate the best API for callers to configure their
// session lifecycle. (?)
//
// Should we allow passing in a session store? That would allow the handler to
// be stateless.
func (h *StreamableHTTPHandler) closeAll() {
	// TODO: if we ever expose this outside of tests, we'll need to do better
	// than simply collecting sessions while holding the lock: we need to prevent
	// new sessions from being added.
	//
	// Currently, sessions remove themselves from h.sessions when closed, so we
	// can't call Close while holding the lock.
	h.mu.Lock()
	sessionInfos := slices.Collect(maps.Values(h.sessions))
	h.sessions = nil
	h.mu.Unlock()
	for _, s := range sessionInfos {
		s.session.Close()
	}
}

func (h *StreamableHTTPHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Allow multiple 'Accept' headers.
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Accept#syntax
	accept := strings.Split(strings.Join(req.Header.Values("Accept"), ","), ",")
	var jsonOK, streamOK bool
	for _, c := range accept {
		switch strings.TrimSpace(c) {
		case "application/json", "application/*":
			jsonOK = true
		case "text/event-stream", "text/*":
			streamOK = true
		case "*/*":
			jsonOK = true
			streamOK = true
		}
	}

	if req.Method == http.MethodGet {
		if !streamOK {
			http.Error(w, "Accept must contain 'text/event-stream' for GET requests", http.StatusBadRequest)
			return
		}
	} else if (!jsonOK || !streamOK) && req.Method != http.MethodDelete { // TODO: consolidate with handling of http method below.
		http.Error(w, "Accept must contain both 'application/json' and 'text/event-stream'", http.StatusBadRequest)
		return
	}

	sessionID := req.Header.Get(sessionIDHeader)
	var sessInfo *sessionInfo
	if sessionID != "" {
		h.mu.Lock()
		sessInfo = h.sessions[sessionID]
		h.mu.Unlock()
		if sessInfo == nil && !h.opts.Stateless {
			// Unless we're in 'stateless' mode, which doesn't perform any Session-ID
			// validation, we require that the session ID matches a known session.
			//
			// In stateless mode, a temporary transport is be created below.
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
	}

	if req.Method == http.MethodDelete {
		if sessionID == "" {
			http.Error(w, "Bad Request: DELETE requires an Mcp-Session-Id header", http.StatusBadRequest)
			return
		}
		if sessInfo != nil { // sessInfo may be nil in stateless mode
			// Closing the session also removes it from h.sessions, due to the
			// onClose callback.
			sessInfo.session.Close()
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	switch req.Method {
	case http.MethodPost, http.MethodGet:
		if req.Method == http.MethodGet && (h.opts.Stateless || sessionID == "") {
			http.Error(w, "GET requires an active session", http.StatusMethodNotAllowed)
			return
		}
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "Method Not Allowed: streamable MCP servers support GET, POST, and DELETE requests", http.StatusMethodNotAllowed)
		return
	}

	// [§2.7] of the spec (2025-06-18) states:
	//
	// "If using HTTP, the client MUST include the MCP-Protocol-Version:
	// <protocol-version> HTTP header on all subsequent requests to the MCP
	// server, allowing the MCP server to respond based on the MCP protocol
	// version.
	//
	// For example: MCP-Protocol-Version: 2025-06-18
	// The protocol version sent by the client SHOULD be the one negotiated during
	// initialization.
	//
	// For backwards compatibility, if the server does not receive an
	// MCP-Protocol-Version header, and has no other way to identify the version -
	// for example, by relying on the protocol version negotiated during
	// initialization - the server SHOULD assume protocol version 2025-03-26.
	//
	// If the server receives a request with an invalid or unsupported
	// MCP-Protocol-Version, it MUST respond with 400 Bad Request."
	//
	// Since this wasn't present in the 2025-03-26 version of the spec, this
	// effectively means:
	//  1. IF the client provides a version header, it must be a supported
	//     version.
	//  2. In stateless mode, where we've lost the state of the initialize
	//     request, we assume that whatever the client tells us is the truth (or
	//     assume 2025-03-26 if the client doesn't say anything).
	//
	// This logic matches the typescript SDK.
	//
	// [§2.7]: https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#protocol-version-header
	protocolVersion := req.Header.Get(protocolVersionHeader)
	if protocolVersion == "" {
		protocolVersion = protocolVersion20250326
	}
	if !slices.Contains(supportedProtocolVersions, protocolVersion) {
		http.Error(w, fmt.Sprintf("Bad Request: Unsupported protocol version (supported versions: %s)", strings.Join(supportedProtocolVersions, ",")), http.StatusBadRequest)
		return
	}

	if sessInfo == nil {
		server := h.getServer(req)
		if server == nil {
			// The getServer argument to NewStreamableHTTPHandler returned nil.
			http.Error(w, "no server available", http.StatusBadRequest)
			return
		}
		if sessionID == "" {
			// In stateless mode, sessionID may be nonempty even if there's no
			// existing transport.
			sessionID = server.opts.GetSessionID()
		}
		transport := &StreamableServerTransport{
			SessionID:    sessionID,
			Stateless:    h.opts.Stateless,
			EventStore:   h.opts.EventStore,
			jsonResponse: h.opts.JSONResponse,
			logger:       h.opts.Logger,
		}

		// Sessions without a session ID are also stateless: there's no way to
		// address them.
		stateless := h.opts.Stateless || sessionID == ""
		// To support stateless mode, we initialize the session with a default
		// state, so that it doesn't reject subsequent requests.
		var connectOpts *ServerSessionOptions
		if stateless {
			// Peek at the body to see if it is initialize or initialized.
			// We want those to be handled as usual.
			var hasInitialize, hasInitialized bool
			{
				// TODO: verify that this allows protocol version negotiation for
				// stateless servers.
				body, err := io.ReadAll(req.Body)
				if err != nil {
					http.Error(w, "failed to read body", http.StatusInternalServerError)
					return
				}
				req.Body.Close()

				// Reset the body so that it can be read later.
				req.Body = io.NopCloser(bytes.NewBuffer(body))

				msgs, _, err := readBatch(body)
				if err == nil {
					for _, msg := range msgs {
						if req, ok := msg.(*jsonrpc.Request); ok {
							switch req.Method {
							case methodInitialize:
								hasInitialize = true
							case notificationInitialized:
								hasInitialized = true
							}
						}
					}
				}
			}

			// If we don't have InitializeParams or InitializedParams in the request,
			// set the initial state to a default value.
			state := new(ServerSessionState)
			if !hasInitialize {
				state.InitializeParams = &InitializeParams{
					ProtocolVersion: protocolVersion,
				}
			}
			if !hasInitialized {
				state.InitializedParams = new(InitializedParams)
			}
			state.LogLevel = "info"
			connectOpts = &ServerSessionOptions{
				State: state,
			}
		} else {
			// Cleanup is only required in stateful mode, as transportation is
			// not stored in the map otherwise.
			connectOpts = &ServerSessionOptions{
				onClose: func() {
					h.mu.Lock()
					defer h.mu.Unlock()
					if info, ok := h.sessions[transport.SessionID]; ok {
						info.stopTimer()
						delete(h.sessions, transport.SessionID)
						if h.onTransportDeletion != nil {
							h.onTransportDeletion(transport.SessionID)
						}
					}
				},
			}
		}

		// Pass req.Context() here, to allow middleware to add context values.
		// The context is detached in the jsonrpc2 library when handling the
		// long-running stream.
		session, err := server.Connect(req.Context(), transport, connectOpts)
		if err != nil {
			http.Error(w, "failed connection", http.StatusInternalServerError)
			return
		}
		sessInfo = &sessionInfo{
			session:   session,
			transport: transport,
		}

		if stateless {
			// Stateless mode: close the session when the request exits.
			defer session.Close() // close the fake session after handling the request
		} else {
			// Otherwise, save the transport so that it can be reused

			// Clean up the session when it times out.
			//
			// Note that the timer here may fire multiple times, but
			// sessInfo.session.Close is idempotent.
			if h.opts.SessionTimeout > 0 {
				sessInfo.timeout = h.opts.SessionTimeout
				sessInfo.timer = time.AfterFunc(sessInfo.timeout, func() {
					sessInfo.session.Close()
				})
			}
			h.mu.Lock()
			h.sessions[transport.SessionID] = sessInfo
			h.mu.Unlock()
			defer func() {
				// If initialization failed, clean up the session (#578).
				if session.InitializeParams() == nil {
					// Initialization failed.
					session.Close()
				}
			}()
		}
	}

	if req.Method == http.MethodPost {
		sessInfo.startPOST()
		defer sessInfo.endPOST()
	}

	sessInfo.transport.ServeHTTP(w, req)
}

// A StreamableServerTransport implements the server side of the MCP streamable
// transport.
//
// Each StreamableServerTransport must be connected (via [Server.Connect]) at
// most once, since [StreamableServerTransport.ServeHTTP] serves messages to
// the connected session.
//
// Reads from the streamable server connection receive messages from http POST
// requests from the client. Writes to the streamable server connection are
// sent either to the related stream, or to the standalone SSE stream,
// according to the following rules:
//   - JSON-RPC responses to incoming requests are always routed to the
//     appropriate HTTP response.
//   - Requests or notifications made with a context.Context value derived from
//     an incoming request handler, are routed to the HTTP response
//     corresponding to that request, unless it has already terminated, in
//     which case they are routed to the standalone SSE stream.
//   - Requests or notifications made with a detached context.Context value are
//     routed to the standalone SSE stream.
type StreamableServerTransport struct {
	// SessionID is the ID of this session.
	//
	// If SessionID is the empty string, this is a 'stateless' session, which has
	// limited ability to communicate with the client. Otherwise, the session ID
	// must be globally unique, that is, different from any other session ID
	// anywhere, past and future. (We recommend using a crypto random number
	// generator to produce one, as with [crypto/rand.Text].)
	SessionID string

	// Stateless controls whether the eventstore is 'Stateless'. Server sessions
	// connected to a stateless transport are disallowed from making outgoing
	// requests.
	//
	// See also [StreamableHTTPOptions.Stateless].
	Stateless bool

	// EventStore enables stream resumption.
	//
	// If set, EventStore will be used to persist stream events and replay them
	// upon stream resumption.
	EventStore EventStore

	// jsonResponse, if set, tells the server to prefer to respond to requests
	// using application/json responses rather than text/event-stream.
	//
	// Specifically, responses will be application/json whenever incoming POST
	// request contain only a single message. In this case, notifications or
	// requests made within the context of a server request will be sent to the
	// standalone SSE stream, if any.
	//
	// TODO(rfindley): jsonResponse should be exported, since
	// StreamableHTTPOptions.JSONResponse is exported, and we want to allow users
	// to write their own streamable HTTP handler.
	jsonResponse bool

	// optional logger provided through the [StreamableHTTPOptions.Logger].
	//
	// TODO(rfindley): logger should be exported, since we want to allow users
	// to write their own streamable HTTP handler.
	logger *slog.Logger

	// connection is non-nil if and only if the transport has been connected.
	connection *streamableServerConn
}

// Connect implements the [Transport] interface.
func (t *StreamableServerTransport) Connect(ctx context.Context) (Connection, error) {
	if t.connection != nil {
		return nil, fmt.Errorf("transport already connected")
	}
	t.connection = &streamableServerConn{
		sessionID:      t.SessionID,
		stateless:      t.Stateless,
		eventStore:     t.EventStore,
		jsonResponse:   t.jsonResponse,
		logger:         ensureLogger(t.logger), // see #556: must be non-nil
		incoming:       make(chan jsonrpc.Message, 10),
		done:           make(chan struct{}),
		streams:        make(map[string]*stream),
		requestStreams: make(map[jsonrpc.ID]string),
	}
	// Stream 0 corresponds to the standalone SSE stream.
	//
	// It is always text/event-stream, since it must carry arbitrarily many
	// messages.
	var err error
	t.connection.streams[""], err = t.connection.newStream(ctx, nil, "")
	if err != nil {
		return nil, err
	}
	return t.connection, nil
}

type streamableServerConn struct {
	sessionID    string
	stateless    bool
	jsonResponse bool
	eventStore   EventStore

	logger *slog.Logger

	incoming chan jsonrpc.Message // messages from the client to the server

	mu sync.Mutex // guards all fields below

	// Sessions are closed exactly once.
	isDone bool
	done   chan struct{}

	// Sessions can have multiple logical connections (which we call streams),
	// corresponding to HTTP requests. Additionally, streams may be resumed by
	// subsequent HTTP requests, when the HTTP connection is terminated
	// unexpectedly.
	//
	// Therefore, we use a logical stream ID to key the stream state, and
	// perform the accounting described below when incoming HTTP requests are
	// handled.

	// streams holds the logical streams for this session, keyed by their ID.
	//
	// Lifecycle: streams persist until all of their responses are received from
	// the server.
	streams map[string]*stream

	// requestStreams maps incoming requests to their logical stream ID.
	//
	// Lifecycle: requestStreams persist until their response is received.
	requestStreams map[jsonrpc.ID]string
}

func (c *streamableServerConn) SessionID() string {
	return c.sessionID
}

// A stream is a single logical stream of SSE events within a server session.
// A stream begins with a client request, or with a client GET that has
// no Last-Event-ID header.
//
// A stream ends only when its session ends; we cannot determine its end otherwise,
// since a client may send a GET with a Last-Event-ID that references the stream
// at any time.
type stream struct {
	// id is the logical ID for the stream, unique within a session.
	//
	// The standalone SSE stream has id "".
	id string

	// mu guards the fields below, as well as storage of new messages in the
	// connection's event store (if any).
	mu sync.Mutex

	// If non-nil, deliver writes data directly to the HTTP response.
	//
	// Only one HTTP response may receive messages at a given time. An active
	// HTTP connection acquires ownership of the stream by setting this field.
	deliver func(data []byte, final bool) error

	// streamRequests is the set of unanswered incoming requests for the stream.
	//
	// Requests are removed when their response has been received.
	requests map[jsonrpc.ID]struct{}
}

// doneLocked reports whether the stream is logically complete.
//
// s.mu must be held while calling this function.
func (s *stream) doneLocked() bool {
	return len(s.requests) == 0 && s.id != ""
}

func (c *streamableServerConn) newStream(ctx context.Context, requests map[jsonrpc.ID]struct{}, id string) (*stream, error) {
	if c.eventStore != nil {
		if err := c.eventStore.Open(ctx, c.sessionID, id); err != nil {
			return nil, err
		}
	}
	return &stream{
		id:       id,
		requests: requests,
	}, nil
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
	if t.connection == nil {
		http.Error(w, "transport not connected", http.StatusInternalServerError)
		return
	}
	switch req.Method {
	case http.MethodGet:
		t.connection.serveGET(w, req)
	case http.MethodPost:
		t.connection.servePOST(w, req)
	default:
		// Should not be reached, as this is checked in StreamableHTTPHandler.ServeHTTP.
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
		return
	}
}

// serveGET streams messages to a hanging http GET, with stream ID and last
// message parsed from the Last-Event-ID header.
//
// It returns an HTTP status code and error message.
func (c *streamableServerConn) serveGET(w http.ResponseWriter, req *http.Request) {
	// streamID "" corresponds to the default GET request.
	streamID := ""
	// By default, we haven't seen a last index. Since indices start at 0, we represent
	// that by -1. This is incremented just before each event is written.
	lastIdx := -1
	if len(req.Header.Values("Last-Event-ID")) > 0 {
		eid := req.Header.Get("Last-Event-ID")
		var ok bool
		streamID, lastIdx, ok = parseEventID(eid)
		if !ok {
			http.Error(w, fmt.Sprintf("malformed Last-Event-ID %q", eid), http.StatusBadRequest)
			return
		}
		if c.eventStore == nil {
			http.Error(w, "stream replay unsupported", http.StatusBadRequest)
			return
		}
	}

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	stream, done := c.acquireStream(ctx, w, streamID, &lastIdx)
	if stream == nil {
		return
	}
	// Release the stream when we're done.
	defer func() {
		stream.mu.Lock()
		stream.deliver = nil
		stream.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		// request cancelled
	case <-done:
		// request complete
	case <-c.done:
		// session closed
	}
}

// writeEvent writes an SSE event to w corresponding to the given stream, data, and index.
// lastIdx is incremented before writing, so that it continues to point to the index of the
// last event written to the stream.
func (c *streamableServerConn) writeEvent(w http.ResponseWriter, stream *stream, data []byte, lastIdx *int) error {
	*lastIdx++
	e := Event{
		Name: "message",
		Data: data,
	}
	if c.eventStore != nil {
		e.ID = formatEventID(stream.id, *lastIdx)
	}
	if _, err := writeEvent(w, e); err != nil {
		return err
	}
	return nil
}

// acquireStream acquires the stream and replays all events since lastIdx, if
// any, updating lastIdx accordingly. If non-nil, the resulting stream will be
// registered for receiving new messages, and the resulting done channel will
// be closed when all related messages have been delivered.
//
// If any errors occur, they will be written to w and the resulting stream will
// be nil. The resulting stream may also be nil if the stream is complete.
//
// Importantly, this function must hold the stream mutex until done replaying
// all messages, so that no delivery or storage of new messages occurs while
// the stream is still replaying.
func (c *streamableServerConn) acquireStream(ctx context.Context, w http.ResponseWriter, streamID string, lastIdx *int) (*stream, chan struct{}) {
	// if tempStream is set, the stream is done and we're just replaying messages.
	//
	// We record a temporary stream to claim exclusive replay rights.
	tempStream := false
	c.mu.Lock()
	s, ok := c.streams[streamID]
	if !ok {
		// The stream is logically done, but claim exclusive rights to replay it by
		// adding a temporary entry in the streams map.
		//
		// We create this entry with a non-nil deliver function, to ensure it isn't
		// claimed by another request before we lock it below.
		tempStream = true
		s = &stream{
			id:      streamID,
			deliver: func([]byte, bool) error { return nil },
		}
		c.streams[streamID] = s

		// Since this stream is transient, we must clean up after replaying.
		defer func() {
			c.mu.Lock()
			delete(c.streams, streamID)
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check that this stream wasn't claimed by another request.
	if !tempStream && s.deliver != nil {
		http.Error(w, "stream ID conflicts with ongoing stream", http.StatusConflict)
		return nil, nil
	}

	// Collect events to replay. Collect them all before writing, so that we
	// have an opportunity to set the HTTP status code on an error.
	//
	// As indicated above, we must do that while holding stream.mu, so that no
	// new messages are added to the eventstore until we've replayed all previous
	// messages, and registered our delivery function.
	var toReplay [][]byte
	if c.eventStore != nil {
		for data, err := range c.eventStore.After(ctx, c.SessionID(), s.id, *lastIdx) {
			if err != nil {
				// We can't replay events, perhaps because the underlying event store
				// has garbage collected its storage.
				//
				// We must be careful here: any 404 will signal to the client that the
				// *session* is not found, rather than the stream.
				//
				// 400 is not really accurate, but should at least have no side effects.
				// Other SDKs (typescript) do not have a mechanism for events to be purged.
				http.Error(w, "failed to replay events", http.StatusBadRequest)
				return nil, nil
			}
			toReplay = append(toReplay, data)
		}
	}

	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Content-Type", "text/event-stream") // Accept checked in [StreamableHTTPHandler]
	w.Header().Set("Connection", "keep-alive")

	if s.id == "" {
		// Issue #410: the standalone SSE stream is likely not to receive messages
		// for a long time. Ensure that headers are flushed.
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	for _, data := range toReplay {
		if err := c.writeEvent(w, s, data, lastIdx); err != nil {
			return nil, nil
		}
	}

	if tempStream || s.doneLocked() {
		// Nothing more to do.
		return nil, nil
	}

	// The stream is not done: register a delivery function before the stream is
	// unlocked, allowing the connection to write new events.
	done := make(chan struct{})
	s.deliver = func(data []byte, final bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := c.writeEvent(w, s, data, lastIdx)
		if final {
			close(done)
		}
		return err
	}
	return s, done
}

// servePOST handles an incoming message, and replies with either an outgoing
// message stream or single response object, depending on whether the
// jsonResponse option is set.
//
// It returns an HTTP status code and error message.
func (c *streamableServerConn) servePOST(w http.ResponseWriter, req *http.Request) {
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
	incoming, isBatch, err := readBatch(body)
	if err != nil {
		http.Error(w, fmt.Sprintf("malformed payload: %v", err), http.StatusBadRequest)
		return
	}

	protocolVersion := req.Header.Get(protocolVersionHeader)
	if protocolVersion == "" {
		protocolVersion = protocolVersion20250326
	}

	if isBatch && protocolVersion >= protocolVersion20250618 {
		http.Error(w, fmt.Sprintf("JSON-RPC batching is not supported in %s and later (request version: %s)", protocolVersion20250618, protocolVersion), http.StatusBadRequest)
		return
	}

	// TODO(rfindley): no tests fail if we reject batch JSON requests entirely.
	// We need to test this with older protocol versions.
	// if isBatch && c.jsonResponse {
	// 	http.Error(w, "server does not support batch requests", http.StatusBadRequest)
	// 	return
	// }

	calls := make(map[jsonrpc.ID]struct{})
	tokenInfo := auth.TokenInfoFromContext(req.Context())
	isInitialize := false
	for _, msg := range incoming {
		if jreq, ok := msg.(*jsonrpc.Request); ok {
			// Preemptively check that this is a valid request, so that we can fail
			// the HTTP request. If we didn't do this, a request with a bad method or
			// missing ID could be silently swallowed.
			if _, err := checkRequest(jreq, serverMethodInfos); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if jreq.Method == methodInitialize {
				isInitialize = true
			}
			jreq.Extra = &RequestExtra{
				TokenInfo: tokenInfo,
				Header:    req.Header,
			}
			if jreq.IsCall() {
				calls[jreq.ID] = struct{}{}
			}
		}
	}

	// If we don't have any calls, we can just publish the incoming messages and return.
	// No need to track a logical stream.
	if len(calls) == 0 {
		for _, msg := range incoming {
			select {
			case c.incoming <- msg:
			case <-c.done:
				// The session is closing. Since we haven't yet written any data to the
				// response, we can signal to the client that the session is gone.
				http.Error(w, "session is closing", http.StatusNotFound)
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Invariant: we have at least one call.
	//
	// Create a logical stream to track its responses.
	// Important: don't publish the incoming messages until the stream is
	// registered, as the server may attempt to respond to imcoming messages as
	// soon as they're published.
	stream, err := c.newStream(req.Context(), calls, randText())
	if err != nil {
		http.Error(w, fmt.Sprintf("storing stream: %v", err), http.StatusInternalServerError)
		return
	}

	// Set response headers. Accept was checked in [StreamableHTTPHandler].
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	if c.jsonResponse {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Connection", "keep-alive")
	}
	if c.sessionID != "" && isInitialize {
		w.Header().Set(sessionIDHeader, c.sessionID)
	}

	// Message delivery has two paths, depending on whether we're responding with JSON or
	// event stream.
	done := make(chan struct{}) // closed after the final response is written
	if c.jsonResponse {
		var msgs []json.RawMessage
		stream.deliver = func(data []byte, final bool) error {
			// Collect messages until we've received the final response.
			//
			// In recent protocol versions, there should only be one message as
			// batching is disabled, as checked above.
			msgs = append(msgs, data)
			if !final {
				return nil
			}
			defer close(done) // final response

			// Write either the JSON object corresponding to the one response, or a
			// JSON array corresponding to the batch response.
			var toWrite []byte
			if len(msgs) == 1 {
				toWrite = []byte(msgs[0])
			} else {
				var err error
				toWrite, err = json.Marshal(msgs)
				if err != nil {
					return err
				}
			}
			_, err = w.Write(toWrite)
			return err
		}
	} else {
		// Write events in the order we receive them.
		lastIndex := -1
		stream.deliver = func(data []byte, final bool) error {
			if final {
				defer close(done)
			}
			return c.writeEvent(w, stream, data, &lastIndex)
		}
	}

	// Release ownership of the stream by unsetting deliver.
	defer func() {
		stream.mu.Lock()
		// TODO(rfindley): if we have no event store, we should really cancel all
		// remaining requests here, since the client will never get the results.
		stream.deliver = nil
		stream.mu.Unlock()
	}()

	// The stream is now set up to deliver messages.
	//
	// Register it before publishing incoming messages.
	c.mu.Lock()
	c.streams[stream.id] = stream
	for reqID := range calls {
		c.requestStreams[reqID] = stream.id
	}
	c.mu.Unlock()

	// Publish incoming messages.
	for _, msg := range incoming {
		select {
		case c.incoming <- msg:
		// Note: don't select on req.Context().Done() here, since we've already
		// received the requests and may have already published a response message
		// or notification. The client could resume the stream.
		case <-c.done:
			// Session closed: we don't know if any data has been written, so it's
			// too late to write a status code here.
			return
		}
	}

	select {
	case <-req.Context().Done():
		// request cancelled
	case <-done:
		// request complete
	case <-c.done:
		// session is closed
	}
}

// Event IDs: encode both the logical connection ID and the index, as
// <streamID>_<idx>, to be consistent with the typescript implementation.

// formatEventID returns the event ID to use for the logical connection ID
// streamID and message index idx.
//
// See also [parseEventID].
func formatEventID(sid string, idx int) string {
	return fmt.Sprintf("%s_%d", sid, idx)
}

// parseEventID parses a Last-Event-ID value into a logical stream id and
// index.
//
// See also [formatEventID].
func parseEventID(eventID string) (streamID string, idx int, ok bool) {
	parts := strings.Split(eventID, "_")
	if len(parts) != 2 {
		return "", 0, false
	}
	streamID = parts[0]
	idx, err := strconv.Atoi(parts[1])
	if err != nil || idx < 0 {
		return "", 0, false
	}
	return streamID, idx, true
}

// Read implements the [Connection] interface.
func (c *streamableServerConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-c.incoming:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-c.done:
		return nil, io.EOF
	}
}

// Write implements the [Connection] interface.
func (c *streamableServerConn) Write(ctx context.Context, msg jsonrpc.Message) error {
	// Throughout this function, note that any error that wraps ErrRejected
	// indicates a does not cause the connection to break.
	//
	// Most errors don't break the connection: unlike a true bidirectional
	// stream, a failure to deliver to a stream is not an indication that the
	// logical session is broken.
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return err
	}

	if req, ok := msg.(*jsonrpc.Request); ok && req.ID.IsValid() && (c.stateless || c.sessionID == "") {
		// Requests aren't possible with stateless servers, or when there's no session ID.
		return fmt.Errorf("%w: stateless servers cannot make requests", jsonrpc2.ErrRejected)
	}

	// Find the incoming request that this write relates to, if any.
	var (
		relatedRequest jsonrpc.ID
		responseTo     jsonrpc.ID // if valid, the message is a response to this request
	)
	if resp, ok := msg.(*jsonrpc.Response); ok {
		// If the message is a response, it relates to its request (of course).
		relatedRequest = resp.ID
		responseTo = resp.ID
	} else {
		// Otherwise, we check to see if it request was made in the context of an
		// ongoing request. This may not be the case if the request was made with
		// an unrelated context.
		if v := ctx.Value(idContextKey{}); v != nil {
			relatedRequest = v.(jsonrpc.ID)
		}
	}

	// If the stream is application/json, but the message is not a response, we
	// must send it out of band to the standalone SSE stream.
	if c.jsonResponse && !responseTo.IsValid() {
		relatedRequest = jsonrpc.ID{}
	}

	// Write the message to the stream.
	var s *stream
	c.mu.Lock()
	if relatedRequest.IsValid() {
		if streamID, ok := c.requestStreams[relatedRequest]; ok {
			s = c.streams[streamID]
		}
	} else {
		s = c.streams[""] // standalone SSE stream
	}
	if responseTo.IsValid() {
		// Once we've responded to a request, disallow related messages by removing
		// the stream association. This also releases memory.
		delete(c.requestStreams, responseTo)
	}
	sessionClosed := c.isDone
	c.mu.Unlock()

	if s == nil {
		// The request was made in the context of an ongoing request, but that
		// request is complete.
		//
		// In the future, we could be less strict and allow the request to land on
		// the standalone SSE stream.
		return fmt.Errorf("%w: write to closed stream", jsonrpc2.ErrRejected)
	}
	if sessionClosed {
		return errors.New("session is closed")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doneLocked() {
		// It's possible that the stream was completed in between getting s above,
		// and acquiring the stream lock. In order to avoid acquiring s.mu while
		// holding c.mu, we check the terminal condition again.
		return fmt.Errorf("%w: write to closed stream", jsonrpc2.ErrRejected)
	}
	// Perform accounting on responses.
	if responseTo.IsValid() {
		if _, ok := s.requests[responseTo]; !ok {
			panic(fmt.Sprintf("internal error: stream %v: response to untracked request %v", s.id, responseTo))
		}
		if s.id == "" {
			// This should be guaranteed not to happen by the stream resolution logic
			// above, but be defensive: we don't ever want to delete the standalone
			// stream.
			panic("internal error: response on standalone stream")
		}
		delete(s.requests, responseTo)
		if len(s.requests) == 0 {
			c.mu.Lock()
			delete(c.streams, s.id)
			c.mu.Unlock()
		}
	}

	delivered := false
	if c.eventStore != nil {
		if err := c.eventStore.Append(ctx, c.sessionID, s.id, data); err != nil {
			// TODO: report a side-channel error.
		} else {
			delivered = true
		}
	}
	if s.deliver != nil {
		if err := s.deliver(data, s.doneLocked()); err != nil {
			// TODO: report a side-channel error.
		} else {
			delivered = true
		}
	}
	if !delivered {
		return fmt.Errorf("%w: undelivered message", jsonrpc2.ErrRejected)
	}
	return nil
}

// Close implements the [Connection] interface.
func (c *streamableServerConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.isDone {
		c.isDone = true
		close(c.done)
		if c.eventStore != nil {
			// TODO: find a way to plumb a context here, or an event store with a long-running
			// close operation can take arbitrary time. Alternative: impose a fixed timeout here.
			return c.eventStore.SessionClosed(context.TODO(), c.sessionID)
		}
	}
	return nil
}

// A StreamableClientTransport is a [Transport] that can communicate with an MCP
// endpoint serving the streamable HTTP transport defined by the 2025-03-26
// version of the spec.
type StreamableClientTransport struct {
	Endpoint   string
	HTTPClient *http.Client
	// MaxRetries is the maximum number of times to attempt a reconnect before giving up.
	// It defaults to 5. To disable retries, use a negative number.
	MaxRetries int

	// TODO(rfindley): propose exporting these.
	// If strict is set, the transport is in 'strict mode', where any violation
	// of the MCP spec causes a failure.
	strict bool
	// If logger is set, it is used to log aspects of the transport, such as spec
	// violations that were ignored.
	logger *slog.Logger
}

// These settings are not (yet) exposed to the user in
// StreamableClientTransport.
const (
	// reconnectGrowFactor is the multiplicative factor by which the delay increases after each attempt.
	// A value of 1.0 results in a constant delay, while a value of 2.0 would double it each time.
	// It must be 1.0 or greater if MaxRetries is greater than 0.
	reconnectGrowFactor = 1.5
	// reconnectInitialDelay is the base delay for the first reconnect attempt.
	reconnectInitialDelay = 1 * time.Second
	// reconnectMaxDelay caps the backoff delay, preventing it from growing indefinitely.
	reconnectMaxDelay = 30 * time.Second
)

// Connect implements the [Transport] interface.
//
// The resulting [Connection] writes messages via POST requests to the
// transport URL with the Mcp-Session-Id header set, and reads messages from
// hanging requests.
//
// When closed, the connection issues a DELETE request to terminate the logical
// session.
func (t *StreamableClientTransport) Connect(ctx context.Context) (Connection, error) {
	client := t.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	maxRetries := t.MaxRetries
	if maxRetries == 0 {
		maxRetries = 5
	} else if maxRetries < 0 {
		maxRetries = 0
	}
	// Create a new cancellable context that will manage the connection's lifecycle.
	// This is crucial for cleanly shutting down the background SSE listener by
	// cancelling its blocking network operations, which prevents hangs on exit.
	connCtx, cancel := context.WithCancel(ctx)
	conn := &streamableClientConn{
		url:        t.Endpoint,
		client:     client,
		incoming:   make(chan jsonrpc.Message, 10),
		done:       make(chan struct{}),
		maxRetries: maxRetries,
		strict:     t.strict,
		logger:     t.logger,
		ctx:        connCtx,
		cancel:     cancel,
		failed:     make(chan struct{}),
	}
	return conn, nil
}

type streamableClientConn struct {
	url        string
	client     *http.Client
	ctx        context.Context
	cancel     context.CancelFunc
	incoming   chan jsonrpc.Message
	maxRetries int
	strict     bool         // from [StreamableClientTransport.strict]
	logger     *slog.Logger // from [StreamableClientTransport.logger]

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

// errSessionMissing distinguishes if the session is known to not be present on
// the server (see [streamableClientConn.fail]).
//
// TODO(rfindley): should we expose this error value (and its corresponding
// API) to the user?
//
// The spec says that if the server returns 404, clients should reestablish
// a session. For now, we delegate that to the user, but do they need a way to
// differentiate a 'NotFound' error from other errors?
var errSessionMissing = errors.New("session not found")

var _ clientConnection = (*streamableClientConn)(nil)

func (c *streamableClientConn) sessionUpdated(state clientSessionState) {
	c.mu.Lock()
	c.initializedResult = state.InitializeResult
	c.mu.Unlock()

	// Start the standalone SSE stream as soon as we have the initialized
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
	c.connectStandaloneSSE()
}

func (c *streamableClientConn) connectStandaloneSSE() {
	resp, err := c.connectSSE("")
	if err != nil {
		c.fail(fmt.Errorf("standalone SSE request failed (session ID: %v): %v", c.sessionID, err))
		return
	}

	// [§2.2.3]: "The server MUST either return Content-Type:
	// text/event-stream in response to this HTTP GET, or else return HTTP
	// 405 Method Not Allowed, indicating that the server does not offer an
	// SSE stream at this endpoint."
	//
	// [§2.2.3]: https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#listening-for-messages-from-the-server
	if resp.StatusCode == http.StatusMethodNotAllowed {
		// The server doesn't support the standalone SSE stream.
		resp.Body.Close()
		return
	}
	if resp.StatusCode == http.StatusNotFound && !c.strict {
		// modelcontextprotocol/gosdk#393: some servers return NotFound instead
		// of MethodNotAllowed for the standalone SSE stream.
		//
		// Treat this like MethodNotAllowed in non-strict mode.
		if c.logger != nil {
			c.logger.Warn("got 404 instead of 405 for standalone SSE stream")
		}
		resp.Body.Close()
		return
	}
	summary := "standalone SSE stream"
	if err := c.checkResponse(summary, resp); err != nil {
		c.fail(err)
		return
	}
	go c.handleSSE(summary, resp, true, nil)
}

// fail handles an asynchronous error while reading.
//
// If err is non-nil, it is terminal, and subsequent (or pending) Reads will
// fail.
//
// If err wraps errSessionMissing, the failure indicates that the session is no
// longer present on the server, and no final DELETE will be performed when
// closing the connection.
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
	case msg := <-c.incoming:
		return msg, nil
	}
}

// Write implements the [Connection] interface.
func (c *streamableClientConn) Write(ctx context.Context, msg jsonrpc.Message) error {
	if err := c.failure(); err != nil {
		return err
	}

	var requestSummary string
	var isCall bool
	switch msg := msg.(type) {
	case *jsonrpc.Request:
		requestSummary = fmt.Sprintf("sending %q", msg.Method)
		isCall = msg.IsCall()
	case *jsonrpc.Response:
		requestSummary = fmt.Sprintf("sending jsonrpc response #%d", msg.ID)
	default:
		panic("unreachable")
	}

	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return fmt.Errorf("%s: %v", requestSummary, err)
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
		return fmt.Errorf("%s: %v", requestSummary, err)
	}

	if err := c.checkResponse(requestSummary, resp); err != nil {
		c.fail(err)
		return err
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
	// TODO(rfindley): this logic isn't quite right.
	// We should keep going even if the server returns 202, if we have a call.
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusAccepted {
		// [§2.1.4]: "If the input is a JSON-RPC response or notification:
		// If the server accepts the input, the server MUST return HTTP status code 202 Accepted with no body."
		//
		// [§2.1.4]: https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#listening-for-messages-from-the-server
		resp.Body.Close()
		return nil
	} else if !isCall && !c.strict {
		// Some servers return 200, even with an empty json body.
		// Ignore this response in non-strict mode.
		if c.logger != nil {
			c.logger.Warn(fmt.Sprintf("unexpected status code %d from non-call", resp.StatusCode))
		}
		resp.Body.Close()
		return nil
	}

	contentType := strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])
	switch contentType {
	case "application/json":
		go c.handleJSON(requestSummary, resp)

	case "text/event-stream":
		var forCall *jsonrpc.Request
		if jsonReq, ok := msg.(*jsonrpc.Request); ok && jsonReq.IsCall() {
			forCall = jsonReq
		}
		// TODO: should we cancel this logical SSE request if/when jsonReq is canceled?
		go c.handleSSE(requestSummary, resp, false, forCall)

	default:
		resp.Body.Close()
		return fmt.Errorf("%s: unsupported content type %q", requestSummary, contentType)
	}
	return nil
}

// testAuth controls whether a fake Authorization header is added to outgoing requests.
// TODO: replace with a better mechanism when client-side auth is in place.
var testAuth atomic.Bool

func (c *streamableClientConn) setMCPHeaders(req *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.initializedResult != nil {
		req.Header.Set(protocolVersionHeader, c.initializedResult.ProtocolVersion)
	}
	if c.sessionID != "" {
		req.Header.Set(sessionIDHeader, c.sessionID)
	}
	if testAuth.Load() {
		req.Header.Set("Authorization", "Bearer foo")
	}
}

func (c *streamableClientConn) handleJSON(requestSummary string, resp *http.Response) {
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		c.fail(fmt.Errorf("%s: failed to read body: %v", requestSummary, err))
		return
	}
	msg, err := jsonrpc.DecodeMessage(body)
	if err != nil {
		c.fail(fmt.Errorf("%s: failed to decode response: %v", requestSummary, err))
		return
	}
	select {
	case c.incoming <- msg:
	case <-c.done:
		// The connection was closed by the client; exit gracefully.
	}
}

// handleSSE manages the lifecycle of an SSE connection. It can be either
// persistent (for the main GET listener) or temporary (for a POST response).
//
// If forCall is set, it is the call that initiated the stream, and the
// stream is complete when we receive its response.
func (c *streamableClientConn) handleSSE(requestSummary string, resp *http.Response, persistent bool, forCall *jsonrpc2.Request) {
	for {
		// Connection was successful. Continue the loop with the new response.
		// TODO: we should set a reasonable limit on the number of times we'll try
		// getting a response for a given request.
		//
		// Eventually, if we don't get the response, we should stop trying and
		// fail the request.
		lastEventID, clientClosed := c.processStream(requestSummary, resp, forCall)

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
		newResp, err := c.connectSSE(lastEventID)
		if err != nil {
			// All reconnection attempts failed: fail the connection.
			c.fail(fmt.Errorf("%s: failed to reconnect (session ID: %v): %v", requestSummary, c.sessionID, err))
			return
		}
		resp = newResp
		if err := c.checkResponse(requestSummary, resp); err != nil {
			c.fail(err)
			return
		}
	}
}

// checkResponse checks the status code of the provided response, and
// translates it into an error if the request was unsuccessful.
//
// The response body is close if a non-nil error is returned.
func (c *streamableClientConn) checkResponse(requestSummary string, resp *http.Response) (err error) {
	defer func() {
		if err != nil {
			resp.Body.Close()
		}
	}()
	// §2.5.3: "The server MAY terminate the session at any time, after
	// which it MUST respond to requests containing that session ID with HTTP
	// 404 Not Found."
	if resp.StatusCode == http.StatusNotFound {
		// Return an errSessionMissing to avoid sending a redundant DELETE when the
		// session is already gone.
		return fmt.Errorf("%s: failed to connect (session ID: %v): %w", requestSummary, c.sessionID, errSessionMissing)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: failed to connect: %v", requestSummary, http.StatusText(resp.StatusCode))
	}
	return nil
}

// processStream reads from a single response body, sending events to the
// incoming channel. It returns the ID of the last processed event and a flag
// indicating if the connection was closed by the client. If resp is nil, it
// returns "", false.
func (c *streamableClientConn) processStream(requestSummary string, resp *http.Response, forCall *jsonrpc.Request) (lastEventID string, clientClosed bool) {
	defer resp.Body.Close()
	for evt, err := range scanEvents(resp.Body) {
		if err != nil {
			// TODO: we should differentiate EOF from other errors here.
			break
		}

		if evt.ID != "" {
			lastEventID = evt.ID
		}

		msg, err := jsonrpc.DecodeMessage(evt.Data)
		if err != nil {
			c.fail(fmt.Errorf("%s: failed to decode event: %v", requestSummary, err))
			return "", true
		}

		select {
		case c.incoming <- msg:
			if jsonResp, ok := msg.(*jsonrpc.Response); ok && forCall != nil {
				// TODO: we should never get a response when forReq is nil (the standalone SSE request).
				// We should detect this case.
				if jsonResp.ID == forCall.ID {
					return "", true
				}
			}
		case <-c.done:
			// The connection was closed by the client; exit gracefully.
			return "", true
		}
	}
	// The loop finished without an error, indicating the server closed the stream.
	//
	// If the lastEventID is "", the stream is not retryable and we should
	// report a synthetic error for the call.
	if lastEventID == "" && forCall != nil {
		errmsg := &jsonrpc2.Response{
			ID:    forCall.ID,
			Error: fmt.Errorf("request terminated without response"),
		}
		select {
		case c.incoming <- errmsg:
		case <-c.done:
		}
	}
	return lastEventID, false
}

// connectSSE handles the logic of connecting a text/event-stream connection.
//
// If lastEventID is set, it is the last-event ID of a stream being resumed.
//
// If connection fails, connectSSE retries with an exponential backoff
// strategy. It returns a new, valid HTTP response if successful, or an error
// if all retries are exhausted.
func (c *streamableClientConn) connectSSE(lastEventID string) (*http.Response, error) {
	var finalErr error
	// If lastEventID is set, we've already connected successfully once, so
	// consider that to be the first attempt.
	attempt := 0
	if lastEventID != "" {
		attempt = 1
	}
	for ; attempt <= c.maxRetries; attempt++ {
		select {
		case <-c.done:
			return nil, fmt.Errorf("connection closed by client during reconnect")
		case <-time.After(calculateReconnectDelay(attempt)):
			req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.url, nil)
			if err != nil {
				return nil, err
			}
			c.setMCPHeaders(req)
			if lastEventID != "" {
				req.Header.Set("Last-Event-ID", lastEventID)
			}
			req.Header.Set("Accept", "text/event-stream")
			resp, err := c.client.Do(req)
			if err != nil {
				finalErr = err // Store the error and try again.
				continue
			}
			return resp, nil
		}
	}
	// If the loop completes, all retries have failed, or the client is closing.
	if finalErr != nil {
		return nil, fmt.Errorf("connection failed after %d attempts: %w", c.maxRetries, finalErr)
	}
	return nil, fmt.Errorf("connection aborted after %d attempts", c.maxRetries)
}

// Close implements the [Connection] interface.
func (c *streamableClientConn) Close() error {
	c.closeOnce.Do(func() {
		if errors.Is(c.failure(), errSessionMissing) {
			// If the session is missing, no need to delete it.
		} else {
			req, err := http.NewRequestWithContext(c.ctx, http.MethodDelete, c.url, nil)
			if err != nil {
				c.closeErr = err
			} else {
				c.setMCPHeaders(req)
				if _, err := c.client.Do(req); err != nil {
					c.closeErr = err
				}
			}
		}

		// Cancel any hanging network requests after cleanup.
		c.cancel()
		close(c.done)
	})
	return c.closeErr
}

// calculateReconnectDelay calculates a delay using exponential backoff with full jitter.
func calculateReconnectDelay(attempt int) time.Duration {
	if attempt == 0 {
		return 0
	}
	// Calculate the exponential backoff using the grow factor.
	backoffDuration := time.Duration(float64(reconnectInitialDelay) * math.Pow(reconnectGrowFactor, float64(attempt-1)))
	// Cap the backoffDuration at maxDelay.
	backoffDuration = min(backoffDuration, reconnectMaxDelay)

	// Use a full jitter using backoffDuration
	jitter := rand.N(backoffDuration)

	return backoffDuration + jitter
}
