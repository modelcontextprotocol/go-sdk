// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
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
	setResultType(ResultType)
	inputRequests() map[string]InputRequest
	setInputRequest(k string, v InputRequest)
	hasContent() bool
}

func handleMRTRResult(ss *ServerSession, logger *slog.Logger, res mrtrResult) error {
	hasInputRequests := res.inputRequests() != nil

	protocolVersion := latestProtocolVersion
	if iparams := ss.InitializeParams(); iparams != nil {
		protocolVersion = iparams.ProtocolVersion
	}
	supportsMRTR := protocolVersion >= protocolVersion20260630

	switch {
	case hasInputRequests && !supportsMRTR:
		return fmt.Errorf("protocol version %q does not support input requests (< %q)", protocolVersion, protocolVersion20260630)

	case hasInputRequests && res.hasContent():
		logger.Warn("handler returned both content and inputRequests; inputRequests takes precedence")
		return &jsonrpc.Error{
			Code:    jsonrpc.CodeInternalError,
			Message: "server bug: result has both content and inputRequests",
		}

	case hasInputRequests:
		res.setResultType(ResultTypeInputRequired)

	case supportsMRTR:
		res.setResultType(ResultTypeComplete)
	}
	return nil
}

func mrtrMiddleware(c *Client) Middleware {
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

func mrtrInputRequests(res Result) InputRequestMap {
	switch r := res.(type) {
	case *CallToolResult:
		if r.NeedsInput() {
			return r.InputRequests
		}
	case *GetPromptResult:
		if r.NeedsInput() {
			return r.InputRequests
		}
	case *ReadResourceResult:
		if r.NeedsInput() {
			return r.InputRequests
		}
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
	case *GetPromptParams:
		p.InputResponses = responses
		p.RequestState = state
	case *ReadResourceParams:
		p.InputResponses = responses
		p.RequestState = state
	}
}

func fulfillInputRequests(ctx context.Context, cs *ClientSession, requests InputRequestMap) (InputResponseMap, error) {
	responses := make(InputResponseMap)
	for id, ir := range requests {
		resp, err := fulfillInputRequest(ctx, cs, ir)
		if err != nil {
			return nil, fmt.Errorf("MRTR: fulfilling input request %q: %w", id, err)
		}
		responses[id] = resp
	}
	return responses, nil
}

func fulfillInputRequest(ctx context.Context, cs *ClientSession, ir InputRequest) (InputResponse, error) {
	switch p := ir.(type) {
	case *ElicitParams:
		return cs.client.elicit(ctx, newClientRequest(cs, p))
	case *CreateMessageParams:
		return fulfillCreateMessage(ctx, cs, p)
	case *ListRootsParams:
		return cs.client.listRoots(ctx, newClientRequest(cs, p))
	default:
		return nil, fmt.Errorf("unknown input request type: %T", ir)
	}
}

func fulfillCreateMessage(ctx context.Context, cs *ClientSession, p *CreateMessageParams) (*CreateMessageResult, error) {
	if cs.client.opts.CreateMessageHandler != nil {
		return cs.client.opts.CreateMessageHandler(ctx, &CreateMessageRequest{Session: cs, Params: p})
	}
	if cs.client.opts.CreateMessageWithToolsHandler != nil {
		var msgs []*SamplingMessageV2
		for _, m := range p.Messages {
			msgs = append(msgs, &SamplingMessageV2{Content: []Content{m.Content}, Role: m.Role})
		}
		wtp := &CreateMessageWithToolsParams{
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
		result, err := cs.client.opts.CreateMessageWithToolsHandler(ctx, &CreateMessageWithToolsRequest{Session: cs, Params: wtp})
		if err != nil {
			return nil, err
		}
		var content Content
		if len(result.Content) > 0 {
			content = result.Content[0]
		}
		return &CreateMessageResult{
			Meta:       result.Meta,
			Content:    content,
			Model:      result.Model,
			Role:       result.Role,
			StopReason: result.StopReason,
		}, nil
	}
	return nil, fmt.Errorf("client does not support CreateMessage")
}
