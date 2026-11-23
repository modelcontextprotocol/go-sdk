// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"fmt"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListSkillsHandler handles skills/list requests.
type ListSkillsHandler func(context.Context, *mcp.ServerSession, *ListSkillsParams) (*ListSkillsResult, error)

// GetSkillHandler handles skills/get. Return (nil, nil) for an unknown skill;
// [AddHandlers] translates it to JSON-RPC Invalid Params. Other errors pass through.
type GetSkillHandler func(context.Context, *mcp.ServerSession, *GetSkillParams) (*GetSkillResult, error)

// ReadDirectoryHandler handles resources/directory/read. Return (nil, nil) if
// the URI does not exist or is not a directory. An empty directory has a non-nil result.
type ReadDirectoryHandler func(context.Context, *mcp.ServerSession, *ReadDirectoryParams) (*ReadDirectoryResult, error)

// ServerOptions configures the per-skill limits. Protocol validation always
// runs; applications can perform additional checks in their handlers.
type ServerOptions struct {
	Limits Limits
}

// Handlers contains the Skills extension handlers.
// List and Get are required; ReadDirectory is optional.
type Handlers struct {
	List          ListSkillsHandler
	Get           GetSkillHandler
	ReadDirectory ReadDirectoryHandler
}

// AddHandlers registers the Skills extension. Register skill content separately
// with [mcp.Server.AddResource] or [mcp.Server.AddResourceTemplate], which also advertises
// the required resources capability. Configure the server before connecting.
//
// If options is nil, the default limits apply. Handlers own pagination; use
// [PaginateSkills] or [PaginateDirectoryResources] to paginate in-memory slices.
// AddHandlers supplies resultType and default cache hints for the request's
// protocol version. See [ListSkillsResult] and [GetSkillResult].
//
// Options and handler functions are copied. Results are validated without
// modifying handler-owned values; handlers must synchronize their own state.
func AddHandlers(server *mcp.Server, handlers *Handlers, options *ServerOptions) error {
	if server == nil {
		return fmt.Errorf("skills: nil server")
	}
	if handlers == nil || handlers.List == nil || handlers.Get == nil {
		return fmt.Errorf("skills: list and get handlers are required")
	}
	h := *handlers
	var limits Limits
	if options != nil {
		limits = options.Limits
	}
	limits, err := limits.resolve()
	if err != nil {
		return err
	}
	if err := mcp.AddReceivingCustomMethod(server, MethodList,
		func(ctx context.Context, session *mcp.ServerSession, params *ListSkillsParams) (*ListSkillsResult, error) {
			if params == nil {
				params = &ListSkillsParams{}
			}
			result, err := h.List(ctx, session, params)
			if err != nil {
				return nil, err
			}
			if result == nil {
				return nil, fmt.Errorf("skills/list handler returned a nil result")
			}
			out := *result
			out.Meta = maps.Clone(result.Meta)
			if out.Skills == nil {
				out.Skills = []*Skill{}
			}
			if err := validateListResult(&out, limits); err != nil {
				return nil, fmt.Errorf("skills/list handler returned an invalid result: %w", err)
			}
			out.omitCache = !supportsCaching(params.Meta)
			out.ResultType = resultType(params.Meta)
			normalizeCache(&out.Cacheable)
			if err := validateCache(out.Cacheable); err != nil {
				return nil, err
			}
			return &out, nil
		}); err != nil {
		return err
	}
	if err := mcp.AddReceivingCustomMethod(server, MethodGet,
		func(ctx context.Context, session *mcp.ServerSession, params *GetSkillParams) (*GetSkillResult, error) {
			if params == nil {
				return nil, invalidParams("missing required uri")
			}
			if _, err := skillNameFromURI(params.URI); err != nil {
				return nil, invalidParams(err.Error())
			}
			result, err := h.Get(ctx, session, params)
			if err != nil {
				return nil, err
			}
			if result == nil || result.Skill == nil {
				return nil, invalidParams("unknown skill: " + params.URI)
			}
			if err := validateGetResult(params.URI, result, limits); err != nil {
				return nil, fmt.Errorf("skills/get handler returned an invalid result: %w", err)
			}
			out := *result
			out.Meta = maps.Clone(result.Meta)
			out.omitCache = !supportsCaching(params.Meta)
			out.ResultType = resultType(params.Meta)
			normalizeCache(&out.Cacheable)
			if err := validateCache(out.Cacheable); err != nil {
				return nil, err
			}
			return &out, nil
		}); err != nil {
		return err
	}
	settings := map[string]any{}
	if h.ReadDirectory != nil {
		if err := mcp.AddReceivingCustomMethod(server, MethodReadDirectory,
			func(ctx context.Context, session *mcp.ServerSession, params *ReadDirectoryParams) (*ReadDirectoryResult, error) {
				if params == nil {
					return nil, invalidParams("missing required uri")
				}
				if _, err := parseDirectoryURI(params.URI); err != nil {
					return nil, invalidParams(err.Error())
				}
				result, err := h.ReadDirectory(ctx, session, params)
				if err != nil {
					return nil, err
				}
				if result == nil {
					return nil, invalidParams("unknown directory: " + params.URI)
				}
				out := *result
				out.Meta = maps.Clone(result.Meta)
				if out.Resources == nil {
					out.Resources = []*mcp.Resource{}
				}
				if err := ValidateDirectoryResult(params.URI, &out); err != nil {
					return nil, fmt.Errorf("resources/directory/read handler returned an invalid result: %w", err)
				}
				out.ResultType = resultType(params.Meta)
				return &out, nil
			}); err != nil {
			return err
		}
		settings[capabilityDirectoryRead] = true
	}
	server.AddExtension(ExtensionID, settings)
	return nil
}

// Modern requests carry the validated protocol version in _meta. InitializeParams
// contains the client's proposal, which can differ from the negotiated version.
func supportsCaching(meta mcp.Meta) bool {
	version, _ := meta[mcp.MetaKeyProtocolVersion].(string)
	return version >= "2026-07-28"
}

func resultType(meta mcp.Meta) string {
	if supportsCaching(meta) {
		return "complete"
	}
	return ""
}

func normalizeCache(cache *mcp.Cacheable) {
	if cache.CacheScope == "" {
		cache.CacheScope = "public"
	}
}

func invalidParams(message string) error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: message}
}
