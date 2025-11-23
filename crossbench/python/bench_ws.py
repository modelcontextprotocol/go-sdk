#!/usr/bin/env python3
"""
Simple WebSocket micro-benchmark for Python.
Creates an in-process echo server and a client that performs N round-trips of a
JSON-RPC-like message with a configurable payload size.

Usage:
  python3 bench_ws.py --iters 10000 --payload 1024

Optional dependencies (recommended):
  pip install websockets orjson

If `orjson` is installed it will be used for (de)serialization; otherwise the
standard `json` module is used.
"""

import argparse
import asyncio
import json
import socket
import time
import sys
import subprocess
import shlex

try:
    import orjson as _orjson
    has_orjson = True
except Exception:
    _orjson = None
    has_orjson = False

try:
    import websockets
except Exception:
    print("Please install the websockets package: pip install websockets", file=sys.stderr)
    raise


def get_free_port() -> int:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(("127.0.0.1", 0))
    addr, port = s.getsockname()
    s.close()
    return port


async def echo_handler(ws, path=None):
    # Accept an optional path parameter for compatibility with multiple
    # `websockets` versions which pass either (ws, path) or (ws,) to the
    # handler. Echo back each received message.
    try:
        async for msg in ws:
            await ws.send(msg)
    except Exception:
        # If the connection fails, just return.
        return


async def run_benchmark(host: str, port: int, iters: int, payload: int):
    uri = f"ws://{host}:{port}"

    # Create message template
    payload_str = "x" * payload
    msg_obj = {"id": 1, "method": "test", "params": {"data": payload_str}}

    if has_orjson:
        encode = lambda o: _orjson.dumps(o)
        decode = lambda b: _orjson.loads(b)
    else:
        encode = lambda o: json.dumps(o).encode("utf-8")
        decode = lambda b: json.loads(b)

    # Connect client
    async with websockets.connect(uri) as ws:
        # warm-up
        await ws.send(encode(msg_obj))
        await ws.recv()

        t0 = time.perf_counter_ns()
        for i in range(iters):
            # reuse same id to focus on transport + encode cost
            data = encode(msg_obj)
            await ws.send(data)
            _ = await ws.recv()
        t1 = time.perf_counter_ns()

    total_ns = t1 - t0
    ns_per = total_ns / iters
    ops_per_sec = 1e9 * iters / total_ns
    bytes_per = len(encode(msg_obj))

    print(f"Python microbench: iters={iters} payload={payload} bytes/op={bytes_per}")
    print(f"  total time: {total_ns/1e9:.4f}s, ns/op: {ns_per:.0f}, ops/s: {ops_per_sec:.0f}")


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--iters", type=int, default=10000)
    p.add_argument("--payload", type=int, default=1024)
    p.add_argument("--host", default="127.0.0.1")
    p.add_argument("--port", type=int, default=0)
    p.add_argument("--use-sdk", action="store_true", help="install and use the official python-sdk from GitHub if available")
    args = p.parse_args()

    if args.use_sdk:
        print("Installing modelcontextprotocol/python-sdk from GitHub...")
        cmd = "pip install --upgrade git+https://github.com/modelcontextprotocol/python-sdk.git"
        try:
            subprocess.check_call(shlex.split(cmd))
            print("Installed python-sdk (attempting to import)")
        except Exception as e:
            print(f"Failed to install python-sdk: {e}", file=sys.stderr)
            print("Falling back to raw websockets/json path")

    port = args.port or get_free_port()

    async def _async_main():
        # Start server as an async context manager so the event loop is running
        async with websockets.serve(echo_handler, args.host, port):
            await run_benchmark(args.host, port, args.iters, args.payload)

    asyncio.run(_async_main())


if __name__ == "__main__":
    main()
