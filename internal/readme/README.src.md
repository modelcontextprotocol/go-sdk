# MCP Go SDK v0.8.0

[![Open in GitHub Codespaces](https://github.com/codespaces/badge.svg)](https://codespaces.new/modelcontextprotocol/go-sdk)

***BREAKING CHANGES***

This version contains minor breaking changes.
See the [release notes](
https://github.com/modelcontextprotocol/go-sdk/releases/tag/v0.8.0) for details.

[![PkgGoDev](https://pkg.go.dev/badge/github.com/modelcontextprotocol/go-sdk)](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk)

This repository contains an implementation of the official Go software
development kit (SDK) for the Model Context Protocol (MCP).

> [!IMPORTANT]
> The SDK is in release-candidate state, and is going to be tagged v1.0.0
> soon (see https://github.com/modelcontextprotocol/go-sdk/issues/328).
> We do not anticipate significant API changes or instability. Please use it
> and [file issues](https://github.com/modelcontextprotocol/go-sdk/issues/new/choose).

## Package / Feature documentation

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
  [`github.com/modelcontextprotocol/go-sdk/oauthex`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/oauthex)
  package provides extensions to the OAuth protocol, such as ProtectedResourceMetadata.

The SDK endeavors to implement the full MCP spec. The [`docs/`](/docs/) directory
contains feature documentation, mapping the MCP spec to the packages above.

## Getting started

To get started creating an MCP server, create an `mcp.Server` instance, add
features to it, and then run it over an `mcp.Transport`. For example, this
server adds a single simple tool, and then connects it to clients over
stdin/stdout:

%include server/server.go -

To communicate with that server, create an `mcp.Client` and connect it to the
corresponding server, by running the server command and communicating over its
stdin/stdout:

%include client/client.go -

The [`examples/`](/examples/) directory contains more example clients and
servers.

## Contributing

We welcome contributions to the SDK! Please see See
[CONTRIBUTING.md](/CONTRIBUTING.md) for details of how to contribute.

## Acknowledgements / Alternatives

Several third party Go MCP SDKs inspired the development and design of this
official SDK, and continue to be viable alternatives, notably
[mcp-go](https://github.com/mark3labs/mcp-go), originally authored by Ed Zynda.
We are grateful to Ed as well as the other contributors to mcp-go, and to
authors and contributors of other SDKs such as
[mcp-golang](https://github.com/metoro-io/mcp-golang) and
[go-mcp](https://github.com/ThinkInAIXYZ/go-mcp). Thanks to their work, there
is a thriving ecosystem of Go MCP clients and servers.

## License

This project is licensed under the MIT License - see the [LICENSE](./LICENSE)
file for details.
