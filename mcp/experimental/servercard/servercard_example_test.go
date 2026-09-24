// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package servercard_test

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp/experimental/servercard"
)

// !+build

func ExampleBuildServerCard() {
	impl := &mcp.Implementation{
		Name:       "com.example/dice-roller",
		Title:      "Dice Roller",
		Version:    "1.0.0",
		WebsiteURL: "https://dice.example.com",
	}
	card, err := servercard.BuildServerCard(impl,
		servercard.WithName("com.example/dice-roller"),
		servercard.WithDescription("Rolls dice for tabletop games."),
		servercard.WithRemotes(servercard.Remote{
			Type: servercard.RemoteTypeStreamableHTTP,
			URL:  "https://dice.example.com/mcp",
		}),
		servercard.WithRepository(servercard.Repository{
			URL:    "https://github.com/example/dice-roller",
			Source: "github",
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(card.Name, card.Version)
	fmt.Println(card.Remotes[0].URL)
	// Output:
	// com.example/dice-roller 1.0.0
	// https://dice.example.com/mcp
}

// !-build

func ExampleMount() {
	card, err := servercard.BuildServerCard(
		&mcp.Implementation{Name: "com.example/dice-roller", Version: "1.0.0"},
		servercard.WithName("com.example/dice-roller"),
		servercard.WithDescription("Rolls dice for tabletop games."),
	)
	if err != nil {
		log.Fatal(err)
	}

	// !+serve

	mux := http.NewServeMux()
	servercard.Mount(mux, "/mcp/server-card", card)

	// !-serve

	req := httptest.NewRequest(http.MethodGet, "https://dice.example.com/mcp/server-card", nil)
	req.Header.Set("Accept", servercard.MediaType)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	fmt.Println(rec.Code)
	fmt.Println(rec.Header().Get("Content-Type"))
	fmt.Println(rec.Header().Get("Cache-Control"))
	fmt.Println(rec.Header().Get("Access-Control-Allow-Origin"))
	fmt.Println(rec.Header().Get("ETag") != "")
	// Output:
	// 200
	// application/mcp-server-card+json
	// public, max-age=3600
	// *
	// true
}

// !+static

func Example_staticFile() {
	card, err := servercard.BuildServerCard(
		&mcp.Implementation{Name: "com.example/dice-roller", Version: "1.0.0"},
		servercard.WithName("com.example/dice-roller"),
		servercard.WithDescription("Rolls dice for tabletop games."),
	)
	if err != nil {
		log.Fatal(err)
	}

	data, err := json.MarshalIndent(card, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile("server-card.json", data, 0o644); err != nil {
		log.Fatal(err)
	}
}

// !-static
