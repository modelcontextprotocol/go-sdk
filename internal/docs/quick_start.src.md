# Quick Start

%toc

## Installation

```
go get github.com/modelcontextprotocol/go-sdk/mcp
```

The module root holds no importable package, so `go get` on the module path
alone records the requirement without the `go.sum` entries the `mcp` package
needs, and the first `go build` fails. Ask for the package instead, as above
(or run `go mod tidy` after writing the code below).

## Getting started

To get started creating an MCP server, create an `mcp.Server` instance, add
features to it, and then run it over an `mcp.Transport`. For example, this
server adds a single simple tool, and then connects it to clients over
stdin/stdout:

%include ../readme/server/server.go -

To communicate with that server, create an `mcp.Client` and connect it to the
corresponding server, by running the server command and communicating over its
stdin/stdout:

%include ../readme/client/client.go -

The [`examples/`](https://github.com/modelcontextprotocol/go-sdk/tree/main/examples) directory contains more example clients and
servers.
