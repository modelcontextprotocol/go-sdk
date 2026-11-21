// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Request types for Skills server handlers.
type (
	ListSkillsRequest    = mcp.ServerRequest[*ListSkillsParams]
	GetSkillRequest      = mcp.ServerRequest[*GetSkillParams]
	ReadDirectoryRequest = mcp.ServerRequest[*ReadDirectoryParams]
)

// ListSkillsHandler handles skills/list requests.
type ListSkillsHandler func(context.Context, *ListSkillsRequest) (*ListSkillsResult, error)

// GetSkillHandler handles skills/get. Return (nil, nil) for an unknown skill;
// [AddHandlers] translates it to JSON-RPC Invalid Params. Explicit JSON-RPC errors
// are preserved; other errors become Internal Error.
type GetSkillHandler func(context.Context, *GetSkillRequest) (*GetSkillResult, error)

// ReadDirectoryHandler handles resources/directory/read. Return (nil, nil) if
// the URI does not exist or is not a directory. An empty directory has a non-nil result.
type ReadDirectoryHandler func(context.Context, *ReadDirectoryRequest) (*ReadDirectoryResult, error)

// ServerOptions configures the per-skill limits. Protocol validation always
// runs; applications can perform additional checks in their handlers.
type ServerOptions struct {
	// Limits optionally bounds static manifests; zero fields impose no caps.
	// Use [BaselineLimits] to opt into the spec's interoperability baseline.
	// AddHandlers copies the value during registration.
	Limits Limits
}

// Handlers contains the Skills extension handlers.
// List and Get are required; ReadDirectory is optional.
type Handlers struct {
	List          ListSkillsHandler
	Get           GetSkillHandler
	ReadDirectory ReadDirectoryHandler
}

// Add serves a static catalog with URI-based lookup and pagination. Pass the
// complete catalog in one call; subsequent registrations replace its handlers.
// Entries and their manifests must remain immutable while the server serves them.
func Add(server *mcp.Server, entries []*Skill, options *ServerOptions) error {
	catalog := slices.Clone(entries)
	byURI := make(map[string]*Skill, len(catalog))
	var limits Limits
	if options != nil {
		limits = options.Limits
	}
	for _, entry := range catalog {
		if err := validateSkill(entry, limits); err != nil {
			return err
		}
		if byURI[entry.URI] != nil {
			return fmt.Errorf("skills: duplicate URI %q", entry.URI)
		}
		byURI[entry.URI] = entry
	}
	return AddHandlers(server, &Handlers{
		List: func(_ context.Context, req *ListSkillsRequest) (*ListSkillsResult, error) {
			page, next, err := paginateSkills(catalog, req.Params.Cursor, 0)
			return &ListSkillsResult{Skills: page, NextCursor: next}, err
		},
		Get: func(_ context.Context, req *GetSkillRequest) (*GetSkillResult, error) {
			return &GetSkillResult{Skill: byURI[req.Params.URI]}, nil
		},
	}, options)
}

// AddHandlers registers the Skills extension. Register skill content separately
// with [mcp.Server.AddResource] or [mcp.Server.AddResourceTemplate] before connecting.
// Advertise ExtensionID in the server options. Resource registrations advertise
// the required resources capability. Configure the server before connecting.
//
// If options is nil, no manifest caps apply. Handlers own pagination; use
// [PaginateDirectoryResources] to paginate in-memory slices.
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
	if err := limits.validate(); err != nil {
		return err
	}
	if err := mcp.AddReceivingCustomRequestHandler(server, MethodList,
		func(ctx context.Context, req *ListSkillsRequest) (*ListSkillsResult, error) {
			params := req.Params
			if params == nil {
				params = &ListSkillsParams{}
				copyReq := *req
				copyReq.Params = params
				req = &copyReq
			}
			result, err := h.List(ctx, req)
			if err != nil {
				return nil, internalError(err)
			}
			if result == nil {
				return nil, internalError(fmt.Errorf("skills/list handler returned a nil result"))
			}
			out := *result
			out.Meta = maps.Clone(result.Meta)
			if out.Skills == nil {
				out.Skills = []*Skill{}
			}
			if err := validateListResult(&out, limits); err != nil {
				return nil, internalError(fmt.Errorf("skills/list handler returned an invalid result: %w", err))
			}
			if err := stampEnvelope(params.Meta, &out.ResultType, &out.omitCache, &out.Cacheable); err != nil {
				return nil, internalError(err)
			}
			return &out, nil
		}); err != nil {
		return err
	}
	if err := mcp.AddReceivingCustomRequestHandler(server, MethodGet,
		func(ctx context.Context, req *GetSkillRequest) (*GetSkillResult, error) {
			params := req.Params
			if params == nil {
				return nil, invalidParams("missing required uri")
			}
			if _, err := skillNameFromURI(params.URI); err != nil {
				return nil, invalidParams(err.Error())
			}
			result, err := h.Get(ctx, req)
			if err != nil {
				return nil, internalError(err)
			}
			if result == nil || result.Skill == nil {
				return nil, invalidParams("unknown skill: " + params.URI)
			}
			if err := validateGetResult(params.URI, result, limits); err != nil {
				return nil, internalError(fmt.Errorf("skills/get handler returned an invalid result: %w", err))
			}
			out := *result
			out.Meta = maps.Clone(result.Meta)
			if err := stampEnvelope(params.Meta, &out.ResultType, &out.omitCache, &out.Cacheable); err != nil {
				return nil, internalError(err)
			}
			return &out, nil
		}); err != nil {
		return err
	}

	if h.ReadDirectory != nil {
		if err := mcp.AddReceivingCustomRequestHandler(server, MethodReadDirectory,
			func(ctx context.Context, req *ReadDirectoryRequest) (*ReadDirectoryResult, error) {
				params := req.Params
				if params == nil {
					return nil, invalidParams("missing required uri")
				}
				if _, err := parseDirectoryURI(params.URI); err != nil {
					return nil, invalidParams(err.Error())
				}
				result, err := h.ReadDirectory(ctx, req)
				if err != nil {
					return nil, internalError(err)
				}
				if result == nil {
					return nil, invalidParams("unknown directory: " + params.URI)
				}
				out := *result
				out.Meta = maps.Clone(result.Meta)
				if out.Resources == nil {
					out.Resources = []*mcp.Resource{}
				}
				if err := validateDirectoryResult(params.URI, &out); err != nil {
					return nil, internalError(fmt.Errorf("resources/directory/read handler returned an invalid result: %w", err))
				}
				if err := stampEnvelope(params.Meta, &out.ResultType, &out.omitCache, &out.Cacheable); err != nil {
					return nil, internalError(err)
				}
				return &out, nil
			}); err != nil {
			return err
		}
	}
	return nil
}

// Modern requests carry the validated protocol version in _meta. InitializeParams
// contains the client's proposal, which can differ from the negotiated version.
func supportsCaching(meta mcp.Meta) bool {
	version, _ := meta[mcp.MetaKeyProtocolVersion].(string)
	return version >= protocolVersionCaching
}

// stampEnvelope fills in the result type and cache hints that the request's
// protocol version calls for, and validates the hints it settled on.
func stampEnvelope(meta mcp.Meta, resultType *string, omitCache *bool, cache *mcp.Cacheable) error {
	caching := supportsCaching(meta)
	*omitCache = !caching
	if caching {
		*resultType = resultTypeComplete
	}
	if cache.CacheScope == "" {
		cache.CacheScope = cacheScopePublic
	}
	return validateCache(*cache)
}

func invalidParams(message string) error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: message}
}

func internalError(err error) error {
	var rpc *jsonrpc.Error
	if errors.As(err, &rpc) {
		return &jsonrpc.Error{Code: rpc.Code, Message: err.Error(), Data: rpc.Data}
	}
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: err.Error()}
}
