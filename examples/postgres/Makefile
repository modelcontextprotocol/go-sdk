# PostgreSQL MCP Server Makefile

.PHONY: help build test clean docker docker-compose run run-http dev

# Default target
help:
	@echo "Available targets:"
	@echo "  build         - Build the PostgreSQL MCP server binary"
	@echo "  test          - Run tests with coverage"
	@echo "  clean         - Clean build artifacts"
	@echo "  docker        - Build Docker image"
	@echo "  docker-compose - Start development environment with Docker Compose"
	@echo "  run           - Run server in stdio mode (requires DATABASE_URL)"
	@echo "  run-http      - Run server in HTTP mode on :8080"
	@echo "  dev           - Start local development with sample database"

# Build the binary
build:
	go build -o postgres-mcp-server main.go

# Run tests with coverage
test:
	go test -v -cover

# Clean build artifacts
clean:
	rm -f postgres-mcp-server

# Build Docker image
docker:
	docker build -t postgres-mcp-server .

# Start development environment
docker-compose:
	docker-compose up -d

# Stop development environment
docker-compose-down:
	docker-compose down -v

# Run server in stdio mode
run:
	@if [ -z "$(DATABASE_URL)" ]; then \
		echo "DATABASE_URL environment variable is required"; \
		echo "Example: make run DATABASE_URL='postgres://user:pass@localhost:5432/db'"; \
		exit 1; \
	fi
	go run main.go

# Run server in HTTP mode for debugging
run-http:
	@if [ -z "$(DATABASE_URL)" ]; then \
		echo "DATABASE_URL environment variable is required"; \
		echo "Example: make run-http DATABASE_URL='postgres://user:pass@localhost:5432/db'"; \
		exit 1; \
	fi
	go run main.go -http=:8080

# Start local development with sample database
dev: docker-compose
	@echo "Waiting for PostgreSQL to start..."
	@sleep 5
	@echo "Development environment ready!"
	@echo "Database: postgres://testuser:testpass@localhost:5432/testdb"
	@echo "To run the MCP server:"
	@echo "  export DATABASE_URL='postgres://testuser:testpass@localhost:5432/testdb?sslmode=disable'"
	@echo "  make run"

# Install dependencies
deps:
	go mod tidy

# Run linting (if available)
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed, skipping..."; \
		go vet ./...; \
	fi

# Run all checks
check: test lint

# Package for distribution
package: clean build
	tar -czf postgres-mcp-server.tar.gz postgres-mcp-server README.md init.sql docker-compose.yml Dockerfile
