// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListSkillsHandler handles skills/list requests.
type ListSkillsHandler func(context.Context, *mcp.ServerSession, *ListSkillsParams) (*ListSkillsResult, error)

// GetSkillHandler handles skills/get requests.
type GetSkillHandler func(context.Context, *mcp.ServerSession, *GetSkillParams) (*GetSkillResult, error)

// ReadDirectoryHandler handles resources/directory/read requests.
type ReadDirectoryHandler func(context.Context, *mcp.ServerSession, *ReadDirectoryParams) (*ReadDirectoryResult, error)

// UnsafeOptions permits behavior that may not interoperate with conforming hosts.
type UnsafeOptions struct {
	DisableDefaultValidation bool
	Limits                   *Limits
}

// ServerOptions configures handler validation.
type ServerOptions struct {
	SkillValidators     []func(context.Context, *Skill) error
	ListValidators      []func(context.Context, *ListSkillsResult) error
	DirectoryValidators []func(context.Context, *ReadDirectoryResult) error
	Unsafe              *UnsafeOptions
}

// Handlers contains the required and optional Skills extension handlers.
type Handlers struct {
	List          ListSkillsHandler
	Get           GetSkillHandler
	ReadDirectory ReadDirectoryHandler
}

// AddHandlers registers the Skills extension handlers on server.
func AddHandlers(server *mcp.Server, handlers *Handlers, options *ServerOptions) error {
	if server == nil {
		return fmt.Errorf("skills: nil server")
	}
	if handlers == nil || handlers.List == nil || handlers.Get == nil {
		return fmt.Errorf("skills: list and get handlers are required")
	}
	handlers = &Handlers{List: handlers.List, Get: handlers.Get, ReadDirectory: handlers.ReadDirectory}
	options = cloneServerOptions(options)
	if err := mcp.AddReceivingCustomMethod(server, MethodList,
		func(ctx context.Context, session *mcp.ServerSession, params *ListSkillsParams) (*ListSkillsResult, error) {
			if params == nil {
				params = &ListSkillsParams{}
			}
			result, err := handlers.List(ctx, session, params)
			if err != nil {
				return nil, err
			}
			if err := validateListResult(ctx, result, options); err != nil {
				return nil, fmt.Errorf("skills/list handler returned an invalid result: %w", err)
			}
			if supportsListCaching(session, params.Meta) {
				if result.CacheScope == "" {
					result.CacheScope = "public"
				}
			} else {
				result.omitCache = true
			}
			return result, nil
		}); err != nil {
		return err
	}
	if err := mcp.AddReceivingCustomMethod(server, MethodGet,
		func(ctx context.Context, session *mcp.ServerSession, params *GetSkillParams) (*GetSkillResult, error) {
			if params == nil || params.URI == "" {
				return nil, invalidParams("missing required uri")
			}
			if _, err := skillNameFromURI(params.URI); err != nil {
				return nil, invalidParams(err.Error())
			}
			result, err := handlers.Get(ctx, session, params)
			if err != nil {
				return nil, err
			}
			if result == nil || result.Skill == nil {
				return nil, fmt.Errorf("skills/get handler returned a nil skill")
			}
			if result.Skill.URI != params.URI {
				return nil, fmt.Errorf("skills/get handler returned URI %q for %q", result.Skill.URI, params.URI)
			}
			if err := validateSkillResult(ctx, result.Skill, options); err != nil {
				return nil, fmt.Errorf("skills/get handler returned an invalid result: %w", err)
			}
			result.ResultType = "complete"
			return result, nil
		}); err != nil {
		return err
	}
	settings := map[string]any{}
	if handlers.ReadDirectory != nil {
		if err := mcp.AddReceivingCustomMethod(server, MethodReadDirectory,
			func(ctx context.Context, session *mcp.ServerSession, params *ReadDirectoryParams) (*ReadDirectoryResult, error) {
				if params == nil || params.URI == "" {
					return nil, invalidParams("missing required uri")
				}
				if _, err := parseDirectoryURI(params.URI); err != nil {
					return nil, invalidParams(err.Error())
				}
				result, err := handlers.ReadDirectory(ctx, session, params)
				if err != nil {
					return nil, err
				}
				if err := validateDirectoryResult(ctx, params.URI, result, options); err != nil {
					return nil, fmt.Errorf("resources/directory/read handler returned an invalid result: %w", err)
				}
				return result, nil
			}); err != nil {
			return err
		}
		settings["directoryRead"] = true
	}
	server.AddExtension(ExtensionID, settings)
	return nil
}

func cloneServerOptions(options *ServerOptions) *ServerOptions {
	if options == nil {
		return nil
	}
	cloned := *options
	cloned.SkillValidators = append([]func(context.Context, *Skill) error(nil), options.SkillValidators...)
	cloned.ListValidators = append([]func(context.Context, *ListSkillsResult) error(nil), options.ListValidators...)
	cloned.DirectoryValidators = append([]func(context.Context, *ReadDirectoryResult) error(nil), options.DirectoryValidators...)
	if options.Unsafe != nil {
		unsafe := *options.Unsafe
		if options.Unsafe.Limits != nil {
			limits := *options.Unsafe.Limits
			unsafe.Limits = &limits
		}
		cloned.Unsafe = &unsafe
	}
	return &cloned
}

func validateListResponse(ctx context.Context, result *ListSkillsResult) error {
	if result == nil {
		return fmt.Errorf("result is nil")
	}
	if result.Skills == nil {
		return fmt.Errorf("skills is missing or null")
	}
	seen := make(map[string]bool, len(result.Skills))
	for _, skill := range result.Skills {
		if err := validateSkillResult(ctx, skill, nil); err != nil {
			return err
		}
		if seen[skill.URI] {
			return fmt.Errorf("skill URI %q occurs more than once", skill.URI)
		}
		seen[skill.URI] = true
	}
	return nil
}

func supportsListCaching(session *mcp.ServerSession, meta mcp.Meta) bool {
	version, _ := meta[mcp.MetaKeyProtocolVersion].(string)
	if version == "" && session != nil {
		if params := session.InitializeParams(); params != nil {
			version = params.ProtocolVersion
		}
	}
	return version >= "2026-07-28"
}

func validateListResult(ctx context.Context, result *ListSkillsResult, options *ServerOptions) error {
	if result == nil {
		return fmt.Errorf("result is nil")
	}
	if result.Skills == nil {
		result.Skills = []*Skill{}
	}
	seen := make(map[string]bool, len(result.Skills))
	for _, skill := range result.Skills {
		if err := validateSkillResult(ctx, skill, options); err != nil {
			return err
		}
		if seen[skill.URI] {
			return fmt.Errorf("skill URI %q occurs more than once", skill.URI)
		}
		seen[skill.URI] = true
	}
	if options != nil {
		if err := runValidators(ctx, options.ListValidators, result); err != nil {
			return err
		}
	}
	result.ResultType = "complete"
	return nil
}

func validateSkillResult(ctx context.Context, skill *Skill, options *ServerOptions) error {
	defaultValidation, limits := validationSettings(options)
	if defaultValidation {
		if err := ValidateSkillWithLimits(skill, limits); err != nil {
			return err
		}
	}
	if options != nil {
		return runValidators(ctx, options.SkillValidators, skill)
	}
	return nil
}

func validationSettings(options *ServerOptions) (bool, Limits) {
	limits := DefaultLimits()
	if options == nil || options.Unsafe == nil {
		return true, limits
	}
	if options.Unsafe.Limits != nil {
		limits = *options.Unsafe.Limits
	}
	return !options.Unsafe.DisableDefaultValidation, limits
}

func validateDirectoryResult(ctx context.Context, uri string, result *ReadDirectoryResult, options *ServerOptions) error {
	defaultValidation, _ := validationSettings(options)
	if defaultValidation {
		if err := ValidateDirectoryResult(uri, result); err != nil {
			return err
		}
	}
	if result != nil {
		result.ResultType = "complete"
	}
	if options != nil {
		return runValidators(ctx, options.DirectoryValidators, result)
	}
	return nil
}

func runValidators[T any](ctx context.Context, validators []func(context.Context, T) error, value T) error {
	for _, validate := range validators {
		if validate != nil {
			if err := validate(ctx, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func invalidParams(message string) error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: message}
}
