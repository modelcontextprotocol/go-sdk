#!/bin/bash
# Copyright 2025 The Go MCP SDK Authors. All rights reserved.
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

# Run MCP conformance tests against the Go SDK conformance server.

set -e

PORT="${PORT:-3000}"
SERVER_PID=""
RESULT_DIR=""
WORKDIR=""
CONFORMANCE_REPO=""

usage() {
    echo "Usage: $0 [options]"
    echo ""
    echo "Run MCP conformance tests against the Go SDK conformance server."
    echo ""
    echo "Options:"
    echo "  --result_dir <dir>       Save results to the specified directory"
    echo "  --conformance_repo <dir> Run conformance tests from a local checkout"
    echo "                           instead of using the latest npm release"
    echo "  --help                   Show this help message"
}

# Parse arguments.
while [[ $# -gt 0 ]]; do
    case $1 in
        --result_dir)
            RESULT_DIR="$2"
            shift 2
            ;;
        --conformance_repo)
            CONFORMANCE_REPO="$2"
            shift 2
            ;;
        --help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

cleanup() {
    if [ -n "$SERVER_PID" ]; then
        kill "$SERVER_PID" 2>/dev/null || true
    fi
    # Clean up the work directory unless --result_dir was specified.
    if [ -z "$RESULT_DIR" ] && [ -n "$WORKDIR" ]; then
        rm -rf "$WORKDIR"
    fi
}
trap cleanup EXIT

# Set up the work directory.
if [ -n "$RESULT_DIR" ]; then
    mkdir -p "$RESULT_DIR"
    WORKDIR="$RESULT_DIR"
else
    WORKDIR=$(mktemp -d)
fi

# Build the conformance server.
go build -o "$WORKDIR/conformance-server" ./examples/server/conformance

# Start the server in the background
echo "Starting conformance server on port $PORT..."
"$WORKDIR/conformance-server" -http=":$PORT" &
SERVER_PID=$!

echo "Server pid is $SERVER_PID"

# Wait for server to be ready
echo "Waiting for server to be ready..."
for i in {1..30}; do
    if curl -s "http://localhost:$PORT" > /dev/null 2>&1; then
        echo "Server is ready."
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "Server failed to start within 15 seconds."
        exit 1
    fi
    sleep 0.5
done

# Run conformance tests from the work directory to avoid writing results to the repo.
echo "Running conformance tests..."
if [ -n "$CONFORMANCE_REPO" ]; then
    # Run from local checkout using npm run start.
    (cd "$WORKDIR" && \
        npm --prefix "$CONFORMANCE_REPO" run start -- \
            server --url "http://localhost:$PORT")
else
    (cd "$WORKDIR" && \
        npx @modelcontextprotocol/conformance@latest server --url "http://localhost:$PORT")
fi

echo ""
if [ -n "$RESULT_DIR" ]; then
    echo "See $RESULT_DIR for details."
else
    echo "Run with --result_dir to save results."
fi
