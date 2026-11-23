#!/bin/bash
# Copyright 2025 The Go MCP SDK Authors. All rights reserved.
# Use of this source code is governed by the license
# that can be found in the LICENSE file.

set -euo pipefail

usage() {
    echo "Usage: $0 --conformance_repo <built-checkout> [--result_dir <dir>]"
    echo "Run the Skills server scenarios on both supported protocol transports."
    echo "The checkout must contain the scenarios from conformance PR #330."
}

CONFORMANCE_REPO=""
RESULT_DIR=""
SERVER_PID=""
PORT="${PORT:-18301}"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --conformance_repo) CONFORMANCE_REPO="$2"; shift 2 ;;
        --result_dir) RESULT_DIR="$2"; shift 2 ;;
        --help) usage; exit 0 ;;
        *) usage >&2; exit 1 ;;
    esac
done
if [[ -z "$CONFORMANCE_REPO" || ! -f "$CONFORMANCE_REPO/dist/index.js" ]]; then
    usage >&2
    exit 1
fi
CONFORMANCE_REPO=$(cd "$CONFORMANCE_REPO" && pwd)
if [[ -z "$RESULT_DIR" ]]; then
    RESULT_DIR=$(mktemp -d)
fi
mkdir -p "$RESULT_DIR"
RESULT_DIR=$(cd "$RESULT_DIR" && pwd)
REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)

stop_server() {
    if [[ -n "$SERVER_PID" ]]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
        SERVER_PID=""
    fi
}
trap stop_server EXIT

go -C "$REPO_ROOT" build -o "$RESULT_DIR/skills-server" ./conformance/skills-server
STATUS=0
for version in 2025-11-25 2026-07-28; do
    stateless=false
    if [[ "$version" == 2026-07-28 ]]; then
        stateless=true
    fi
    "$RESULT_DIR/skills-server" -http="localhost:$PORT" -stateless="$stateless" > "$RESULT_DIR/$version-server.log" 2>&1 &
    SERVER_PID=$!
    ready=false
    for ((attempt = 0; attempt < 30; attempt++)); do
        if ! kill -0 "$SERVER_PID" 2>/dev/null; then
            break
        fi
        if curl --silent --max-time 1 --output /dev/null "http://localhost:$PORT"; then
            ready=true
            break
        fi
        sleep 0.5
    done
    if [[ "$ready" != true ]]; then
        cat "$RESULT_DIR/$version-server.log" >&2
        exit 1
    fi
    for scenario in enumeration manifest directory; do
        node "$CONFORMANCE_REPO/dist/index.js" server \
            --url "http://localhost:$PORT" \
            --scenario "sep-2640-skills-$scenario" \
            --spec-version "$version" --force \
            --output-dir "$RESULT_DIR/$version/$scenario" || STATUS=1
    done
    stop_server
done
echo "Skills conformance results: $RESULT_DIR"
exit "$STATUS"
