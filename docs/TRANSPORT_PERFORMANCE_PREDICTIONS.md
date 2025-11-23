# MCP Transport Layer Performance Predictions

**Date:** January 13, 2025  
**Author:** AI Analysis  
**Purpose:** Document performance predictions for transport layers to validate against empirical measurements

## Methodology

Benchmarks measure three key dimensions:
1. **Latency** - Round-trip time for single request-response
2. **Throughput** - Sustained message rate (one-way)
3. **Payload Scaling** - Performance with varying message sizes
4. **Concurrency** - Performance under parallel load

## Transport Layer Overview

### Currently Implemented
1. **InMemory** - Go channels, no serialization at transport level
2. **Stdio** - OS pipes (stdin/stdout)
3. **IOTransport** - Generic io.Reader/Writer
4. **WebSocket** - TCP with WebSocket framing
5. **Streamable HTTP** - HTTP with optional SSE streaming

### Future Implementation
6. **gRPC** - HTTP/2 with Protocol Buffers (planned)

## Performance Predictions

### 1. Latency (Round-Trip Time per Request)

**Predicted Rankings (fastest → slowest):**

| Rank | Transport | Predicted Latency | Reasoning |
|------|-----------|------------------|-----------|
| 1 | InMemory | **100-500 ns** | Pure Go channels, no network/serialization overhead |
| 2 | Stdio | **1-5 µs** | Local kernel pipes, minimal overhead |
| 3 | WebSocket | **10-50 µs** | Persistent TCP connection, WebSocket framing |
| 4 | gRPC (future) | **15-80 µs** | HTTP/2 overhead, but efficient binary encoding |
| 5 | Streamable HTTP | **20-100 µs** | Full HTTP request/response cycle |

**Reasoning:**

- **InMemory**: Theoretical minimum - just Go scheduler + channel operations
- **Stdio**: Kernel pipe syscalls (`write`/`read`), but local - no TCP stack
- **WebSocket**: TCP handshake amortized, but frame parsing + TCP overhead per message
- **gRPC**: HTTP/2 adds complexity, but binary protobuf may offset JSON parsing cost
- **HTTP**: Each message requires full HTTP headers, connection management

**Key Variables:**
- CPU: Modern multicore (2-8 cores) will favor concurrent designs
- Network: Localhost testing removes network latency but includes TCP stack
- JSON: Parsing overhead ~1-10µs depending on message complexity
- Context switching: Goroutine switching ~100-200ns

### 2. Throughput (Messages per Second, One-Way)

**Predicted Rankings (highest → lowest):**

| Rank | Transport | Predicted Throughput | Reasoning |
|------|-----------|---------------------|-----------|
| 1 | InMemory | **1M - 10M msg/sec** | Limited only by Go scheduler |
| 2 | Stdio | **100K - 500K msg/sec** | Kernel pipe bandwidth |
| 3 | gRPC (future) | **100K - 300K msg/sec** | HTTP/2 multiplexing advantage |
| 4 | WebSocket | **50K - 200K msg/sec** | TCP + WebSocket framing |
| 5 | Streamable HTTP | **10K - 50K msg/sec** | Connection overhead, no pipelining |

**Reasoning:**

- **InMemory**: Benchmarks show Go channels can handle 10M+ ops/sec with low contention
- **Stdio**: Kernel pipe buffers (64KB default), but single-threaded serialization
- **gRPC**: HTTP/2 allows multiple streams over one connection (multiplexing benefit)
- **WebSocket**: Single TCP connection, but framing overhead per message
- **HTTP**: Each request requires connection setup or pool management

**Bottlenecks:**
- CPU: JSON encoding/decoding likely bottleneck for all except InMemory
- Network: TCP window size, congestion control (even on localhost)
- Buffering: Larger buffers help throughput but hurt latency

### 3. Payload Size Scaling

**Predictions by Transport:**

#### Small Messages (< 1KB)
```
Best:    InMemory (no serialization overhead)
Good:    Stdio, WebSocket (efficient for small payloads)
Poor:    HTTP (header overhead dominates)
Best²:   gRPC (binary encoding efficiency)
```

#### Medium Messages (1KB - 100KB)
```
Best:    InMemory (linear scaling)
Good:    Stdio, WebSocket, gRPC (all handle well)
Fair:    HTTP (chunking helps)
```

#### Large Messages (> 100KB)
```
Best:    gRPC (streaming + binary)
Good:    WebSocket (streaming frames)
Fair:    Stdio (pipe buffer limitations)
Poor:    HTTP (memory copies, base64 for binary)
Worst:   InMemory (all in memory at once)
```

**Predicted Crossover Points:**

| Transport | Sweet Spot | Degrades At | Reason |
|-----------|-----------|------------|---------|
| InMemory | < 10KB | > 1MB | Memory pressure, GC impact |
| Stdio | 1KB - 100KB | > 500KB | Pipe buffer fills, blocking writes |
| WebSocket | 1KB - 1MB | > 5MB | TCP segmentation, buffering |
| HTTP | 10KB - 10MB | > 50MB | Chunking works, but copies expensive |
| gRPC | 1KB - 100MB | > 1GB | Streaming + efficient encoding |

### 4. Concurrency Performance

**Predicted Behavior Under Parallel Load:**

| Transport | Scaling | Max Efficiency | Reasoning |
|-----------|---------|----------------|-----------|
| InMemory | **Excellent** | Near-linear to GOMAXPROCS | Go channels are lock-free in hot path |
| Stdio | **Poor** | ~1.2x speedup | Single pipe, mutex serialization |
| WebSocket | **Good** | ~3-4x speedup | Mutex on writes, but reads parallel |
| HTTP | **Excellent** | Near-linear | Connection pool, independent requests |
| gRPC | **Excellent** | Near-linear | HTTP/2 multiplexing, stream-per-request |

**Concurrency Bottlenecks:**

1. **InMemory**: 
   - Channel contention at high goroutine count (>100)
   - GC pressure from rapid allocation

2. **Stdio**:
   - Single write pipe = global mutex
   - CPU serialization bottleneck

3. **WebSocket**:
   - Write mutex required (gorilla/websocket not thread-safe)
   - Single TCP connection = head-of-line blocking

4. **HTTP**:
   - Connection pool management overhead
   - Too many connections = memory/fd exhaustion

5. **gRPC**:
   - Stream management overhead
   - Protobuf encoder allocation

### 5. Memory Efficiency

**Predicted Memory Usage (per connection):**

| Transport | Fixed Overhead | Per-Message Overhead | Notes |
|-----------|---------------|---------------------|-------|
| InMemory | ~500 bytes | ~200 bytes | Channel + buffering |
| Stdio | ~1 KB | ~100 bytes | Pipe buffers, minimal state |
| WebSocket | ~10-20 KB | ~300 bytes | TCP buffers + WS state |
| HTTP | ~5-15 KB | ~500 bytes | HTTP buffers, connection pool |
| gRPC | ~20-50 KB | ~250 bytes | HTTP/2 state + stream metadata |

**Memory Allocation Patterns:**

- **InMemory**: Most allocations are for message copies
- **Stdio**: Allocations mostly in JSON encode/decode
- **WebSocket**: Frame allocation, TCP buffers
- **HTTP**: Header parsing, body buffers
- **gRPC**: Protobuf encoding (fewer allocations than JSON)

## Special Case: gRPC Predictions

### Why gRPC May Surprise

**Potential Advantages:**
1. Binary encoding (protobuf) is 3-10x faster to encode/decode than JSON
2. HTTP/2 multiplexing eliminates head-of-line blocking
3. Streaming RPCs are first-class (vs WebSocket which needs framing)
4. Built-in flow control and backpressure

**Potential Disadvantages:**
1. HTTP/2 complexity adds latency (HPACK compression, flow control)
2. Protobuf schema requirement (vs schema-less JSON)
3. Less browser-friendly (requires grpc-web proxy)
4. Larger binary size due to protobuf runtime

**Wild Predictions:**

I predict gRPC will:
- ✅ Beat HTTP for latency (50% faster) due to connection reuse
- ✅ Match or beat WebSocket for throughput due to multiplexing
- ❌ NOT beat InMemory or Stdio (too much protocol overhead)
- ✅ Excel at large payloads (binary encoding + streaming)
- ✅ Be 2-3x more memory efficient than JSON-based transports

**The Surprise Factor:**

Most developers expect gRPC to be "enterprise heavy" and slow. I predict it will actually be competitive with WebSocket for most workloads due to:
1. HTTP/2 is now mature and highly optimized
2. Protobuf encoding is genuinely faster than JSON
3. Multiplexing eliminates connection overhead

**Where gRPC May Disappoint:**

- Very small messages (< 100 bytes): HTTP/2 frame overhead may exceed JSON overhead
- Ultra-low latency: InMemory and Stdio will always win for localhost
- Simplicity: More moving parts = more that can go wrong

## Testing Methodology

To validate these predictions:

```bash
# Run all benchmarks
go test -bench=. -benchmem -benchtime=3s ./mcp

# Compare specific transports
go test -bench="BenchmarkInMemoryTransport|BenchmarkWebSocketTransport" -benchmem ./mcp

# Test with different payload sizes
go test -bench="Payload" -benchmem ./mcp

# Stress test concurrency
go test -bench="Concurrency" -benchmem -cpu=1,2,4,8 ./mcp
```

## Success Criteria for Predictions

I'll consider my predictions **accurate** if:

1. **Latency**: Within 2x of predicted values
2. **Throughput**: Within same order of magnitude (10x range)
3. **Rankings**: Relative order correct for 80% of scenarios
4. **Scaling**: Trends match predictions (linear, sublinear, etc.)

I'll be **surprised** if:

1. WebSocket beats Stdio for latency (TCP overhead should be > pipe syscall)
2. HTTP beats WebSocket for throughput (persistent connection should win)
3. gRPC has worse latency than HTTP (HTTP/2 should amortize handshake)
4. InMemory doesn't dominate all categories (it's literally just memory)

## Real-World Recommendations (Pre-Testing)

Based on predictions, here's what I'd recommend TODAY (before seeing real numbers):

### Use Case → Transport Mapping

| Use Case | Recommended | Reasoning |
|----------|-------------|-----------|
| **Testing** | InMemory | Fastest, deterministic, no flakiness |
| **CLI tools** | Stdio | Universal, simple, works everywhere |
| **Browser clients** | WebSocket | Native browser support, bidirectional |
| **Server-to-server** | gRPC | Best performance, type safety, streaming |
| **Public APIs** | HTTP | Universal, cacheable, REST-ful |
| **Real-time updates** | WebSocket | Push notifications, low latency |
| **File transfers** | gRPC or HTTP | Streaming support, chunking |
| **Mobile apps** | gRPC | Efficient binary, connection pooling |

## Questions to Answer with Benchmarks

1. **Is the JSON parsing bottleneck bigger than transport overhead?**
   - Prediction: Yes, for messages > 1KB

2. **Does WebSocket persistent connection really help?**
   - Prediction: Yes, 2-5x better throughput than HTTP

3. **Is gRPC worth the complexity?**
   - Prediction: Yes for high-throughput services, no for simple tools

4. **When should you use InMemory vs Stdio?**
   - Prediction: InMemory for tests, Stdio for everything else

5. **Does HTTP/2 in gRPC actually help?**
   - Prediction: Yes, 30-50% better than HTTP/1.1

## Conclusion

These predictions are based on:
- 20+ years of networking performance research
- Go runtime characteristics (goroutines, channels, GC)
- Protocol specifications (WebSocket, HTTP/2, gRPC)
- General systems knowledge (TCP, pipes, syscalls)

The actual numbers will depend on:
- Hardware (CPU, RAM, network interface)
- Go version (runtime optimizations)
- Message characteristics (size, complexity)
- Concurrency patterns (goroutine count, contention)

**Most Confident Predictions:**
1. InMemory will crush everything (it's just memory)
2. HTTP will be slowest for small messages (header overhead)
3. Throughput order: InMemory >> Stdio > gRPC ≈ WebSocket > HTTP
4. gRPC will surprise people with how competitive it is

**Least Confident Predictions:**
1. Exact latency numbers (hardware dependent)
2. gRPC vs WebSocket ranking (too many variables)
3. Concurrency scaling factors (depends on contention)

Let's run the benchmarks and see how wrong (or right) I am! 🎯
