# MCP HTTP Example

This example demonstrates how to use the Model Context Protocol (MCP) over HTTP using the streamable transport. It includes both a server and client implementation.

## Overview

The example implements:
- A server that provides a `get_time` tool
- A client that connects to the server, lists available tools, and calls the `get_time` tool

## Running the Example

### Start the Server

```bash
go run main.go -server
```

This starts an MCP server on `localhost:8080` (default) that provides a `get_time` tool.

### Run the Client

In another terminal:

```bash
go run main.go -client
```

The client will:
1. Connect to the server
2. List available tools
3. Call the `get_time` tool for NYC, San Francisco, and Boston
4. Display the results

### Custom Host and Port

You can specify custom host and port:

```bash
# Server on all interfaces, port 9090
go run main.go -server -host 0.0.0.0 -port 9090

# Client connecting to custom address
go run main.go -client -host 192.168.1.100 -port 9090
```

## Testing with Real-World MCP Clients

You can test this server with Claude Code or other MCP clients:

```bash
# Start the server
go run main.go -server -host localhost -port 8080

# In another terminal, add the server to Claude Code
claude mcp add http://localhost:8080
```

Once added, Claude Code will be able to discover and use the `get_time` tool provided by this server.

