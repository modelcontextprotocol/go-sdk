# Overview

The SDK consists of several importable packages:

- The
  [`github.com/modelcontextprotocol/go-sdk/mcp`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp)
  package defines the primary APIs for constructing and using MCP clients and
  servers.
- The
  [`github.com/modelcontextprotocol/go-sdk/jsonrpc`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/jsonrpc) package is for users implementing
  their own transports.
- The
  [`github.com/modelcontextprotocol/go-sdk/auth`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/auth)
  package provides some primitives for supporting OAuth.
- The
  [`github.com/modelcontextprotocol/go-sdk/auth/extauth`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/auth/extauth)
  package provides OAuth handlers for authorization extensions.
- The
  [`github.com/modelcontextprotocol/go-sdk/oauthex`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/oauthex)
  package provides extensions to the OAuth protocol, such as ProtectedResourceMetadata.
- The
  [`github.com/modelcontextprotocol/go-sdk/skills`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/skills)
  package provides opt-in Skills extension support for discovery, directory
  browsing, and content verification.


These docs describe the SDK's implementation of the
[MCP specification](https://modelcontextprotocol.io/specification/2026-07-28)
and optional extensions. See the [version compatibility table](../README.md#version-compatibility)
for supported protocol revisions. Use the index below to learn how the SDK
implements a particular feature.

## Base Protocol

1. [Lifecycle (Clients, Servers, and Sessions)](protocol.md#lifecycle).
1. [Transports](protocol.md#transports)
    1. [Stdio transport](protocol.md#stdio-transport)
    1. [Streamable transport](protocol.md#streamable-transport)
    1. [Custom transports](protocol.md#stateless-mode)
1. [Authorization](protocol.md#authorization)
1. [Security](protocol.md#security)
1. [Utilities](protocol.md#utilities)
    1. [Cancellation](protocol.md#cancellation)
    1. [Ping](protocol.md#ping)
    1. [Progress](protocol.md#progress)

## Client Features

1. [Roots](client.md#roots)
1. [Sampling](client.md#sampling)
1. [Elicitation](client.md#elicitation)
1. [Extensions](client.md#extensions)
    1. [Skills](client.md#skills-extension)

## Server Features

1. [Prompts](server.md#prompts)
1. [Resources](server.md#resources)
1. [Tools](server.md#tools)
1. [Extensions](server.md#extensions)
    1. [Skills](server.md#skills-extension)
1. [Utilities](server.md#utilities)
    1. [Completion](server.md#completion)
    1. [Logging](server.md#logging)
    1. [Pagination](server.md#pagination)

# TroubleShooting

See [troubleshooting.md](troubleshooting.md) for a troubleshooting guide.

# Backwards compatibility

See [mcpgodebug.md](mcpgodebug.md) for a list of backwards incompatible behavior changes
and description how they can be temporarily undone.

# Rough edges

See [rough_edges.md](rough_edges.md) for a list of rough edges or API
oversights that can't be addressed due to our compatibility promise. We'll
revisit these if/when we move to a v2 of the SDK.
