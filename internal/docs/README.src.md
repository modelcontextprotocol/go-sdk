# Features

These docs mirror the [official MCP spec](https://modelcontextprotocol.io/specification/2025-06-18).
Use the index below to learn how the SDK implements a particular aspect of the
protocol.

## Base Protocol

1. [Lifecycle (Clients, Servers, and Sessions)](protocol.md#lifecycle).
1. [Transports](protocol.md#transports)
    1. [Stdio transport](protocol.md#stdio-transport)
    1. [Streamable transport](protocol.md#streamable-transport)
    1. [Custom transports](protocol.md#stateless-mode)
1. [Authorization](protocol.md#authorization)
1. [Security](protocol.md#security)
1. [Utilities](protocol.md#utilities)
    1. [Cancellation](utilities.md#cancellation)
    1. [Ping](utilities.md#ping)
    1. [Progress](utilities.md#progress)

## Client Features

1. [Roots](client.md#roots)
1. [Sampling](client.md#sampling)
1. [Elicitation](clients.md#elicitation)

## Server Features

1. [Prompts](server.md#prompts)
1. [Resources](server.md#resources)
1. [Tools](tools.md)
1. [Utilities](server.md#utilities)
    1. [Completion](server.md#completion)
    1. [Logging](server.md#logging)
    1. [Pagination](server.md#pagination)

# TroubleShooting

See [troubleshooting.md](troubleshooting.md) for a troubleshooting guide.
