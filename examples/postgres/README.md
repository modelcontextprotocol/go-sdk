# PostgreSQL

A Model Context Protocol server that provides read-only access to PostgreSQL databases. This server enables LLMs to inspect database schemas and execute read-only queries.

## Components

### Tools

- **query**
  - Execute read-only SQL queries against the connected database
  - Input: `sql` (string): The SQL query to execute
  - All queries are executed within a READ ONLY transaction

### Resources

The server provides schema information for each table in the database:

- **Table Schemas** (`postgres://<host>/<table>/schema`)
  - JSON schema information for each table
  - Includes column names and data types
  - Automatically discovered from database metadata

## Configuration

### Usage with Claude Desktop

To use this server with the Claude Desktop app, add the following configuration to the "mcpServers" section of your `claude_desktop_config.json`:

### Docker

- When running Docker on macOS, use `host.docker.internal` if the PostgreSQL server is running on the host network (e.g. localhost)
- Username/password can be added to the PostgreSQL URL with `postgresql://user:password@host:port/db-name`

```json
{
  "mcpServers": {
    "postgres": {
      "command": "docker",
      "args": [
        "run",
        "-i",
        "--rm",
        "mcp/postgres-go",
        "postgresql://host.docker.internal:5432/mydb"
      ]
    }
  }
}
```

### Go Binary

```json
{
  "mcpServers": {
    "postgres": {
      "command": "/path/to/postgres-mcp-server",
      "args": ["postgresql://localhost/mydb"]
    }
  }
}
```

Replace `/mydb` with your database name and `/path/to/postgres-mcp-server` with the actual path to your built binary.

### Usage with VS Code

For manual installation, add the following JSON block to your User Settings (JSON) file in VS Code. You can do this by pressing `Ctrl + Shift + P` and typing `Preferences: Open User Settings (JSON)`.

Optionally, you can add it to a file called `.vscode/mcp.json` in your workspace. This will allow you to share the configuration with others.

> Note that the `mcp` key is not needed in the `.vscode/mcp.json` file.

### Docker

**Note**: When using Docker and connecting to a PostgreSQL server on your host machine, use `host.docker.internal` instead of `localhost` in the connection URL.

```json
{
  "mcp": {
    "inputs": [
      {
        "type": "promptString",
        "id": "pg_url",
        "description": "PostgreSQL URL (e.g. postgresql://user:pass@host.docker.internal:5432/mydb)"
      }
    ],
    "servers": {
      "postgres": {
        "command": "docker",
        "args": ["run", "-i", "--rm", "mcp/postgres-go", "${input:pg_url}"]
      }
    }
  }
}
```

### Go Binary

```json
{
  "mcp": {
    "inputs": [
      {
        "type": "promptString",
        "id": "pg_url",
        "description": "PostgreSQL URL (e.g. postgresql://user:pass@localhost:5432/mydb)"
      }
    ],
    "servers": {
      "postgres": {
        "command": "/path/to/postgres-mcp-server",
        "args": ["${input:pg_url}"]
      }
    }
  }
}
```

## Development

### Quick Start

1. Start the development environment:

```bash
make dev
```

2. Run the server:

```bash
export DATABASE_URL="postgres://testuser:testpass@localhost:5432/testdb?sslmode=disable"
go run main.go
```

### Building

Go binary:

```bash
go build -o postgres-mcp-server main.go
```

Docker:

```bash
docker build -t mcp/postgres-go .
```

### Testing

Run the test suite:

```bash
go test -v -cover
```

Or use the Makefile:

```bash
make test
```

### Environment Variables

The server can be configured using environment variables:

- `DATABASE_URL`: PostgreSQL connection string (required)

Example:

```bash
export DATABASE_URL="postgres://user:password@localhost:5432/database?sslmode=disable"
```

## License

This MCP server is licensed under the MIT License. This means you are free to use, modify, and distribute the software, subject to the terms and conditions of the MIT License. For more details, please see the LICENSE file in the project repository.
