# Makefile for MCP Go SDK
# Simplified version to run CI checks locally

.DEFAULT_GOAL := help

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "%-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

fmt: ## Check code formatting
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "The following files are not properly formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi; \
	echo "All Go files are properly formatted"

test: ## Run tests
	go test -v ./...

race: ## Run tests with race detector
	go test -v -race ./...

lint: ## Run golangci-lint
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found. Install with:"; \
		echo "go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

ci: fmt test race ## Run all CI checks locally
	@echo "All CI checks passed!"

install-lint: ## Install golangci-lint
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

.PHONY: help fmt test race lint ci install-lint