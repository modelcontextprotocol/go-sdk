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
CONFORMANCE_REF=""
CHECKOUT_DIR=""
SERVER_PACKAGE="./conformance/everything-server"
STATELESS=false
CONFORMANCE_ARGS=(--spec-version 2025-11-25)
FINAL_EXIT_CODE=0

usage() {
    echo "Usage: $0 [options] [-- <conformance arguments>]"
    echo ""
    echo "Run MCP conformance tests against the Go SDK conformance server."
    echo ""
    echo "Options:"
    echo "  --result_dir <dir>       Save results to the specified directory"
    echo "  --conformance_repo <path|url> Use a local checkout or clone a Git repository"
    echo "                               instead of using the latest npm release"
    echo "  --conformance_ref <ref>  Check out a branch, commit, or tag in a temporary clone"
    echo "                           (requires --conformance_repo)"
    echo "  --server <go-package>   Server to build (default: ./conformance/everything-server)"
    echo "  --stateless             Run the server in stateless mode"
    echo "  -- <args...>            Replace the default conformance arguments:"
    echo "                           --spec-version 2025-11-25"
    echo "  --help                   Show this help message"
}

# require_value exits unless $1 was given a usable value in $2.
require_value() {
    if [[ $# -lt 2 || -z "$2" || "$2" == --* ]]; then
        echo "Missing value for $1" >&2
        exit 1
    fi
}

# Parse arguments.
while [[ $# -gt 0 ]]; do
    case $1 in
        --result_dir)
            require_value "$@"
            RESULT_DIR="$2"
            shift 2
            ;;
        --conformance_repo)
            require_value "$@"
            CONFORMANCE_REPO="$2"
            shift 2
            ;;
        --conformance_ref)
            require_value "$@"
            CONFORMANCE_REF="$2"
            shift 2
            ;;
        --server)
            require_value "$@"
            SERVER_PACKAGE="$2"
            shift 2
            ;;
        --stateless)
            STATELESS=true
            shift
            ;;
        --)
            shift
            CONFORMANCE_ARGS=("$@")
            break
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

if [[ -n "$CONFORMANCE_REF" && -z "$CONFORMANCE_REPO" ]]; then
    echo "--conformance_ref requires --conformance_repo" >&2
    exit 1
fi

cleanup() {
    if [ -n "$SERVER_PID" ]; then
        echo "Stopping server..."
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    if [[ -n "$CHECKOUT_DIR" ]]; then
        rm -rf -- "$CHECKOUT_DIR"
    fi
}
trap cleanup EXIT

# Set up the work directory. Results are written to an absolute path, so that
# the conformance runner can be started from the work directory.
OUTPUT_ARGS=()
if [[ -n "$RESULT_DIR" ]]; then
    mkdir -p "$RESULT_DIR"
    RESULT_DIR=$(cd "$RESULT_DIR" && pwd)
    WORKDIR="$RESULT_DIR"
    OUTPUT_ARGS=(--output-dir "$RESULT_DIR")
else
    WORKDIR=$(mktemp -d)
fi

if [[ -n "$CONFORMANCE_REPO" ]]; then
    if [[ -d "$CONFORMANCE_REPO" ]]; then
        CONFORMANCE_REPO=$(cd "$CONFORMANCE_REPO" && pwd)
    fi
    if [[ -n "$CONFORMANCE_REF" || ! -d "$CONFORMANCE_REPO" ]]; then
        CHECKOUT_DIR=$(mktemp -d)
        git clone --quiet --no-checkout -- "$CONFORMANCE_REPO" "$CHECKOUT_DIR"
        git -C "$CHECKOUT_DIR" fetch --quiet origin "${CONFORMANCE_REF:-HEAD}"
        git -C "$CHECKOUT_DIR" checkout --quiet --detach FETCH_HEAD
        CONFORMANCE_REPO="$CHECKOUT_DIR"
        npm --prefix "$CONFORMANCE_REPO" ci --ignore-scripts
    fi
    npm --prefix "$CONFORMANCE_REPO" run build
    RUNNER=(node "$CONFORMANCE_REPO/dist/index.js")
else
    RUNNER=(npx @modelcontextprotocol/conformance@latest)
fi

# Build the conformance server.
REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
go -C "$REPO_ROOT" build -o "$WORKDIR/conformance-server" "$SERVER_PACKAGE"

# Start the server in the background.
# Stateful transport is the default so that
# server-initiated sampling/elicitation scenarios (which are not supported on
# stateless streamable HTTP) work against the current @latest conformance
# suite. Change the default once the 0.2.x line is promoted to the @latest
# dist-tag on npm and the stateless leg becomes viable.
echo "Starting conformance server on localhost:$PORT..."
"$WORKDIR/conformance-server" -http="localhost:$PORT" -stateless="$STATELESS" &
SERVER_PID=$!

echo "Server pid is $SERVER_PID"

# Wait for server to be ready
echo "Waiting for server to be ready..."
READY=false
for ((attempt = 0; attempt < 30; attempt++)); do
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        break
    fi
    if curl --silent --max-time 1 --output /dev/null "http://localhost:$PORT"; then
        READY=true
        break
    fi
    sleep 0.5
done
if [[ "$READY" != true ]]; then
    echo "Server failed to become ready."
    exit 1
fi

# Run conformance tests from the work directory to avoid writing results to the repo.
echo "Running conformance tests..."
(cd "$WORKDIR" && \
    "${RUNNER[@]}" server --url "http://localhost:$PORT" \
        "${CONFORMANCE_ARGS[@]}" "${OUTPUT_ARGS[@]}") || FINAL_EXIT_CODE=$?

echo ""
if [ -n "$RESULT_DIR" ]; then
    echo "See $RESULT_DIR for details."
else
    echo "Run with --result_dir to save results."
fi

exit $FINAL_EXIT_CODE
