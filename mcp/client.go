// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// A Client is an MCP client, which may be connected to an MCP server
// using the [Client.Connect] method.
type Client struct {
	impl                    *Implementation
	opts                    ClientOptions
	mu                      sync.Mutex
	roots                   *featureSet[*Root]
	sessions                []*ClientSession
	sendingMethodHandler_   MethodHandler
	receivingMethodHandler_ MethodHandler
}

// NewClient creates a new [Client].
//
// Use [Client.Connect] to connect it to an MCP server.
//
// The first argument must not be nil.
//
// If non-nil, the provided options configure the Client.
func NewClient(impl *Implementation, opts *ClientOptions) *Client {
	if impl == nil {
		panic("nil Implementation")
	}
	c := &Client{
		impl:                    impl,
		roots:                   newFeatureSet(func(r *Root) string { return r.URI }),
		sendingMethodHandler_:   defaultSendingMethodHandler[*ClientSession],
		receivingMethodHandler_: defaultReceivingMethodHandler[*ClientSession],
	}
	if opts != nil {
		c.opts = *opts
	}
	return c
}

// ClientOptions configures the behavior of the client.
type ClientOptions struct {
	// Handler for sampling.
	// Called when a server calls CreateMessage.
	CreateMessageHandler func(context.Context, *ClientRequest[*CreateMessageParams]) (*CreateMessageResult, error)
	// Handlers for notifications from the server.
	ToolListChangedHandler      func(context.Context, *ClientRequest[*ToolListChangedParams])
	PromptListChangedHandler    func(context.Context, *ClientRequest[*PromptListChangedParams])
	ResourceListChangedHandler  func(context.Context, *ClientRequest[*ResourceListChangedParams])
	ResourceUpdatedHandler      func(context.Context, *ClientRequest[*ResourceUpdatedNotificationParams])
	LoggingMessageHandler       func(context.Context, *ClientRequest[*LoggingMessageParams])
	ProgressNotificationHandler func(context.Context, *ClientRequest[*ProgressNotificationParams])
	// If non-zero, defines an interval for regular "ping" requests.
	// If the peer fails to respond to pings originating from the keepalive check,
	// the session is automatically closed.
	KeepAlive time.Duration
}

// bind implements the binder[*ClientSession] interface, so that Clients can
// be connected using [connect].
func (c *Client) bind(mcpConn Connection, conn *jsonrpc2.Connection, state *clientSessionState) *ClientSession {
	assert(mcpConn != nil && conn != nil, "nil connection")
	cs := &ClientSession{conn: conn, mcpConn: mcpConn, client: c}
	if state != nil {
		cs.state = *state
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions = append(c.sessions, cs)
	return cs
}

// disconnect implements the binder[*Client] interface, so that
// Clients can be connected using [connect].
func (c *Client) disconnect(cs *ClientSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions = slices.DeleteFunc(c.sessions, func(cs2 *ClientSession) bool {
		return cs2 == cs
	})
}

// TODO: Consider exporting this type and its field.
type unsupportedProtocolVersionError struct {
	version string
}

func (e unsupportedProtocolVersionError) Error() string {
	return fmt.Sprintf("unsupported protocol version: %q", e.version)
}

// ClientSessionOptions is reserved for future use.
type ClientSessionOptions struct {
}

// Connect begins an MCP session by connecting to a server over the given
// transport, and initializing the session.
//
// Typically, it is the responsibility of the client to close the connection
// when it is no longer needed. However, if the connection is closed by the
// server, calls or notifications will return an error wrapping
// [ErrConnectionClosed].
func (c *Client) Connect(ctx context.Context, t Transport, _ *ClientSessionOptions) (cs *ClientSession, err error) {
	cs, err = connect(ctx, t, c, (*clientSessionState)(nil))
	if err != nil {
		return nil, err
	}

	caps := &ClientCapabilities{}
	caps.Roots.ListChanged = true
	if c.opts.CreateMessageHandler != nil {
		caps.Sampling = &SamplingCapabilities{}
	}

	params := &InitializeParams{
		ProtocolVersion: latestProtocolVersion,
		ClientInfo:      c.impl,
		Capabilities:    caps,
	}
	req := &ClientRequest[*InitializeParams]{Session: cs, Params: params}
	res, err := handleSend[*InitializeResult](ctx, methodInitialize, req)
	if err != nil {
		_ = cs.Close()
		return nil, err
	}
	if !slices.Contains(supportedProtocolVersions, res.ProtocolVersion) {
		return nil, unsupportedProtocolVersionError{res.ProtocolVersion}
	}
	cs.state.InitializeResult = res
	if hc, ok := cs.mcpConn.(clientConnection); ok {
		hc.sessionUpdated(cs.state)
	}
	req2 := &ClientRequest[*InitializedParams]{Session: cs, Params: &InitializedParams{}}
	if err := handleNotify(ctx, notificationInitialized, req2); err != nil {
		_ = cs.Close()
		return nil, err
	}

	if c.opts.KeepAlive > 0 {
		cs.startKeepalive(c.opts.KeepAlive)
	}

	return cs, nil
}

// A ClientSession is a logical connection with an MCP server. Its
// methods can be used to send requests or notifications to the server. Create
// a session by calling [Client.Connect].
//
// Call [ClientSession.Close] to close the connection, or await server
// termination with [ClientSession.Wait].
type ClientSession struct {
	conn            *jsonrpc2.Connection
	client          *Client
	keepaliveCancel context.CancelFunc
	mcpConn         Connection

	// No mutex is (currently) required to guard the session state, because it is
	// only set synchronously during Client.Connect.
	state clientSessionState
}

type clientSessionState struct {
	InitializeResult *InitializeResult
}

func (cs *ClientSession) ID() string {
	if c, ok := cs.mcpConn.(hasSessionID); ok {
		return c.SessionID()
	}
	return ""
}

// Close performs a graceful close of the connection, preventing new requests
// from being handled, and waiting for ongoing requests to return. Close then
// terminates the connection.
func (cs *ClientSession) Close() error {
	// Note: keepaliveCancel access is safe without a mutex because:
	// 1. keepaliveCancel is only written once during startKeepalive (happens-before all Close calls)
	// 2. context.CancelFunc is safe to call multiple times and from multiple goroutines
	// 3. The keepalive goroutine calls Close on ping failure, but this is safe since
	//    Close is idempotent and conn.Close() handles concurrent calls correctly
	if cs.keepaliveCancel != nil {
		cs.keepaliveCancel()
	}
	return cs.conn.Close()
}

// Wait waits for the connection to be closed by the server.
// Generally, clients should be responsible for closing the connection.
func (cs *ClientSession) Wait() error {
	return cs.conn.Wait()
}

// startKeepalive starts the keepalive mechanism for this client session.
func (cs *ClientSession) startKeepalive(interval time.Duration) {
	startKeepalive(cs, interval, &cs.keepaliveCancel)
}

// AddRoots adds the given roots to the client,
// replacing any with the same URIs,
// and notifies any connected servers.
func (c *Client) AddRoots(roots ...*Root) {
	// Only notify if something could change.
	if len(roots) == 0 {
		return
	}
	changeAndNotify(c, notificationRootsListChanged, &RootsListChangedParams{},
		func() bool { c.roots.add(roots...); return true })
}

// RemoveRoots removes the roots with the given URIs,
// and notifies any connected servers if the list has changed.
// It is not an error to remove a nonexistent root.
// TODO: notification
func (c *Client) RemoveRoots(uris ...string) {
	changeAndNotify(c, notificationRootsListChanged, &RootsListChangedParams{},
		func() bool { return c.roots.remove(uris...) })
}

// changeAndNotify is called when a feature is added or removed.
// It calls change, which should do the work and report whether a change actually occurred.
// If there was a change, it notifies a snapshot of the sessions.
func changeAndNotify[P Params](c *Client, notification string, params P, change func() bool) {
	var sessions []*ClientSession
	// Lock for the change, but not for the notification.
	c.mu.Lock()
	if change() {
		sessions = slices.Clone(c.sessions)
	}
	c.mu.Unlock()
	notifySessions(sessions, notification, params)
}

func (c *Client) listRoots(_ context.Context, req *ClientRequest[*ListRootsParams]) (*ListRootsResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	roots := slices.Collect(c.roots.all())
	if roots == nil {
		roots = []*Root{} // avoid JSON null
	}
	return &ListRootsResult{
		Roots: roots,
	}, nil
}

func (c *Client) createMessage(ctx context.Context, req *ClientRequest[*CreateMessageParams]) (*CreateMessageResult, error) {
	if c.opts.CreateMessageHandler == nil {
		// TODO: wrap or annotate this error? Pick a standard code?
		return nil, jsonrpc2.NewError(CodeUnsupportedMethod, "client does not support CreateMessage")
	}
	return c.opts.CreateMessageHandler(ctx, req)
}

// AddSendingMiddleware wraps the current sending method handler using the provided
// middleware. Middleware is applied from right to left, so that the first one is
// executed first.
//
// For example, AddSendingMiddleware(m1, m2, m3) augments the method handler as
// m1(m2(m3(handler))).
//
// Sending middleware is called when a request is sent. It is useful for tasks
// such as tracing, metrics, and adding progress tokens.
func (c *Client) AddSendingMiddleware(middleware ...Middleware) {
	c.mu.Lock()
	defer c.mu.Unlock()
	addMiddleware(&c.sendingMethodHandler_, middleware)
}

// AddReceivingMiddleware wraps the current receiving method handler using
// the provided middleware. Middleware is applied from right to left, so that the
// first one is executed first.
//
// For example, AddReceivingMiddleware(m1, m2, m3) augments the method handler as
// m1(m2(m3(handler))).
//
// Receiving middleware is called when a request is received. It is useful for tasks
// such as authentication, request logging and metrics.
func (c *Client) AddReceivingMiddleware(middleware ...Middleware) {
	c.mu.Lock()
	defer c.mu.Unlock()
	addMiddleware(&c.receivingMethodHandler_, middleware)
}

// clientMethodInfos maps from the RPC method name to serverMethodInfos.
//
// The 'allowMissingParams' values are extracted from the protocol schema.
// TODO(rfindley): actually load and validate the protocol schema, rather than
// curating these method flags.
var clientMethodInfos = map[string]methodInfo{
	methodComplete:                  newClientMethodInfo(clientSessionMethod((*ClientSession).Complete), 0),
	methodPing:                      newClientMethodInfo(clientSessionMethod((*ClientSession).ping), missingParamsOK),
	methodListRoots:                 newClientMethodInfo(clientMethod((*Client).listRoots), missingParamsOK),
	methodCreateMessage:             newClientMethodInfo(clientMethod((*Client).createMessage), 0),
	notificationToolListChanged:     newClientMethodInfo(clientMethod((*Client).callToolChangedHandler), notification|missingParamsOK),
	notificationPromptListChanged:   newClientMethodInfo(clientMethod((*Client).callPromptChangedHandler), notification|missingParamsOK),
	notificationResourceListChanged: newClientMethodInfo(clientMethod((*Client).callResourceChangedHandler), notification|missingParamsOK),
	notificationResourceUpdated:     newClientMethodInfo(clientMethod((*Client).callResourceUpdatedHandler), notification|missingParamsOK),
	notificationLoggingMessage:      newClientMethodInfo(clientMethod((*Client).callLoggingHandler), notification),
	notificationProgress:            newClientMethodInfo(clientSessionMethod((*ClientSession).callProgressNotificationHandler), notification),
}

func (cs *ClientSession) sendingMethodInfos() map[string]methodInfo {
	return serverMethodInfos
}

func (cs *ClientSession) receivingMethodInfos() map[string]methodInfo {
	return clientMethodInfos
}

func (cs *ClientSession) handle(ctx context.Context, req *jsonrpc.Request) (any, error) {
	return handleReceive(ctx, cs, req)
}

func (cs *ClientSession) sendingMethodHandler() methodHandler {
	cs.client.mu.Lock()
	defer cs.client.mu.Unlock()
	return cs.client.sendingMethodHandler_
}

func (cs *ClientSession) receivingMethodHandler() methodHandler {
	cs.client.mu.Lock()
	defer cs.client.mu.Unlock()
	return cs.client.receivingMethodHandler_
}

// getConn implements [Session.getConn].
func (cs *ClientSession) getConn() *jsonrpc2.Connection { return cs.conn }

func (*ClientSession) ping(context.Context, *PingParams) (*emptyResult, error) {
	return &emptyResult{}, nil
}

func newClientRequest[P Params](cs *ClientSession, params P) *ClientRequest[P] {
	return &ClientRequest[P]{Session: cs, Params: params}
}

// Ping makes an MCP "ping" request to the server.
func (cs *ClientSession) Ping(ctx context.Context, params *PingParams) error {
	_, err := handleSend[*emptyResult](ctx, methodPing, newClientRequest(cs, orZero[Params](params)))
	return err
}

// ListPrompts lists prompts that are currently available on the server.
func (cs *ClientSession) ListPrompts(ctx context.Context, params *ListPromptsParams) (*ListPromptsResult, error) {
	return handleSend[*ListPromptsResult](ctx, methodListPrompts, newClientRequest(cs, orZero[Params](params)))
}

// GetPrompt gets a prompt from the server.
func (cs *ClientSession) GetPrompt(ctx context.Context, params *GetPromptParams) (*GetPromptResult, error) {
	return handleSend[*GetPromptResult](ctx, methodGetPrompt, newClientRequest(cs, orZero[Params](params)))
}

// ListTools lists tools that are currently available on the server.
func (cs *ClientSession) ListTools(ctx context.Context, params *ListToolsParams) (*ListToolsResult, error) {
	return handleSend[*ListToolsResult](ctx, methodListTools, newClientRequest(cs, orZero[Params](params)))
}

// CallTool calls the tool with the given name and arguments.
// The arguments can be any value that marshals into a JSON object.
func (cs *ClientSession) CallTool(ctx context.Context, params *CallToolParams) (*CallToolResult, error) {
	if params == nil {
		params = new(CallToolParams)
	}
	if params.Arguments == nil {
		// Avoid sending nil over the wire.
		params.Arguments = map[string]any{}
	}
	return handleSend[*CallToolResult](ctx, methodCallTool, newClientRequest(cs, orZero[Params](params)))
}

func (cs *ClientSession) SetLevel(ctx context.Context, params *SetLevelParams) error {
	_, err := handleSend[*emptyResult](ctx, methodSetLevel, newClientRequest(cs, orZero[Params](params)))
	return err
}

// ListResources lists the resources that are currently available on the server.
func (cs *ClientSession) ListResources(ctx context.Context, params *ListResourcesParams) (*ListResourcesResult, error) {
	return handleSend[*ListResourcesResult](ctx, methodListResources, newClientRequest(cs, orZero[Params](params)))
}

// ListResourceTemplates lists the resource templates that are currently available on the server.
func (cs *ClientSession) ListResourceTemplates(ctx context.Context, params *ListResourceTemplatesParams) (*ListResourceTemplatesResult, error) {
	return handleSend[*ListResourceTemplatesResult](ctx, methodListResourceTemplates, newClientRequest(cs, orZero[Params](params)))
}

// ReadResource asks the server to read a resource and return its contents.
func (cs *ClientSession) ReadResource(ctx context.Context, params *ReadResourceParams) (*ReadResourceResult, error) {
	return handleSend[*ReadResourceResult](ctx, methodReadResource, newClientRequest(cs, orZero[Params](params)))
}

func (cs *ClientSession) Complete(ctx context.Context, params *CompleteParams) (*CompleteResult, error) {
	return handleSend[*CompleteResult](ctx, methodComplete, newClientRequest(cs, orZero[Params](params)))
}

// Subscribe sends a "resources/subscribe" request to the server, asking for
// notifications when the specified resource changes.
func (cs *ClientSession) Subscribe(ctx context.Context, params *SubscribeParams) error {
	_, err := handleSend[*emptyResult](ctx, methodSubscribe, newClientRequest(cs, orZero[Params](params)))
	return err
}

// Unsubscribe sends a "resources/unsubscribe" request to the server, cancelling
// a previous subscription.
func (cs *ClientSession) Unsubscribe(ctx context.Context, params *UnsubscribeParams) error {
	_, err := handleSend[*emptyResult](ctx, methodUnsubscribe, newClientRequest(cs, orZero[Params](params)))
	return err
}

func (c *Client) callToolChangedHandler(ctx context.Context, req *ClientRequest[*ToolListChangedParams]) (Result, error) {
	if h := c.opts.ToolListChangedHandler; h != nil {
		h(ctx, req)
	}
	return nil, nil
}

func (c *Client) callPromptChangedHandler(ctx context.Context, req *ClientRequest[*PromptListChangedParams]) (Result, error) {
	if h := c.opts.PromptListChangedHandler; h != nil {
		h(ctx, req)
	}
	return nil, nil
}

func (c *Client) callResourceChangedHandler(ctx context.Context, req *ClientRequest[*ResourceListChangedParams]) (Result, error) {
	if h := c.opts.ResourceListChangedHandler; h != nil {
		h(ctx, req)
	}
	return nil, nil
}

func (c *Client) callResourceUpdatedHandler(ctx context.Context, req *ClientRequest[*ResourceUpdatedNotificationParams]) (Result, error) {
	if h := c.opts.ResourceUpdatedHandler; h != nil {
		h(ctx, req)
	}
	return nil, nil
}

func (c *Client) callLoggingHandler(ctx context.Context, req *ClientRequest[*LoggingMessageParams]) (Result, error) {
	if h := c.opts.LoggingMessageHandler; h != nil {
		h(ctx, req)
	}
	return nil, nil
}

func (cs *ClientSession) callProgressNotificationHandler(ctx context.Context, params *ProgressNotificationParams) (Result, error) {
	if h := cs.client.opts.ProgressNotificationHandler; h != nil {
		h(ctx, clientRequestFor(cs, params))
	}
	return nil, nil
}

// NotifyProgress sends a progress notification from the client to the server
// associated with this session.
// This can be used if the client is performing a long-running task that was
// initiated by the server
func (cs *ClientSession) NotifyProgress(ctx context.Context, params *ProgressNotificationParams) error {
	return handleNotify(ctx, notificationProgress, newClientRequest(cs, orZero[Params](params)))
}

// Tools provides an iterator for all tools available on the server,
// automatically fetching pages and managing cursors.
// The params argument can set the initial cursor.
// Iteration stops at the first encountered error, which will be yielded.
func (cs *ClientSession) Tools(ctx context.Context, params *ListToolsParams) iter.Seq2[*Tool, error] {
	if params == nil {
		params = &ListToolsParams{}
	}
	return paginate(ctx, params, cs.ListTools, func(res *ListToolsResult) []*Tool {
		return res.Tools
	})
}

// Resources provides an iterator for all resources available on the server,
// automatically fetching pages and managing cursors.
// The params argument can set the initial cursor.
// Iteration stops at the first encountered error, which will be yielded.
func (cs *ClientSession) Resources(ctx context.Context, params *ListResourcesParams) iter.Seq2[*Resource, error] {
	if params == nil {
		params = &ListResourcesParams{}
	}
	return paginate(ctx, params, cs.ListResources, func(res *ListResourcesResult) []*Resource {
		return res.Resources
	})
}

// ResourceTemplates provides an iterator for all resource templates available on the server,
// automatically fetching pages and managing cursors.
// The `params` argument can set the initial cursor.
// Iteration stops at the first encountered error, which will be yielded.
func (cs *ClientSession) ResourceTemplates(ctx context.Context, params *ListResourceTemplatesParams) iter.Seq2[*ResourceTemplate, error] {
	if params == nil {
		params = &ListResourceTemplatesParams{}
	}
	return paginate(ctx, params, cs.ListResourceTemplates, func(res *ListResourceTemplatesResult) []*ResourceTemplate {
		return res.ResourceTemplates
	})
}

// Prompts provides an iterator for all prompts available on the server,
// automatically fetching pages and managing cursors.
// The params argument can set the initial cursor.
// Iteration stops at the first encountered error, which will be yielded.
func (cs *ClientSession) Prompts(ctx context.Context, params *ListPromptsParams) iter.Seq2[*Prompt, error] {
	if params == nil {
		params = &ListPromptsParams{}
	}
	return paginate(ctx, params, cs.ListPrompts, func(res *ListPromptsResult) []*Prompt {
		return res.Prompts
	})
}

// paginate is a generic helper function to provide a paginated iterator.
func paginate[P listParams, R listResult[T], T any](ctx context.Context, params P, listFunc func(context.Context, P) (R, error), items func(R) []*T) iter.Seq2[*T, error] {
	return func(yield func(*T, error) bool) {
		for {
			res, err := listFunc(ctx, params)
			if err != nil {
				yield(nil, err)
				return
			}
			for _, r := range items(res) {
				if !yield(r, nil) {
					return
				}
			}
			nextCursorVal := res.nextCursorPtr()
			if nextCursorVal == nil || *nextCursorVal == "" {
				return
			}
			*params.cursorPtr() = *nextCursorVal
		}
	}
}
