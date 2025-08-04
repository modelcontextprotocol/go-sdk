// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/internal/util"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

const DefaultPageSize = 1000

// A Server is an instance of an MCP server.
//
// Servers expose server-side MCP features, which can serve one or more MCP
// sessions by using [Server.Run].
type Server struct {
	// fixed at creation
	impl *Implementation
	opts ServerOptions

	mu                      sync.Mutex
	prompts                 *featureSet[*serverPrompt]
	tools                   *featureSet[*serverTool]
	resources               *featureSet[*serverResource]
	resourceTemplates       *featureSet[*serverResourceTemplate]
	sessions                []*ServerSession
	sendingMethodHandler_   MethodHandler[*ServerSession]
	receivingMethodHandler_ MethodHandler[*ServerSession]
	resourceSubscriptions   map[string]map[*ServerSession]bool // uri -> session -> bool
}

// ServerOptions is used to configure behavior of the server.
type ServerOptions struct {
	// Optional instructions for connected clients.
	Instructions string
	// If non-nil, called when "notifications/initialized" is received.
	InitializedHandler func(context.Context, *ServerSession, *InitializedParams)
	// PageSize is the maximum number of items to return in a single page for
	// list methods (e.g. ListTools).
	PageSize int
	// If non-nil, called when "notifications/roots/list_changed" is received.
	RootsListChangedHandler func(context.Context, *ServerSession, *RootsListChangedParams)
	// If non-nil, called when "notifications/progress" is received.
	ProgressNotificationHandler func(context.Context, *ServerSession, *ProgressNotificationParams)
	// If non-nil, called when "completion/complete" is received.
	CompletionHandler func(context.Context, *ServerSession, *CompleteParams) (*CompleteResult, error)
	// If non-zero, defines an interval for regular "ping" requests.
	// If the peer fails to respond to pings originating from the keepalive check,
	// the session is automatically closed.
	KeepAlive time.Duration
	// Function called when a client session subscribes to a resource.
	SubscribeHandler func(context.Context, *ServerSession, *SubscribeParams) error
	// Function called when a client session unsubscribes from a resource.
	UnsubscribeHandler func(context.Context, *ServerSession, *UnsubscribeParams) error
	// If true, advertises the prompts capability during initialization,
	// even if no prompts have been registered.
	HasPrompts bool
	// If true, advertises the resources capability during initialization,
	// even if no resources have been registered.
	HasResources bool
	// If true, advertises the tools capability during initialization,
	// even if no tools have been registered.
	HasTools bool
}

// NewServer creates a new MCP server. The resulting server has no features:
// add features using the various Server.AddXXX methods, and the [AddTool] function.
//
// The server can be connected to one or more MCP clients using [Server.Run].
//
// The first argument must not be nil.
//
// If non-nil, the provided options are used to configure the server.
func NewServer(impl *Implementation, opts *ServerOptions) *Server {
	if impl == nil {
		panic("nil Implementation")
	}
	if opts == nil {
		opts = new(ServerOptions)
	}
	if opts.PageSize < 0 {
		panic(fmt.Errorf("invalid page size %d", opts.PageSize))
	}
	// TODO(jba): don't modify opts, modify Server.opts.
	if opts.PageSize == 0 {
		opts.PageSize = DefaultPageSize
	}
	if opts.SubscribeHandler != nil && opts.UnsubscribeHandler == nil {
		panic("SubscribeHandler requires UnsubscribeHandler")
	}
	if opts.UnsubscribeHandler != nil && opts.SubscribeHandler == nil {
		panic("UnsubscribeHandler requires SubscribeHandler")
	}
	return &Server{
		impl:                    impl,
		opts:                    *opts,
		prompts:                 newFeatureSet(func(p *serverPrompt) string { return p.prompt.Name }),
		tools:                   newFeatureSet(func(t *serverTool) string { return t.tool.Name }),
		resources:               newFeatureSet(func(r *serverResource) string { return r.resource.URI }),
		resourceTemplates:       newFeatureSet(func(t *serverResourceTemplate) string { return t.resourceTemplate.URITemplate }),
		sendingMethodHandler_:   defaultSendingMethodHandler[*ServerSession],
		receivingMethodHandler_: defaultReceivingMethodHandler[*ServerSession],
		resourceSubscriptions:   make(map[string]map[*ServerSession]bool),
	}
}

// AddPrompt adds a [Prompt] to the server, or replaces one with the same name.
func (s *Server) AddPrompt(p *Prompt, h PromptHandler) {
	// Assume there was a change, since add replaces existing items.
	// (It's possible an item was replaced with an identical one, but not worth checking.)
	s.changeAndNotify(
		notificationPromptListChanged,
		&PromptListChangedParams{},
		func() bool { s.prompts.add(&serverPrompt{p, h}); return true })
}

// RemovePrompts removes the prompts with the given names.
// It is not an error to remove a nonexistent prompt.
func (s *Server) RemovePrompts(names ...string) {
	s.changeAndNotify(notificationPromptListChanged, &PromptListChangedParams{},
		func() bool { return s.prompts.remove(names...) })
}

// AddTool adds a [Tool] to the server, or replaces one with the same name.
// The Tool argument must not be modified after this call.
//
// The tool's input schema must be non-nil. For a tool that takes no input,
// or one where any input is valid, set [Tool.InputSchema] to the empty schema,
// &jsonschema.Schema{}.
func (s *Server) AddTool(t *Tool, h ToolHandler) {
	if t.InputSchema == nil {
		// This prevents the tool author from forgetting to write a schema where
		// one should be provided. If we papered over this by supplying the empty
		// schema, then every input would be validated and the problem wouldn't be
		// discovered until runtime, when the LLM sent bad data.
		panic(fmt.Sprintf("adding tool %q: nil input schema", t.Name))
	}
	if err := addToolErr(s, t, h); err != nil {
		panic(err)
	}
}

// AddTool adds a [Tool] to the server, or replaces one with the same name.
// If the tool's input schema is nil, it is set to the schema inferred from the In
// type parameter, using [jsonschema.For].
// If the tool's output schema is nil and the Out type parameter is not the empty
// interface, then the output schema is set to the schema inferred from Out.
// The Tool argument must not be modified after this call.
func AddTool[In, Out any](s *Server, t *Tool, h ToolHandlerFor[In, Out]) {
	if err := addToolErr(s, t, h); err != nil {
		panic(err)
	}
}

func addToolErr[In, Out any](s *Server, t *Tool, h ToolHandlerFor[In, Out]) (err error) {
	defer util.Wrapf(&err, "adding tool %q", t.Name)
	st, err := newServerTool(t, h)
	if err != nil {
		return err
	}
	// Assume there was a change, since add replaces existing tools.
	// (It's possible a tool was replaced with an identical one, but not worth checking.)
	// TODO: Batch these changes by size and time? The typescript SDK doesn't.
	// TODO: Surface notify error here? best not, in case we need to batch.
	s.changeAndNotify(notificationToolListChanged, &ToolListChangedParams{},
		func() bool { s.tools.add(st); return true })
	return nil
}

// RemoveTools removes the tools with the given names.
// It is not an error to remove a nonexistent tool.
func (s *Server) RemoveTools(names ...string) {
	s.changeAndNotify(notificationToolListChanged, &ToolListChangedParams{},
		func() bool { return s.tools.remove(names...) })
}

// AddResource adds a [Resource] to the server, or replaces one with the same URI.
// AddResource panics if the resource URI is invalid or not absolute (has an empty scheme).
func (s *Server) AddResource(r *Resource, h ResourceHandler) {
	s.changeAndNotify(notificationResourceListChanged, &ResourceListChangedParams{},
		func() bool {
			u, err := url.Parse(r.URI)
			if err != nil {
				panic(err) // url.Parse includes the URI in the error
			}
			if !u.IsAbs() {
				panic(fmt.Errorf("URI %s needs a scheme", r.URI))
			}
			s.resources.add(&serverResource{r, h})
			return true
		})
}

// RemoveResources removes the resources with the given URIs.
// It is not an error to remove a nonexistent resource.
func (s *Server) RemoveResources(uris ...string) {
	s.changeAndNotify(notificationResourceListChanged, &ResourceListChangedParams{},
		func() bool { return s.resources.remove(uris...) })
}

// AddResourceTemplate adds a [ResourceTemplate] to the server, or replaces one with the same URI.
// AddResourceTemplate panics if a URI template is invalid or not absolute (has an empty scheme).
func (s *Server) AddResourceTemplate(t *ResourceTemplate, h ResourceHandler) {
	s.changeAndNotify(notificationResourceListChanged, &ResourceListChangedParams{},
		func() bool {
			// TODO: check template validity.
			s.resourceTemplates.add(&serverResourceTemplate{t, h})
			return true
		})
}

// RemoveResourceTemplates removes the resource templates with the given URI templates.
// It is not an error to remove a nonexistent resource.
func (s *Server) RemoveResourceTemplates(uriTemplates ...string) {
	s.changeAndNotify(notificationResourceListChanged, &ResourceListChangedParams{},
		func() bool { return s.resourceTemplates.remove(uriTemplates...) })
}

func (s *Server) capabilities() *serverCapabilities {
	s.mu.Lock()
	defer s.mu.Unlock()

	caps := &serverCapabilities{
		Logging: &loggingCapabilities{},
	}
	if s.opts.HasTools || s.tools.len() > 0 {
		caps.Tools = &toolCapabilities{ListChanged: true}
	}
	if s.opts.HasPrompts || s.prompts.len() > 0 {
		caps.Prompts = &promptCapabilities{ListChanged: true}
	}
	if s.opts.HasResources || s.resources.len() > 0 || s.resourceTemplates.len() > 0 {
		caps.Resources = &resourceCapabilities{ListChanged: true}
		if s.opts.SubscribeHandler != nil {
			caps.Resources.Subscribe = true
		}
	}
	if s.opts.CompletionHandler != nil {
		caps.Completions = &completionCapabilities{}
	}
	return caps
}

func (s *Server) complete(ctx context.Context, ss *ServerSession, params *CompleteParams) (Result, error) {
	if s.opts.CompletionHandler == nil {
		return nil, jsonrpc2.ErrMethodNotFound
	}
	return s.opts.CompletionHandler(ctx, ss, params)
}

// changeAndNotify is called when a feature is added or removed.
// It calls change, which should do the work and report whether a change actually occurred.
// If there was a change, it notifies a snapshot of the sessions.
func (s *Server) changeAndNotify(notification string, params Params, change func() bool) {
	var sessions []*ServerSession
	// Lock for the change, but not for the notification.
	s.mu.Lock()
	if change() {
		sessions = slices.Clone(s.sessions)
	}
	s.mu.Unlock()
	notifySessions(sessions, notification, params)
}

// Sessions returns an iterator that yields the current set of server sessions.
func (s *Server) Sessions() iter.Seq[*ServerSession] {
	s.mu.Lock()
	clients := slices.Clone(s.sessions)
	s.mu.Unlock()
	return slices.Values(clients)
}

func (s *Server) listPrompts(_ context.Context, _ *ServerSession, params *ListPromptsParams) (*ListPromptsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if params == nil {
		params = &ListPromptsParams{}
	}
	return paginateList(s.prompts, s.opts.PageSize, params, &ListPromptsResult{}, func(res *ListPromptsResult, prompts []*serverPrompt) {
		res.Prompts = []*Prompt{} // avoid JSON null
		for _, p := range prompts {
			res.Prompts = append(res.Prompts, p.prompt)
		}
	})
}

func (s *Server) getPrompt(ctx context.Context, cc *ServerSession, params *GetPromptParams) (*GetPromptResult, error) {
	s.mu.Lock()
	prompt, ok := s.prompts.get(params.Name)
	s.mu.Unlock()
	if !ok {
		// TODO: surface the error code over the wire, instead of flattening it into the string.
		return nil, fmt.Errorf("%s: unknown prompt %q", jsonrpc2.ErrInvalidParams, params.Name)
	}
	return prompt.handler(ctx, cc, params)
}

func (s *Server) listTools(_ context.Context, _ *ServerSession, params *ListToolsParams) (*ListToolsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if params == nil {
		params = &ListToolsParams{}
	}
	return paginateList(s.tools, s.opts.PageSize, params, &ListToolsResult{}, func(res *ListToolsResult, tools []*serverTool) {
		res.Tools = []*Tool{} // avoid JSON null
		for _, t := range tools {
			res.Tools = append(res.Tools, t.tool)
		}
	})
}

func (s *Server) callTool(ctx context.Context, cc *ServerSession, params *CallToolParamsFor[json.RawMessage]) (*CallToolResult, error) {
	s.mu.Lock()
	st, ok := s.tools.get(params.Name)
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%s: unknown tool %q", jsonrpc2.ErrInvalidParams, params.Name)
	}
	return st.handler(ctx, cc, params)
}

func (s *Server) listResources(_ context.Context, _ *ServerSession, params *ListResourcesParams) (*ListResourcesResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if params == nil {
		params = &ListResourcesParams{}
	}
	return paginateList(s.resources, s.opts.PageSize, params, &ListResourcesResult{}, func(res *ListResourcesResult, resources []*serverResource) {
		res.Resources = []*Resource{} // avoid JSON null
		for _, r := range resources {
			res.Resources = append(res.Resources, r.resource)
		}
	})
}

func (s *Server) listResourceTemplates(_ context.Context, _ *ServerSession, params *ListResourceTemplatesParams) (*ListResourceTemplatesResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if params == nil {
		params = &ListResourceTemplatesParams{}
	}
	return paginateList(s.resourceTemplates, s.opts.PageSize, params, &ListResourceTemplatesResult{},
		func(res *ListResourceTemplatesResult, rts []*serverResourceTemplate) {
			res.ResourceTemplates = []*ResourceTemplate{} // avoid JSON null
			for _, rt := range rts {
				res.ResourceTemplates = append(res.ResourceTemplates, rt.resourceTemplate)
			}
		})
}

func (s *Server) readResource(ctx context.Context, ss *ServerSession, params *ReadResourceParams) (*ReadResourceResult, error) {
	uri := params.URI
	// Look up the resource URI in the lists of resources and resource templates.
	// This is a security check as well as an information lookup.
	handler, mimeType, ok := s.lookupResourceHandler(uri)
	if !ok {
		// Don't expose the server configuration to the client.
		// Treat an unregistered resource the same as a registered one that couldn't be found.
		return nil, ResourceNotFoundError(uri)
	}
	res, err := handler(ctx, ss, params)
	if err != nil {
		return nil, err
	}
	if res == nil || res.Contents == nil {
		return nil, fmt.Errorf("reading resource %s: read handler returned nil information", uri)
	}
	// As a convenience, populate some fields.
	for _, c := range res.Contents {
		if c.URI == "" {
			c.URI = uri
		}
		if c.MIMEType == "" {
			c.MIMEType = mimeType
		}
	}
	return res, nil
}

// lookupResourceHandler returns the resource handler and MIME type for the resource or
// resource template matching uri. If none, the last return value is false.
func (s *Server) lookupResourceHandler(uri string) (ResourceHandler, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Try resources first.
	if r, ok := s.resources.get(uri); ok {
		return r.handler, r.resource.MIMEType, true
	}
	// Look for matching template.
	for rt := range s.resourceTemplates.all() {
		if rt.Matches(uri) {
			return rt.handler, rt.resourceTemplate.MIMEType, true
		}
	}
	return nil, "", false
}

// fileResourceHandler returns a ReadResourceHandler that reads paths using dir as
// a base directory.
// It honors client roots and protects against path traversal attacks.
//
// The dir argument should be a filesystem path. It need not be absolute, but
// that is recommended to avoid a dependency on the current working directory (the
// check against client roots is done with an absolute path). If dir is not absolute
// and the current working directory is unavailable, fileResourceHandler panics.
//
// Lexical path traversal attacks, where the path has ".." elements that escape dir,
// are always caught. Go 1.24 and above also protects against symlink-based attacks,
// where symlinks under dir lead out of the tree.
func fileResourceHandler(dir string) ResourceHandler {
	// Convert dir to an absolute path.
	dirFilepath, err := filepath.Abs(dir)
	if err != nil {
		panic(err)
	}
	return func(ctx context.Context, ss *ServerSession, params *ReadResourceParams) (_ *ReadResourceResult, err error) {
		defer util.Wrapf(&err, "reading resource %s", params.URI)

		// TODO: use a memoizing API here.
		rootRes, err := ss.ListRoots(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("listing roots: %w", err)
		}
		roots, err := fileRoots(rootRes.Roots)
		if err != nil {
			return nil, err
		}
		data, err := readFileResource(params.URI, dirFilepath, roots)
		if err != nil {
			return nil, err
		}
		// TODO(jba): figure out mime type. Omit for now: Server.readResource will fill it in.
		return &ReadResourceResult{Contents: []*ResourceContents{
			{URI: params.URI, Blob: data},
		}}, nil
	}
}

// ResourceUpdated sends a notification to all clients that have subscribed to the
// resource specified in params. This method is the primary way for a
// server author to signal that a resource has changed.
func (s *Server) ResourceUpdated(ctx context.Context, params *ResourceUpdatedNotificationParams) error {
	s.mu.Lock()
	subscribedSessions := s.resourceSubscriptions[params.URI]
	sessions := slices.Collect(maps.Keys(subscribedSessions))
	s.mu.Unlock()
	notifySessions(sessions, notificationResourceUpdated, params)
	return nil
}

func (s *Server) subscribe(ctx context.Context, ss *ServerSession, params *SubscribeParams) (*emptyResult, error) {
	if s.opts.SubscribeHandler == nil {
		return nil, fmt.Errorf("%w: server does not support resource subscriptions", jsonrpc2.ErrMethodNotFound)
	}
	if err := s.opts.SubscribeHandler(ctx, ss, params); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resourceSubscriptions[params.URI] == nil {
		s.resourceSubscriptions[params.URI] = make(map[*ServerSession]bool)
	}
	s.resourceSubscriptions[params.URI][ss] = true

	return &emptyResult{}, nil
}

func (s *Server) unsubscribe(ctx context.Context, ss *ServerSession, params *UnsubscribeParams) (*emptyResult, error) {
	if s.opts.UnsubscribeHandler == nil {
		return nil, jsonrpc2.ErrMethodNotFound
	}

	if err := s.opts.UnsubscribeHandler(ctx, ss, params); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if subscribedSessions, ok := s.resourceSubscriptions[params.URI]; ok {
		delete(subscribedSessions, ss)
		if len(subscribedSessions) == 0 {
			delete(s.resourceSubscriptions, params.URI)
		}
	}

	return &emptyResult{}, nil
}

// Run runs the server over the given transport, which must be persistent.
//
// Run blocks until the client terminates the connection or the provided
// context is cancelled. If the context is cancelled, Run closes the connection.
//
// If tools have been added to the server before this call, then the server will
// advertise the capability for tools, including the ability to send list-changed notifications.
// If no tools have been added, the server will not have the tool capability.
// The same goes for other features like prompts and resources.
func (s *Server) Run(ctx context.Context, t Transport) error {
	ss, err := s.Connect(ctx, t)
	if err != nil {
		return err
	}

	ssClosed := make(chan error)
	go func() {
		ssClosed <- ss.Wait()
	}()

	select {
	case <-ctx.Done():
		ss.Close()
		return ctx.Err()
	case err := <-ssClosed:
		return err
	}
}

// bind implements the binder[*ServerSession] interface, so that Servers can
// be connected using [connect].
func (s *Server) bind(conn *jsonrpc2.Connection) *ServerSession {
	ss := &ServerSession{conn: conn, server: s}
	s.mu.Lock()
	s.sessions = append(s.sessions, ss)
	s.mu.Unlock()
	return ss
}

// disconnect implements the binder[*ServerSession] interface, so that
// Servers can be connected using [connect].
func (s *Server) disconnect(cc *ServerSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = slices.DeleteFunc(s.sessions, func(cc2 *ServerSession) bool {
		return cc2 == cc
	})

	for _, subscribedSessions := range s.resourceSubscriptions {
		delete(subscribedSessions, cc)
	}
}

// Connect connects the MCP server over the given transport and starts handling
// messages.
//
// It returns a connection object that may be used to terminate the connection
// (with [Connection.Close]), or await client termination (with
// [Connection.Wait]).
func (s *Server) Connect(ctx context.Context, t Transport) (*ServerSession, error) {
	return connect(ctx, t, s)
}

func (s *Server) callInitializedHandler(ctx context.Context, ss *ServerSession, params *InitializedParams) (Result, error) {
	if s.opts.KeepAlive > 0 {
		ss.startKeepalive(s.opts.KeepAlive)
	}
	return callNotificationHandler(ctx, s.opts.InitializedHandler, ss, params)
}

func (s *Server) callRootsListChangedHandler(ctx context.Context, ss *ServerSession, params *RootsListChangedParams) (Result, error) {
	return callNotificationHandler(ctx, s.opts.RootsListChangedHandler, ss, params)
}

func (ss *ServerSession) callProgressNotificationHandler(ctx context.Context, params *ProgressNotificationParams) (Result, error) {
	return callNotificationHandler(ctx, ss.server.opts.ProgressNotificationHandler, ss, params)
}

// NotifyProgress sends a progress notification from the server to the client
// associated with this session.
// This is typically used to report on the status of a long-running request
// that was initiated by the client.
func (ss *ServerSession) NotifyProgress(ctx context.Context, params *ProgressNotificationParams) error {
	return handleNotify(ctx, ss, notificationProgress, params)
}

// A ServerSession is a logical connection from a single MCP client. Its
// methods can be used to send requests or notifications to the client. Create
// a session by calling [Server.Connect].
//
// Call [ServerSession.Close] to close the connection, or await client
// termination with [ServerSession.Wait].
type ServerSession struct {
	server           *Server
	conn             *jsonrpc2.Connection
	mcpConn          Connection
	mu               sync.Mutex
	logLevel         LoggingLevel
	initializeParams *InitializeParams
	initialized      bool
	keepaliveCancel  context.CancelFunc
}

func (ss *ServerSession) setConn(c Connection) {
	ss.mcpConn = c
}

func (ss *ServerSession) ID() string {
	if ss.mcpConn == nil {
		return ""
	}
	return ss.mcpConn.SessionID()
}

// Ping pings the client.
func (ss *ServerSession) Ping(ctx context.Context, params *PingParams) error {
	_, err := handleSend[*emptyResult](ctx, ss, methodPing, orZero[Params](params))
	return err
}

// ListRoots lists the client roots.
func (ss *ServerSession) ListRoots(ctx context.Context, params *ListRootsParams) (*ListRootsResult, error) {
	return handleSend[*ListRootsResult](ctx, ss, methodListRoots, orZero[Params](params))
}

// CreateMessage sends a sampling request to the client.
func (ss *ServerSession) CreateMessage(ctx context.Context, params *CreateMessageParams) (*CreateMessageResult, error) {
	return handleSend[*CreateMessageResult](ctx, ss, methodCreateMessage, orZero[Params](params))
}

// Log sends a log message to the client.
// The message is not sent if the client has not called SetLevel, or if its level
// is below that of the last SetLevel.
func (ss *ServerSession) Log(ctx context.Context, params *LoggingMessageParams) error {
	ss.mu.Lock()
	logLevel := ss.logLevel
	ss.mu.Unlock()
	if logLevel == "" {
		// The spec is unclear, but seems to imply that no log messages are sent until the client
		// sets the level.
		// TODO(jba): read other SDKs, possibly file an issue.
		return nil
	}
	if compareLevels(params.Level, logLevel) < 0 {
		return nil
	}
	return handleNotify(ctx, ss, notificationLoggingMessage, params)
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
func (s *Server) AddSendingMiddleware(middleware ...Middleware[*ServerSession]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	addMiddleware(&s.sendingMethodHandler_, middleware)
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
func (s *Server) AddReceivingMiddleware(middleware ...Middleware[*ServerSession]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	addMiddleware(&s.receivingMethodHandler_, middleware)
}

// serverMethodInfos maps from the RPC method name to serverMethodInfos.
//
// The 'allowMissingParams' values are extracted from the protocol schema.
// TODO(rfindley): actually load and validate the protocol schema, rather than
// curating these method flags.
var serverMethodInfos = map[string]methodInfo{
	methodComplete:               newMethodInfo(serverMethod((*Server).complete), 0),
	methodInitialize:             newMethodInfo(sessionMethod((*ServerSession).initialize), 0),
	methodPing:                   newMethodInfo(sessionMethod((*ServerSession).ping), missingParamsOK),
	methodListPrompts:            newMethodInfo(serverMethod((*Server).listPrompts), missingParamsOK),
	methodGetPrompt:              newMethodInfo(serverMethod((*Server).getPrompt), 0),
	methodListTools:              newMethodInfo(serverMethod((*Server).listTools), missingParamsOK),
	methodCallTool:               newMethodInfo(serverMethod((*Server).callTool), 0),
	methodListResources:          newMethodInfo(serverMethod((*Server).listResources), missingParamsOK),
	methodListResourceTemplates:  newMethodInfo(serverMethod((*Server).listResourceTemplates), missingParamsOK),
	methodReadResource:           newMethodInfo(serverMethod((*Server).readResource), 0),
	methodSetLevel:               newMethodInfo(sessionMethod((*ServerSession).setLevel), 0),
	methodSubscribe:              newMethodInfo(serverMethod((*Server).subscribe), 0),
	methodUnsubscribe:            newMethodInfo(serverMethod((*Server).unsubscribe), 0),
	notificationInitialized:      newMethodInfo(serverMethod((*Server).callInitializedHandler), notification|missingParamsOK),
	notificationRootsListChanged: newMethodInfo(serverMethod((*Server).callRootsListChangedHandler), notification|missingParamsOK),
	notificationProgress:         newMethodInfo(sessionMethod((*ServerSession).callProgressNotificationHandler), notification),
}

func (ss *ServerSession) sendingMethodInfos() map[string]methodInfo { return clientMethodInfos }

func (ss *ServerSession) receivingMethodInfos() map[string]methodInfo { return serverMethodInfos }

func (ss *ServerSession) sendingMethodHandler() methodHandler {
	ss.server.mu.Lock()
	defer ss.server.mu.Unlock()
	return ss.server.sendingMethodHandler_
}

func (ss *ServerSession) receivingMethodHandler() methodHandler {
	ss.server.mu.Lock()
	defer ss.server.mu.Unlock()
	return ss.server.receivingMethodHandler_
}

// getConn implements [session.getConn].
func (ss *ServerSession) getConn() *jsonrpc2.Connection { return ss.conn }

// handle invokes the method described by the given JSON RPC request.
func (ss *ServerSession) handle(ctx context.Context, req *jsonrpc.Request) (any, error) {
	ss.mu.Lock()
	initialized := ss.initialized
	ss.mu.Unlock()
	// From the spec:
	// "The client SHOULD NOT send requests other than pings before the server
	// has responded to the initialize request."
	switch req.Method {
	case "initialize", "ping":
	default:
		if !initialized {
			return nil, fmt.Errorf("method %q is invalid during session initialization", req.Method)
		}
	}
	// For the streamable transport, we need the request ID to correlate
	// server->client calls and notifications to the incoming request from which
	// they originated. See [idContextKey] for details.
	ctx = context.WithValue(ctx, idContextKey{}, req.ID)
	return handleReceive(ctx, ss, req)
}

func (ss *ServerSession) initialize(ctx context.Context, params *InitializeParams) (*InitializeResult, error) {
	if params == nil {
		return nil, fmt.Errorf("%w: \"params\" must be be provided", jsonrpc2.ErrInvalidParams)
	}
	ss.mu.Lock()
	ss.initializeParams = params
	ss.mu.Unlock()

	// Mark the connection as initialized when this method exits.
	// TODO: Technically, the server should not be considered initialized until it has
	// *responded*, but we don't have adequate visibility into the jsonrpc2
	// connection to implement that easily. In any case, once we've initialized
	// here, we can handle requests.
	defer func() {
		ss.mu.Lock()
		ss.initialized = true
		ss.mu.Unlock()
	}()

	// If we support the client's version, reply with it. Otherwise, reply with our
	// latest version.
	version := params.ProtocolVersion
	if !slices.Contains(supportedProtocolVersions, params.ProtocolVersion) {
		version = latestProtocolVersion
	}

	return &InitializeResult{
		// TODO(rfindley): alter behavior when falling back to an older version:
		// reject unsupported features.
		ProtocolVersion: version,
		Capabilities:    ss.server.capabilities(),
		Instructions:    ss.server.opts.Instructions,
		ServerInfo:      ss.server.impl,
	}, nil
}

func (ss *ServerSession) ping(context.Context, *PingParams) (*emptyResult, error) {
	return &emptyResult{}, nil
}

func (ss *ServerSession) setLevel(_ context.Context, params *SetLevelParams) (*emptyResult, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.logLevel = params.Level
	return &emptyResult{}, nil
}

// Close performs a graceful shutdown of the connection, preventing new
// requests from being handled, and waiting for ongoing requests to return.
// Close then terminates the connection.
func (ss *ServerSession) Close() error {
	if ss.keepaliveCancel != nil {
		// Note: keepaliveCancel access is safe without a mutex because:
		// 1. keepaliveCancel is only written once during startKeepalive (happens-before all Close calls)
		// 2. context.CancelFunc is safe to call multiple times and from multiple goroutines
		// 3. The keepalive goroutine calls Close on ping failure, but this is safe since
		//    Close is idempotent and conn.Close() handles concurrent calls correctly
		ss.keepaliveCancel()
	}
	return ss.conn.Close()
}

// Wait waits for the connection to be closed by the client.
func (ss *ServerSession) Wait() error {
	return ss.conn.Wait()
}

// startKeepalive starts the keepalive mechanism for this server session.
func (ss *ServerSession) startKeepalive(interval time.Duration) {
	startKeepalive(ss, interval, &ss.keepaliveCancel)
}

// pageToken is the internal structure for the opaque pagination cursor.
// It will be Gob-encoded and then Base64-encoded for use as a string token.
type pageToken struct {
	LastUID string // The unique ID of the last resource seen.
}

// encodeCursor encodes a unique identifier (UID) into a opaque pagination cursor
// by serializing a pageToken struct.
func encodeCursor(uid string) (string, error) {
	var buf bytes.Buffer
	token := pageToken{LastUID: uid}
	encoder := gob.NewEncoder(&buf)
	if err := encoder.Encode(token); err != nil {
		return "", fmt.Errorf("failed to encode page token: %w", err)
	}
	return base64.URLEncoding.EncodeToString(buf.Bytes()), nil
}

// decodeCursor decodes an opaque pagination cursor into the original pageToken struct.
func decodeCursor(cursor string) (*pageToken, error) {
	decodedBytes, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("failed to decode cursor: %w", err)
	}

	var token pageToken
	buf := bytes.NewBuffer(decodedBytes)
	decoder := gob.NewDecoder(buf)
	if err := decoder.Decode(&token); err != nil {
		return nil, fmt.Errorf("failed to decode page token: %w, cursor: %v", err, cursor)
	}
	return &token, nil
}

// paginateList is a generic helper that returns a paginated slice of items
// from a featureSet. It populates the provided result res with the items
// and sets its next cursor for subsequent pages.
// If there are no more pages, the next cursor within the result will be an empty string.
func paginateList[P listParams, R listResult[T], T any](fs *featureSet[T], pageSize int, params P, res R, setFunc func(R, []T)) (R, error) {
	var seq iter.Seq[T]
	if params.cursorPtr() == nil || *params.cursorPtr() == "" {
		seq = fs.all()
	} else {
		pageToken, err := decodeCursor(*params.cursorPtr())
		// According to the spec, invalid cursors should return Invalid params.
		if err != nil {
			var zero R
			return zero, jsonrpc2.ErrInvalidParams
		}
		seq = fs.above(pageToken.LastUID)
	}
	var count int
	var features []T
	for f := range seq {
		count++
		// If we've seen pageSize + 1 elements, we've gathered enough info to determine
		// if there's a next page. Stop processing the sequence.
		if count == pageSize+1 {
			break
		}
		features = append(features, f)
	}
	setFunc(res, features)
	// No remaining pages.
	if count < pageSize+1 {
		return res, nil
	}
	nextCursor, err := encodeCursor(fs.uniqueID(features[len(features)-1]))
	if err != nil {
		var zero R
		return zero, err
	}
	*res.nextCursorPtr() = nextCursor
	return res, nil
}
