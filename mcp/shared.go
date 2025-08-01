// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file contains code shared between client and server, including
// method handler and middleware definitions.
// TODO: much of this is here so that we can factor out commonalities using
// generics. Perhaps it can be simplified with reflection.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// latestProtocolVersion is the latest protocol version that this version of the SDK supports.
// It is the version that the client sends in the initialization request.
const latestProtocolVersion = "2025-06-18"

var supportedProtocolVersions = []string{
	latestProtocolVersion,
	"2025-03-26",
	"2024-11-05",
}

// A MethodHandler handles MCP messages.
// For methods, exactly one of the return values must be nil.
// For notifications, both must be nil.
type MethodHandler[S Session] func(ctx context.Context, _ S, method string, params Params) (result Result, err error)

// A methodHandler is a MethodHandler[Session] for some session.
// We need to give up type safety here, or we will end up with a type cycle somewhere
// else. For example, if Session.methodHandler returned a MethodHandler[Session],
// the compiler would complain.
type methodHandler any // MethodHandler[*ClientSession] | MethodHandler[*ServerSession]

// A Session is either a [ClientSession] or a [ServerSession].
type Session interface {
	*ClientSession | *ServerSession
	// ID returns the session ID, or the empty string if there is none.
	ID() string

	sendingMethodInfos() map[string]methodInfo
	receivingMethodInfos() map[string]methodInfo
	sendingMethodHandler() methodHandler
	receivingMethodHandler() methodHandler
	getConn() *jsonrpc2.Connection
}

// Middleware is a function from [MethodHandler] to [MethodHandler].
type Middleware[S Session] func(MethodHandler[S]) MethodHandler[S]

// addMiddleware wraps the handler in the middleware functions.
func addMiddleware[S Session](handlerp *MethodHandler[S], middleware []Middleware[S]) {
	for _, m := range slices.Backward(middleware) {
		*handlerp = m(*handlerp)
	}
}

func defaultSendingMethodHandler[S Session](ctx context.Context, session S, method string, params Params) (Result, error) {
	info, ok := session.sendingMethodInfos()[method]
	if !ok {
		// This can be called from user code, with an arbitrary value for method.
		return nil, jsonrpc2.ErrNotHandled
	}
	// Notifications don't have results.
	if strings.HasPrefix(method, "notifications/") {
		return nil, session.getConn().Notify(ctx, method, params)
	}
	// Create the result to unmarshal into.
	// The concrete type of the result is the return type of the receiving function.
	res := info.newResult()
	if err := call(ctx, session.getConn(), method, params, res); err != nil {
		return nil, err
	}
	return res, nil
}

// Helper method to avoid typed nil.
func orZero[T any, P *U, U any](p P) T {
	if p == nil {
		var zero T
		return zero
	}
	return any(p).(T)
}

func handleNotify[S Session](ctx context.Context, session S, method string, params Params) error {
	mh := session.sendingMethodHandler().(MethodHandler[S])
	_, err := mh(ctx, session, method, params)
	return err
}

func handleSend[R Result, S Session](ctx context.Context, s S, method string, params Params) (R, error) {
	mh := s.sendingMethodHandler().(MethodHandler[S])
	// mh might be user code, so ensure that it returns the right values for the jsonrpc2 protocol.
	res, err := mh(ctx, s, method, params)
	if err != nil {
		var z R
		return z, err
	}
	return res.(R), nil
}

// defaultReceivingMethodHandler is the initial MethodHandler for servers and clients, before being wrapped by middleware.
func defaultReceivingMethodHandler[S Session](ctx context.Context, session S, method string, params Params) (Result, error) {
	info, ok := session.receivingMethodInfos()[method]
	if !ok {
		// This can be called from user code, with an arbitrary value for method.
		return nil, jsonrpc2.ErrNotHandled
	}
	return info.handleMethod.(MethodHandler[S])(ctx, session, method, params)
}

func handleReceive[S Session](ctx context.Context, session S, req *jsonrpc.Request) (Result, error) {
	info, err := checkRequest(req, session.receivingMethodInfos())
	if err != nil {
		return nil, err
	}
	params, err := info.unmarshalParams(req.Params)
	if err != nil {
		return nil, fmt.Errorf("handling '%s': %w", req.Method, err)
	}

	mh := session.receivingMethodHandler().(MethodHandler[S])
	// mh might be user code, so ensure that it returns the right values for the jsonrpc2 protocol.
	res, err := mh(ctx, session, req.Method, params)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// checkRequest checks the given request against the provided method info, to
// ensure it is a valid MCP request.
//
// If valid, the relevant method info is returned. Otherwise, a non-nil error
// is returned describing why the request is invalid.
//
// This is extracted from request handling so that it can be called in the
// transport layer to preemptively reject bad requests.
func checkRequest(req *jsonrpc.Request, infos map[string]methodInfo) (methodInfo, error) {
	info, ok := infos[req.Method]
	if !ok {
		return methodInfo{}, fmt.Errorf("%w: %q unsupported", jsonrpc2.ErrNotHandled, req.Method)
	}
	if info.flags&notification != 0 && req.ID.IsValid() {
		return methodInfo{}, fmt.Errorf("%w: unexpected id for %q", jsonrpc2.ErrInvalidRequest, req.Method)
	}
	if info.flags&notification == 0 && !req.ID.IsValid() {
		return methodInfo{}, fmt.Errorf("%w: missing id for %q", jsonrpc2.ErrInvalidRequest, req.Method)
	}
	// missingParamsOK is checked here to catch the common case where "params" is
	// missing entirely.
	//
	// However, it's checked again after unmarshalling to catch the rare but
	// possible case where "params" is JSON null (see https://go.dev/issue/33835).
	if info.flags&missingParamsOK == 0 && len(req.Params) == 0 {
		return methodInfo{}, fmt.Errorf("%w: missing required \"params\"", jsonrpc2.ErrInvalidRequest)
	}
	return info, nil
}

// methodInfo is information about sending and receiving a method.
type methodInfo struct {
	// flags is a collection of flags controlling how the JSONRPC method is
	// handled. See individual flag values for documentation.
	flags methodFlags
	// Unmarshal params from the wire into a Params struct.
	// Used on the receive side.
	unmarshalParams func(json.RawMessage) (Params, error)
	// Run the code when a call to the method is received.
	// Used on the receive side.
	handleMethod methodHandler
	// Create a pointer to a Result struct.
	// Used on the send side.
	newResult func() Result
}

// The following definitions support converting from typed to untyped method handlers.
// Type parameter meanings:
// - S: sessions
// - P: params
// - R: results

// A typedMethodHandler is like a MethodHandler, but with type information.
type typedMethodHandler[S Session, P Params, R Result] func(context.Context, S, P) (R, error)

type paramsPtr[T any] interface {
	*T
	Params
}

type methodFlags int

const (
	notification    methodFlags = 1 << iota // method is a notification, not request
	missingParamsOK                         // params may be missing or null
)

// newMethodInfo creates a methodInfo from a typedMethodHandler.
//
// If isRequest is set, the method is treated as a request rather than a
// notification.
func newMethodInfo[S Session, P paramsPtr[T], R Result, T any](d typedMethodHandler[S, P, R], flags methodFlags) methodInfo {
	return methodInfo{
		flags: flags,
		unmarshalParams: func(m json.RawMessage) (Params, error) {
			var p P
			if m != nil {
				if err := json.Unmarshal(m, &p); err != nil {
					return nil, fmt.Errorf("unmarshaling %q into a %T: %w", m, p, err)
				}
			}
			// We must check missingParamsOK here, in addition to checkRequest, to
			// catch the edge cases where "params" is set to JSON null.
			// See also https://go.dev/issue/33835.
			//
			// We need to ensure that p is non-null to guard against crashes, as our
			// internal code or externally provided handlers may assume that params
			// is non-null.
			if flags&missingParamsOK == 0 && p == nil {
				return nil, fmt.Errorf("%w: missing required \"params\"", jsonrpc2.ErrInvalidRequest)
			}
			return orZero[Params](p), nil
		},
		handleMethod: MethodHandler[S](func(ctx context.Context, session S, _ string, params Params) (Result, error) {
			if params == nil {
				return d(ctx, session, nil)
			}
			return d(ctx, session, params.(P))
		}),
		// newResult is used on the send side, to construct the value to unmarshal the result into.
		// R is a pointer to a result struct. There is no way to "unpointer" it without reflection.
		// TODO(jba): explore generic approaches to this, perhaps by treating R in
		// the signature as the unpointered type.
		newResult: func() Result { return reflect.New(reflect.TypeFor[R]().Elem()).Interface().(R) },
	}
}

// serverMethod is glue for creating a typedMethodHandler from a method on Server.
func serverMethod[P Params, R Result](
	f func(*Server, context.Context, *ServerSession, P) (R, error),
) typedMethodHandler[*ServerSession, P, R] {
	return func(ctx context.Context, ss *ServerSession, p P) (R, error) {
		return f(ss.server, ctx, ss, p)
	}
}

// clientMethod is glue for creating a typedMethodHandler from a method on Client.
func clientMethod[P Params, R Result](
	f func(*Client, context.Context, *ClientSession, P) (R, error),
) typedMethodHandler[*ClientSession, P, R] {
	return func(ctx context.Context, cs *ClientSession, p P) (R, error) {
		return f(cs.client, ctx, cs, p)
	}
}

// sessionMethod is glue for creating a typedMethodHandler from a method on ServerSession.
func sessionMethod[S Session, P Params, R Result](f func(S, context.Context, P) (R, error)) typedMethodHandler[S, P, R] {
	return func(ctx context.Context, sess S, p P) (R, error) {
		return f(sess, ctx, p)
	}
}

// Error codes
const (
	CodeResourceNotFound = -32002
	// The error code if the method exists and was called properly, but the peer does not support it.
	CodeUnsupportedMethod = -31001
)

func callNotificationHandler[S Session, P any](ctx context.Context, h func(context.Context, S, *P), sess S, params *P) (Result, error) {
	if h != nil {
		h(ctx, sess, params)
	}
	return nil, nil
}

// notifySessions calls Notify on all the sessions.
// Should be called on a copy of the peer sessions.
func notifySessions[S Session](sessions []S, method string, params Params) {
	if sessions == nil {
		return
	}
	// TODO: make this timeout configurable, or call Notify asynchronously.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range sessions {
		if err := handleNotify(ctx, s, method, params); err != nil {
			// TODO(jba): surface this error better
			log.Printf("calling %s: %v", method, err)
		}
	}
}

// Meta is additional metadata for requests, responses and other types.
type Meta map[string]any

// GetMeta returns metadata from a value.
func (m Meta) GetMeta() map[string]any { return m }

// SetMeta sets the metadata on a value.
func (m *Meta) SetMeta(x map[string]any) { *m = x }

const progressTokenKey = "progressToken"

func getProgressToken(p Params) any {
	return p.GetMeta()[progressTokenKey]
}

func setProgressToken(p Params, pt any) {
	switch pt.(type) {
	// Support int32 and int64 for atomic.IntNN.
	case int, int32, int64, string:
	default:
		panic(fmt.Sprintf("progress token %v is of type %[1]T, not int or string", pt))
	}
	m := p.GetMeta()
	if m == nil {
		m = map[string]any{}
	}
	m[progressTokenKey] = pt
}

// Params is a parameter (input) type for an MCP call or notification.
type Params interface {
	// GetMeta returns metadata from a value.
	GetMeta() map[string]any
	// SetMeta sets the metadata on a value.
	SetMeta(map[string]any)
}

// RequestParams is a parameter (input) type for an MCP request.
type RequestParams interface {
	Params

	// GetProgressToken returns the progress token from the params' Meta field, or nil
	// if there is none.
	GetProgressToken() any

	// SetProgressToken sets the given progress token into the params' Meta field.
	// It panics if its argument is not an int or a string.
	SetProgressToken(any)
}

// Result is a result of an MCP call.
type Result interface {
	// GetMeta returns metadata from a value.
	GetMeta() map[string]any
	// SetMeta sets the metadata on a value.
	SetMeta(map[string]any)
}

// emptyResult is returned by methods that have no result, like ping.
// Those methods cannot return nil, because jsonrpc2 cannot handle nils.
type emptyResult struct{}

func (*emptyResult) GetMeta() map[string]any { panic("should never be called") }
func (*emptyResult) SetMeta(map[string]any)  { panic("should never be called") }

type listParams interface {
	// Returns a pointer to the param's Cursor field.
	cursorPtr() *string
}

type listResult[T any] interface {
	// Returns a pointer to the param's NextCursor field.
	nextCursorPtr() *string
}

// keepaliveSession represents a session that supports keepalive functionality.
type keepaliveSession interface {
	Ping(ctx context.Context, params *PingParams) error
	Close() error
}

// startKeepalive starts the keepalive mechanism for a session.
// It assigns the cancel function to the provided cancelPtr and starts a goroutine
// that sends ping messages at the specified interval.
func startKeepalive(session keepaliveSession, interval time.Duration, cancelPtr *context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	// Assign cancel function before starting goroutine to avoid race condition.
	// We cannot return it because the caller may need to cancel during the
	// window between goroutine scheduling and function return.
	*cancelPtr = cancel

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(context.Background(), interval/2)
				err := session.Ping(pingCtx, nil)
				pingCancel()
				if err != nil {
					// Ping failed, close the session
					_ = session.Close()
					return
				}
			}
		}
	}()
}
