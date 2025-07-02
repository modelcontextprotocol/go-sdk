# MCP Go SDK

***BREAKING CHANGES***

The latest version contains breaking changes:

- Server.AddTools is replaced by Server.AddTool.

- NewServerTool is replaced by AddTool. AddTool takes a Tool rather than a name and description, so you can 
  set any field on the Tool that you want before associating it with a handler.

- Tool options have been removed. If you don't want AddTool to infer a JSON Schema for you, you can construct one
  as a struct literal, or using any other code that suits you.

- AddPrompts, AddResources and AddResourceTemplates are similarly replaced by singular methods which pair the
  feature with a handler. The ServerXXX types have been removed.

[![PkgGoDev](https://pkg.go.dev/badge/github.com/modelcontextprotocol/go-sdk)](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk)

This repository contains an unreleased implementation of the official Go
software development kit (SDK) for the Model Context Protocol (MCP).

**WARNING**: The SDK should be considered unreleased, and is currently unstable
and subject to breaking changes. Please test it out and file bug reports or API
proposals, but don't use it in real projects. See the issue tracker for known
issues and missing features. We aim to release a stable version of the SDK in
August, 2025.

## Design

The design doc for this SDK is at [design.md](./design/design.md), which was
initially reviewed at
[modelcontextprotocol/discussions/364](https://github.com/orgs/modelcontextprotocol/discussions/364).

Further design discussion should occur in
[issues](https://github.com/modelcontextprotocol/go-sdk/issues) (for concrete
proposals) or
[discussions](https://github.com/modelcontextprotocol/go-sdk/discussions) for
open-ended discussion. See CONTRIBUTING.md for details.

## Package documentation

The SDK consists of two importable packages:

- The
  [`github.com/modelcontextprotocol/go-sdk/mcp`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp)
  package defines the primary APIs for constructing and using MCP clients and
  servers.
- The
  [`github.com/modelcontextprotocol/go-sdk/jsonschema`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/jsonschema)
  package provides an implementation of [JSON
  Schema](https://json-schema.org/), used for MCP tool input and output schema.

## Example

In this example, an MCP client communicates with an MCP server running in a
sidecar process:

%include client/client.go -

Here's an example of the corresponding server component, which communicates
with its client over stdin/stdout:

%include server/server.go -

The `examples/` directory contains more example clients and servers.

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
