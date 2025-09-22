// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func BenchmarkStreamableServing(b *testing.B) {
	// This benchmark measures how fast we can handle a single tool on a
	// streamable server, including tool validation and stream management.
	customSchemas := map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[Probability](): {Type: "number", Minimum: jsonschema.Ptr(0.0), Maximum: jsonschema.Ptr(1.0)},
		reflect.TypeFor[WeatherType](): {Type: "string", Enum: []any{Sunny, PartlyCloudy, Cloudy, Rainy, Snowy}},
	}
	opts := &jsonschema.ForOptions{TypeSchemas: customSchemas}
	in, err := jsonschema.For[WeatherInput](opts)
	if err != nil {
		b.Fatal(err)
	}
	out, err := jsonschema.For[WeatherOutput](opts)
	if err != nil {
		b.Fatal(err)
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:         "weather",
		InputSchema:  in,
		OutputSchema: out,
	}, WeatherTool)

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := mcp.NewClient(testImpl, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer session.Close()
	b.ResetTimer()
	for range b.N {
		if _, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "weather",
			Arguments: WeatherInput{
				Location: Location{Name: "somewhere"},
				Days:     7,
			},
		}); err != nil {
			b.Errorf("CallTool failed: %v", err)
		}
	}
}
