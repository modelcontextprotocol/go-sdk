// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file holds the request types.

package mcp

// TODO: expand the aliases
type (
	CallToolRequest              = ServerRequest[*CallToolParams]
	CompleteRequest              = ServerRequest[*CompleteParams]
	GetPromptRequest             = ServerRequest[*GetPromptParams]
	InitializedRequest           = ServerRequest[*InitializedParams]
	ListPromptsRequest           = ServerRequest[*ListPromptsParams]
	ListResourcesRequest         = ServerRequest[*ListResourcesParams]
	ListResourceTemplatesRequest = ServerRequest[*ListResourceTemplatesParams]
	ListToolsRequest             = ServerRequest[*ListToolsParams]
	ProgressNotificationRequest  = ServerRequest[*ProgressNotificationParams]
	ReadResourceRequest          = ServerRequest[*ReadResourceParams]
	RootsListChangedRequest      = ServerRequest[*RootsListChangedParams]
	SubscribeRequest             = ServerRequest[*SubscribeParams]
	UnsubscribeRequest           = ServerRequest[*UnsubscribeParams]
)
