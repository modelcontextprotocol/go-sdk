// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"golang.org/x/sync/errgroup"
)

const defaultMRTRMaxRetries = 3

// MRTROptions configures the client-side MRTR (Multi Round-Trip Requests)
// middleware. The middleware is enabled by default and automatically fulfills input
// requests from the server by invoking the appropriate client handlers and
// retrying the original call.
type MRTROptions struct {
	// MaxRetries is the maximum number of MRTR retry attempts after the
	// initial call. Defaults to 3 if the provided value is <= 0.
	MaxRetries int
	// Disabled prevents the automatic MRTR middleware from being installed.
	// When true, the client returns input-required results directly and
	// callers must handle the retry loop themselves using [CallToolResult.NeedsInput],
	// [GetPromptResult.NeedsInput], or [ReadResourceResult.NeedsInput].
	Disabled bool
}

type mrtrResult interface {
	setResultType(resultType)
	inputRequests() map[string]InputRequest
	hasContent() bool
}

func handleMRTRResult(ss *ServerSession, logger *slog.Logger, res mrtrResult) error {
	if res == nil {
		return nil
	}
	hasInputRequests := res.inputRequests() != nil

	if hasInputRequests && res.hasContent() {
		logger.Warn("handler returned both content and inputRequests")
		return &jsonrpc.Error{
			Code:    jsonrpc.CodeInternalError,
			Message: "server bug: result has both content and inputRequests",
		}
	}

	supportsMRTR := sessionSupportsMRTR(ss)

	switch {
	case hasInputRequests && supportsMRTR:
		res.setResultType(resultTypeInputRequired)
	case supportsMRTR:
		res.setResultType(resultTypeComplete)
	}
	// For older clients the resultType is left unset. The serverMRTRMiddleware fulfills the
	// requests by calling the client directly and retries the handler.
	return nil
}

func sessionSupportsMRTR(ss *ServerSession) bool {
	protocolVersion := latestProtocolVersion
	if iparams := ss.InitializeParams(); iparams != nil {
		protocolVersion = iparams.ProtocolVersion
	}
	return protocolVersion >= protocolVersion20260630
}

func clientMRTRMiddleware(c *Client) Middleware {
	return func(next MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			if method != methodCallTool && method != methodGetPrompt && method != methodReadResource {
				return next(ctx, method, req)
			}

			maxRetries := defaultMRTRMaxRetries
			if c.opts.MRTR != nil && c.opts.MRTR.MaxRetries > 0 {
				maxRetries = c.opts.MRTR.MaxRetries
			}

			for retries := 0; ; retries++ {
				res, err := next(ctx, method, req)
				if err != nil {
					return res, err
				}
				irm := mrtrInputRequests(res)
				if len(irm) == 0 {
					return res, nil
				}
				if retries >= maxRetries {
					return nil, fmt.Errorf("MRTR: exceeded maximum retries (%d)", maxRetries)
				}
				cs, ok := req.GetSession().(*ClientSession)
				if !ok {
					return res, nil
				}
				responses, err := fulfillInputRequests(ctx, cs, irm)
				if err != nil {
					return nil, err
				}
				setMRTRRetryParams(req, responses, mrtrRequestState(res))
			}
		}
	}
}

// serverMRTRMiddleware is a receiving middleware for servers that transparently
// handles MRTR for clients on older protocol versions. When a handler returns
// InputRequests and the client does not support MRTR, the middleware fulfills
// the requests by calling the client directly and reinvokes the handler once with the responses.
func serverMRTRMiddleware() Middleware {
	return func(next MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			if method != methodCallTool && method != methodGetPrompt && method != methodReadResource {
				return next(ctx, method, req)
			}
			ss, ok := req.GetSession().(*ServerSession)
			if !ok {
				return next(ctx, method, req)
			}
			if sessionSupportsMRTR(ss) {
				return next(ctx, method, req)
			}

			res, err := next(ctx, method, req)
			if err != nil {
				return res, err
			}
			irm := serverMRTRInputRequests(res)
			if len(irm) == 0 {
				return res, nil
			}
			responses, err := fulfillServerInputRequests(ctx, ss, irm)
			if err != nil {
				return nil, err
			}
			setMRTRRetryParams(req, responses, mrtrRequestState(res))
			return next(ctx, method, req)
		}
	}
}

// serverMRTRInputRequests returns input requests from a result for old clients
// where resultType is not set. It checks InputRequests directly.
func serverMRTRInputRequests(res Result) InputRequestMap {
	if res == nil {
		return nil
	}
	switch r := res.(type) {
	case *CallToolResult:
		if r == nil {
			return nil
		}
		return r.InputRequests
	case *GetPromptResult:
		if r == nil {
			return nil
		}
		return r.InputRequests
	case *ReadResourceResult:
		if r == nil {
			return nil
		}
		return r.InputRequests
	}
	return nil
}

func fulfillServerInputRequests(ctx context.Context, ss *ServerSession, requests InputRequestMap) (InputResponseMap, error) {
	g, ctx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	responses := make(InputResponseMap, len(requests))
	for id, ir := range requests {
		g.Go(func() error {
			resp, err := fulfillServerInputRequest(ctx, ss, ir)
			if err != nil {
				return fmt.Errorf("fulfilling input request %q: %w", id, err)
			}
			mu.Lock()
			responses[id] = resp
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("MRTR: %w", err)
	}
	return responses, nil
}

func fulfillServerInputRequest(ctx context.Context, ss *ServerSession, ir InputRequest) (InputResponse, error) {
	switch p := ir.(type) {
	case *ElicitParams:
		return ss.Elicit(ctx, p)
	case *CreateMessageParams:
		return ss.CreateMessageWithTools(ctx, createMessageParamsToWithTools(p))
	case *CreateMessageWithToolsParams:
		return ss.CreateMessageWithTools(ctx, p)
	case *ListRootsParams:
		return ss.ListRoots(ctx, p)
	default:
		return nil, fmt.Errorf("unknown input request type: %T", ir)
	}
}

func createMessageParamsToWithTools(p *CreateMessageParams) *CreateMessageWithToolsParams {
	var msgs []*SamplingMessageV2
	for _, m := range p.Messages {
		msgs = append(msgs, &SamplingMessageV2{Content: []Content{m.Content}, Role: m.Role})
	}
	return &CreateMessageWithToolsParams{
		Meta:             p.Meta,
		IncludeContext:   p.IncludeContext,
		MaxTokens:        p.MaxTokens,
		Messages:         msgs,
		Metadata:         p.Metadata,
		ModelPreferences: p.ModelPreferences,
		StopSequences:    p.StopSequences,
		SystemPrompt:     p.SystemPrompt,
		Temperature:      p.Temperature,
	}
}

func mrtrInputRequests(res Result) InputRequestMap {
	if res == nil {
		return nil
	}
	switch r := res.(type) {
	case *CallToolResult:
		if r == nil || !r.NeedsInput() {
			return nil
		}
		return r.InputRequests
	case *GetPromptResult:
		if r == nil || !r.NeedsInput() {
			return nil
		}
		return r.InputRequests
	case *ReadResourceResult:
		if r == nil || !r.NeedsInput() {
			return nil
		}
		return r.InputRequests
	}
	return nil
}

func mrtrRequestState(res Result) string {
	switch r := res.(type) {
	case *CallToolResult:
		return r.RequestState
	case *GetPromptResult:
		return r.RequestState
	case *ReadResourceResult:
		return r.RequestState
	}
	return ""
}

func setMRTRRetryParams(req Request, responses InputResponseMap, state string) {
	switch p := req.GetParams().(type) {
	case *CallToolParams:
		p.InputResponses = responses
		p.RequestState = state
	case *CallToolParamsRaw:
		p.InputResponses = responses
		p.RequestState = state
	case *GetPromptParams:
		p.InputResponses = responses
		p.RequestState = state
	case *ReadResourceParams:
		p.InputResponses = responses
		p.RequestState = state
	}
}

func fulfillInputRequests(ctx context.Context, cs *ClientSession, requests InputRequestMap) (InputResponseMap, error) {
	g, ctx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	responses := make(InputResponseMap, len(requests))
	for id, ir := range requests {
		g.Go(func() error {
			resp, err := fulfillInputRequest(ctx, cs, ir)
			if err != nil {
				return fmt.Errorf("fulfilling input request %q: %w", id, err)
			}
			mu.Lock()
			responses[id] = resp
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("MRTR: %w", err)
	}
	return responses, nil
}

func fulfillInputRequest(ctx context.Context, cs *ClientSession, ir InputRequest) (InputResponse, error) {
	switch p := ir.(type) {
	case *ElicitParams:
		return cs.client.elicit(ctx, newClientRequest(cs, p))
	case *CreateMessageParams:
		return cs.client.createMessage(ctx, &CreateMessageWithToolsRequest{Session: cs, Params: createMessageParamsToWithTools(p)})
	case *CreateMessageWithToolsParams:
		return cs.client.createMessage(ctx, &CreateMessageWithToolsRequest{Session: cs, Params: p})
	case *ListRootsParams:
		return cs.client.listRoots(ctx, newClientRequest(cs, p))
	default:
		return nil, fmt.Errorf("unknown input request type: %T", ir)
	}
}
