// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// BenchmarkTransportLatency measures the latency of a single request-response cycle
// for each transport type. This simulates real-world API call patterns.
//
// Expected Performance Ranking (fastest to slowest):
// 1. InMemory - ~100-500ns (pure Go channels, no serialization/network overhead)
// 2. Stdio/IO - ~1-5µs (local pipes, minimal serialization)
// 3. WebSocket - ~10-50µs (TCP overhead, WebSocket framing, HTTP upgrade)
// 4. Streamable HTTP - ~20-100µs (HTTP request/response cycle, more overhead)
// 5. gRPC (future) - ~15-80µs (HTTP/2, protobuf vs JSON)
//
// Reasoning:
// - InMemory: No network, no serialization of transport layer, just Go channels
// - Stdio: Local process pipes are very fast, minimal kernel overhead
// - WebSocket: Persistent connection amortizes handshake, but has framing overhead
// - HTTP: Each request has full HTTP cycle, headers, connection management
// - gRPC: HTTP/2 benefits (multiplexing, header compression) vs JSON parsing costs

func BenchmarkInMemoryTransportLatency(b *testing.B) {
	clientTransport, serverTransport := NewInMemoryTransports()

	clientConn, err := clientTransport.Connect(context.Background())
	if err != nil {
		b.Fatalf("Failed to create client connection: %v", err)
	}
	defer clientConn.Close()

	serverConn, err := serverTransport.Connect(context.Background())
	if err != nil {
		b.Fatalf("Failed to create server connection: %v", err)
	}
	defer serverConn.Close()

	// Simulate server echo
	go func() {
		for {
			msg, err := serverConn.Read(context.Background())
			if err != nil {
				return
			}
			serverConn.Write(context.Background(), msg)
		}
	}()

	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		msg, _ := jsonrpc2.NewCall(jsonrpc2.Int64ID(int64(i)), "test", nil)
		if err := clientConn.Write(ctx, msg); err != nil {
			b.Fatalf("Write failed: %v", err)
		}
		if _, err := clientConn.Read(ctx); err != nil {
			b.Fatalf("Read failed: %v", err)
		}
	}
}

func BenchmarkWebSocketTransportLatency(b *testing.B) {
	// Create echo server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			Subprotocols: []string{"mcp"},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Echo messages
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(messageType, data); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	transport := &WebSocketClientTransport{URL: wsURL}

	clientConn, err := transport.Connect(context.Background())
	if err != nil {
		b.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		msg, _ := jsonrpc2.NewCall(jsonrpc2.Int64ID(int64(i)), "test", nil)
		if err := clientConn.Write(ctx, msg); err != nil {
			b.Fatalf("Write failed: %v", err)
		}
		if _, err := clientConn.Read(ctx); err != nil {
			b.Fatalf("Read failed: %v", err)
		}
	}
}

// Benchmarks in this file have different measurement focuses:
// - Benchmarks named "WebSocketTransport_..." emulate MCP protocol usage and
//   therefore use the 'mcp' subprotocol on both client and server.
// - Framing/encode-only benchmarks (e.g., BenchmarkWebSocketFramingOverhead)
//   intentionally do not set the MCP subprotocol to isolate frame-level cost.

// BenchmarkTransportThroughput measures sustained message throughput
// Expected ranking (highest to lowest throughput):
// 1. InMemory - ~1M-10M msg/sec (limited only by Go scheduler)
// 2. Stdio - ~100K-500K msg/sec (kernel pipe bandwidth)
// 3. WebSocket - ~50K-200K msg/sec (TCP + framing overhead)
// 4. HTTP - ~10K-50K msg/sec (connection overhead, no pipelining)
// 5. gRPC (future) - ~100K-300K msg/sec (HTTP/2 multiplexing advantage)

func BenchmarkInMemoryTransportThroughput(b *testing.B) {
	clientTransport, serverTransport := NewInMemoryTransports()

	clientConn, err := clientTransport.Connect(context.Background())
	if err != nil {
		b.Fatalf("Failed to create client connection: %v", err)
	}
	defer clientConn.Close()

	serverConn, err := serverTransport.Connect(context.Background())
	if err != nil {
		b.Fatalf("Failed to create server connection: %v", err)
	}
	defer serverConn.Close()

	// Consume messages on server side
	go func() {
		for {
			if _, err := serverConn.Read(context.Background()); err != nil {
				return
			}
		}
	}()

	ctx := context.Background()
	msg, _ := jsonrpc2.NewCall(jsonrpc2.Int64ID(1), "test", nil)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := clientConn.Write(ctx, msg); err != nil {
			b.Fatalf("Write failed: %v", err)
		}
	}
}

func BenchmarkWebSocketTransportThroughput(b *testing.B) {
	// Create consuming server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			Subprotocols: []string{"mcp"},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Consume messages
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	transport := &WebSocketClientTransport{URL: wsURL}

	clientConn, err := transport.Connect(context.Background())
	if err != nil {
		b.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	ctx := context.Background()
	msg, _ := jsonrpc2.NewCall(jsonrpc2.Int64ID(1), "test", nil)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := clientConn.Write(ctx, msg); err != nil {
			b.Fatalf("Write failed: %v", err)
		}
	}
}

// BenchmarkTransportPayloadSize measures performance with different payload sizes
// This is critical for understanding transport efficiency with various data sizes.
//
// Expected behavior:
// - InMemory: Linear scaling (no serialization overhead at transport level)
// - Stdio: Near-linear (kernel pipe is efficient for medium payloads)
// - WebSocket: Good for small-medium, overhead for large (TCP buffering, framing)
// - HTTP: Poor for small (header overhead), better for large (chunked encoding)
// - gRPC: Best overall (efficient binary encoding, streaming)

func benchmarkTransportPayloadSize(b *testing.B, payloadSize int, setupTransport func() (Connection, Connection, func())) {
	clientConn, serverConn, cleanup := setupTransport()
	defer cleanup()

	// Server echo
	go func() {
		for {
			msg, err := serverConn.Read(context.Background())
			if err != nil {
				return
			}
			serverConn.Write(context.Background(), msg)
		}
	}()

	// Create message with specific payload size
	payload := make(map[string]interface{})
	payload["data"] = strings.Repeat("x", payloadSize)

	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(int64(payloadSize))

	for i := 0; i < b.N; i++ {
		msg, _ := jsonrpc2.NewCall(jsonrpc2.Int64ID(int64(i)), "test", payload)
		if err := clientConn.Write(ctx, msg); err != nil {
			b.Fatalf("Write failed: %v", err)
		}
		if _, err := clientConn.Read(ctx); err != nil {
			b.Fatalf("Read failed: %v", err)
		}
	}
}

func BenchmarkInMemoryTransport_Payload1KB(b *testing.B) {
	benchmarkTransportPayloadSize(b, 1024, func() (Connection, Connection, func()) {
		clientTransport, serverTransport := NewInMemoryTransports()
		clientConn, _ := clientTransport.Connect(context.Background())
		serverConn, _ := serverTransport.Connect(context.Background())
		return clientConn, serverConn, func() {
			clientConn.Close()
			serverConn.Close()
		}
	})
}

func BenchmarkInMemoryTransport_Payload10KB(b *testing.B) {
	benchmarkTransportPayloadSize(b, 10*1024, func() (Connection, Connection, func()) {
		clientTransport, serverTransport := NewInMemoryTransports()
		clientConn, _ := clientTransport.Connect(context.Background())
		serverConn, _ := serverTransport.Connect(context.Background())
		return clientConn, serverConn, func() {
			clientConn.Close()
			serverConn.Close()
		}
	})
}

func BenchmarkInMemoryTransport_Payload100KB(b *testing.B) {
	benchmarkTransportPayloadSize(b, 100*1024, func() (Connection, Connection, func()) {
		clientTransport, serverTransport := NewInMemoryTransports()
		clientConn, _ := clientTransport.Connect(context.Background())
		serverConn, _ := serverTransport.Connect(context.Background())
		return clientConn, serverConn, func() {
			clientConn.Close()
			serverConn.Close()
		}
	})
}

func BenchmarkWebSocketTransport_Payload1KB(b *testing.B) {
	benchmarkTransportPayloadSize(b, 1024, func() (Connection, Connection, func()) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{Subprotocols: []string{"mcp"}}
			conn, _ := upgrader.Upgrade(w, r, nil)
			go func() {
				defer conn.Close()
				for {
					messageType, data, err := conn.ReadMessage()
					if err != nil {
						return
					}
					conn.WriteMessage(messageType, data)
				}
			}()
		}))

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
		transport := &WebSocketClientTransport{URL: wsURL}
		clientConn, _ := transport.Connect(context.Background())

		// Dummy server connection (not actually used, just for interface compatibility)
		dummyTransport, _ := NewInMemoryTransports()
		serverConn, _ := dummyTransport.Connect(context.Background())

		return clientConn, serverConn, func() {
			clientConn.Close()
			server.Close()
		}
	})
}

func BenchmarkWebSocketTransport_Payload10KB(b *testing.B) {
	benchmarkTransportPayloadSize(b, 10*1024, func() (Connection, Connection, func()) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{Subprotocols: []string{"mcp"}}
			conn, _ := upgrader.Upgrade(w, r, nil)
			go func() {
				defer conn.Close()
				for {
					messageType, data, err := conn.ReadMessage()
					if err != nil {
						return
					}
					conn.WriteMessage(messageType, data)
				}
			}()
		}))

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
		transport := &WebSocketClientTransport{URL: wsURL}
		clientConn, _ := transport.Connect(context.Background())

		dummyTransport, _ := NewInMemoryTransports()
		serverConn, _ := dummyTransport.Connect(context.Background())

		return clientConn, serverConn, func() {
			clientConn.Close()
			server.Close()
		}
	})
}

func BenchmarkWebSocketTransport_Payload100KB(b *testing.B) {
	benchmarkTransportPayloadSize(b, 100*1024, func() (Connection, Connection, func()) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{Subprotocols: []string{"mcp"}}
			conn, _ := upgrader.Upgrade(w, r, nil)
			go func() {
				defer conn.Close()
				for {
					messageType, data, err := conn.ReadMessage()
					if err != nil {
						return
					}
					conn.WriteMessage(messageType, data)
				}
			}()
		}))

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
		transport := &WebSocketClientTransport{URL: wsURL}
		clientConn, _ := transport.Connect(context.Background())

		dummyTransport, _ := NewInMemoryTransports()
		serverConn, _ := dummyTransport.Connect(context.Background())

		return clientConn, serverConn, func() {
			clientConn.Close()
			server.Close()
		}
	})
}

// BenchmarkTransportConcurrency measures performance under concurrent load
// This simulates real-world scenarios with multiple clients/requests.
//
// Expected behavior:
// - InMemory: Excellent scaling (Go channels are highly concurrent)
// - Stdio: Poor (single pipe, serialization bottleneck)
// - WebSocket: Good (multiplexing over single connection)
// - HTTP: Excellent (connection pooling, concurrent requests)
// - gRPC: Excellent (HTTP/2 multiplexing, stream-per-request)

func BenchmarkInMemoryTransportConcurrency(b *testing.B) {
	clientTransport, serverTransport := NewInMemoryTransports()

	clientConn, _ := clientTransport.Connect(context.Background())
	defer clientConn.Close()

	serverConn, _ := serverTransport.Connect(context.Background())
	defer serverConn.Close()

	// Server echo
	go func() {
		for {
			msg, err := serverConn.Read(context.Background())
			if err != nil {
				return
			}
			serverConn.Write(context.Background(), msg)
		}
	}()

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			msg, _ := jsonrpc2.NewCall(jsonrpc2.Int64ID(int64(i)), "test", nil)
			if err := clientConn.Write(context.Background(), msg); err != nil {
				b.Logf("Write failed: %v", err)
				return
			}
			if _, err := clientConn.Read(context.Background()); err != nil {
				b.Logf("Read failed: %v", err)
				return
			}
			i++
		}
	})
}

func BenchmarkWebSocketTransportConcurrency(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{Subprotocols: []string{"mcp"}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Echo loop
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(messageType, data); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// This benchmark creates separate WebSocket connections per goroutine to
	// simulate many clients (not single-connection multiplexing). Use this to
	// evaluate server and TCP stack behavior under high concurrency.
	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		transport := &WebSocketClientTransport{URL: wsURL}
		clientConn, err := transport.Connect(context.Background())
		if err != nil {
			b.Logf("Failed to connect: %v", err)
			return
		}
		defer clientConn.Close()

		i := 0
		for pb.Next() {
			msg, _ := jsonrpc2.NewCall(jsonrpc2.Int64ID(int64(i)), "test", nil)
			if err := clientConn.Write(context.Background(), msg); err != nil {
				b.Logf("Write failed: %v", err)
				return
			}
			if _, err := clientConn.Read(context.Background()); err != nil {
				b.Logf("Read failed: %v", err)
				return
			}
			i++
		}
	})
}

// BenchmarkWebSocketOptimizations tests specific optimization strategies

func BenchmarkWebSocketWriteOptimization(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{Subprotocols: []string{"mcp"}}
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()

		// Just consume messages
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	transport := &WebSocketClientTransport{URL: wsURL}
	clientConn, _ := transport.Connect(context.Background())
	defer clientConn.Close()

	ctx := context.Background()
	msg, _ := jsonrpc2.NewCall(jsonrpc2.Int64ID(1), "test", nil)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := clientConn.Write(ctx, msg); err != nil {
			b.Fatalf("Write failed: %v", err)
		}
	}
}

func BenchmarkWebSocketEncodingOverhead(b *testing.B) {
	// Measure JSON encoding cost separately
	msg, _ := jsonrpc2.NewCall(jsonrpc2.Int64ID(1), "test", map[string]interface{}{
		"data": strings.Repeat("x", 1024),
	})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := jsonrpc.EncodeMessage(msg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWebSocketFramingOverhead(b *testing.B) {
	// Measure WebSocket framing cost
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	dialer := websocket.DefaultDialer
	conn, _, _ := dialer.Dial(wsURL, nil)
	defer conn.Close()

	data := []byte(strings.Repeat("x", 1024))

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWebSocketMicroEcho is a minimal in-process WebSocket echo benchmark
// that measures JSON encoding + WebSocket framing + round-trip without going
// through the higher-level MCP io/readBatch or EventStore paths that call
// io.ReadAll. This isolates transport-level costs.
func BenchmarkWebSocketMicroEcho(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		b.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// Prepare a message payload of moderate size to include encoding cost.
	msg, _ := jsonrpc2.NewCall(jsonrpc2.Int64ID(1), "test", map[string]interface{}{
		"data": strings.Repeat("x", 1024),
	})

	var buf bytes.Buffer

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buf.Reset()
		if err := jsonrpc.EncodeMessageTo(&buf, msg); err != nil {
			b.Fatalf("encode failed: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, buf.Bytes()); err != nil {
			b.Fatalf("write failed: %v", err)
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			b.Fatalf("read failed: %v", err)
		}
	}
}

// BenchmarkWebSocketPooling tests connection pooling benefits
func BenchmarkWebSocketConnectionReuse(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{Subprotocols: []string{"mcp"}}
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()

		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			conn.WriteMessage(messageType, data)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	b.Run("SingleConnection", func(b *testing.B) {
		transport := &WebSocketClientTransport{URL: wsURL}
		clientConn, _ := transport.Connect(context.Background())
		defer clientConn.Close()

		msg, _ := jsonrpc2.NewCall(jsonrpc2.Int64ID(1), "test", nil)
		ctx := context.Background()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			clientConn.Write(ctx, msg)
			clientConn.Read(ctx)
		}
	})

	b.Run("NewConnectionPerRequest", func(b *testing.B) {
		msg, _ := jsonrpc2.NewCall(jsonrpc2.Int64ID(1), "test", nil)
		ctx := context.Background()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			transport := &WebSocketClientTransport{URL: wsURL}
			clientConn, _ := transport.Connect(ctx)
			clientConn.Write(ctx, msg)
			clientConn.Read(ctx)
			clientConn.Close()
		}
	})
}
