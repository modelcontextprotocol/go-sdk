# MCP Go SDK v0.3.0

[![Open in GitHub Codespaces](https://github.com/codespaces/badge.svg)](https://codespaces.new/modelcontextprotocol/go-sdk)

***BREAKING CHANGES***

This version contains breaking changes.
See the [release notes](
https://github.com/modelcontextprotocol/go-sdk/releases/tag/v0.3.0) for details.

[![PkgGoDev](https://pkg.go.dev/badge/github.com/modelcontextprotocol/go-sdk)](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk)

This repository contains an unreleased implementation of the official Go
software development kit (SDK) for the Model Context Protocol (MCP).

> [!WARNING]
> The SDK is not yet at v1.0.0 and may still be subject to incompatible API
> changes. We aim to tag v1.0.0 in September, 2025. See
> https://github.com/modelcontextprotocol/go-sdk/issues/328 for details.

## Package documentation

The SDK consists of two importable packages:

- The
  [`github.com/modelcontextprotocol/go-sdk/mcp`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp)
  package defines the primary APIs for constructing and using MCP clients and
  servers.
- The
  [`github.com/modelcontextprotocol/go-sdk/jsonrpc`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/jsonrpc) package is for users implementing
  their own transports.
- The
  [`github.com/modelcontextprotocol/go-sdk/auth`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/auth)
  package provides some primitives for supporting oauth.

## Getting started

To get started creating an MCP server, create an `mcp.Server` instance, add
features to it, and then run it over an `mcp.Transport`. For example, this
server adds a single simple tool, and then connects it to clients over
stdin/stdout:

%include server/server.go -

To communicate with that server, we can similarly create an `mcp.Client` and
connect it to the corresponding server, by running the server command and
communicating over its stdin/stdout:

%include client/client.go -

The [`examples/`](/examples/) directory contains more example clients and
servers.

## Design

The design doc for this SDK is at [design.md](./design/design.md), which was
initially reviewed at
[modelcontextprotocol/discussions/364](https://github.com/orgs/modelcontextprotocol/discussions/364).

Further design discussion should occur in
[issues](https://github.com/modelcontextprotocol/go-sdk/issues) (for concrete
proposals) or
[discussions](https://github.com/modelcontextprotocol/go-sdk/discussions) for
open-ended discussion. See [CONTRIBUTING.md](/CONTRIBUTING.md) for details.

## Acknowledgements

Several existing Go MCP SDKs inspired the development and design of this
official SDK, notably [mcp-go](https://github.com/mark3labs/mcp-go), authored
by Ed Zynda. We are grateful to Ed as well as the other contributors to mcp-go,
and to authors and contributors of other SDKs such as
[mcp-golang](https://github.com/metoro-io/mcp-golang) and
[go-mcp](https://github.com/ThinkInAIXYZ/go-mcp). Thanks to their work, there
is a thriving ecosystem of Go MCP clients and servers.

## License

This project is licensed under the MIT License - see the [LICENSE](./LICENSE)
file for details.
