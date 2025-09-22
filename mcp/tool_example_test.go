// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ExampleAddTool_customMarshalling() {
	// Sometimes when you want to customize the input or output schema for a
	// tool, you need to customize the schema of a single helper type that's used
	// in several places.
	//
	// For example, suppose you had a type that marshals/unmarshals like a
	// time.Time, and that type was used multiple times in your tool input.
	type MyDate struct {
		time.Time
	}
	type Input struct {
		Query string `json:"query,omitempty"`
		Start MyDate `json:"start,omitempty"`
		End   MyDate `json:"end,omitempty"`
	}

	// In this case, you can use jsonschema.For along with jsonschema.ForOptions
	// to customize the schema inference for your custom type.
	inputSchema, err := jsonschema.For[Input](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{
			reflect.TypeFor[MyDate](): {Type: "string"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	toolHandler := func(context.Context, *mcp.CallToolRequest, Input) (*mcp.CallToolResult, any, error) {
		panic("not implemented")
	}
	mcp.AddTool(server, &mcp.Tool{Name: "my_tool", InputSchema: inputSchema}, toolHandler)

	ctx := context.Background()
	session, err := connect(ctx, server) // create an in-memory connection
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		schemaJSON, err := json.MarshalIndent(t.InputSchema, "", "\t")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(t.Name, string(schemaJSON))
	}
	// Output:
	// my_tool {
	// 	"type": "object",
	// 	"properties": {
	// 		"end": {
	// 			"type": "string"
	// 		},
	// 		"query": {
	// 			"type": "string"
	// 		},
	// 		"start": {
	// 			"type": "string"
	// 		}
	// 	},
	// 	"additionalProperties": false
	// }
}

type Location struct {
	Name      string   `json:"name"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
}

type Forecast struct {
	Forecast string      `json:"forecast" jsonschema:"description of the day's weather"`
	Type     WeatherType `json:"type" jsonschema:"type of weather"`
	Rain     float64     `json:"rain" jsonschema:"probability of rain, between 0 and 1"`
	High     float64     `json:"high" jsonschema:"high temperature"`
	Low      float64     `json:"low" jsonschema:"low temperature"`
}

type WeatherType string

const (
	Sunny        WeatherType = "sun"
	PartlyCloudy WeatherType = "partly_cloudy"
	Cloudy       WeatherType = "clouds"
	Rainy        WeatherType = "rain"
	Snowy        WeatherType = "snow"
)

type Probability float64

// !+weathertool

type WeatherInput struct {
	Location Location `json:"location" jsonschema:"user location"`
	Days     int      `json:"days" jsonschema:"number of days to forecast"`
}

type WeatherOutput struct {
	Summary       string      `json:"summary" jsonschema:"a summary of the weather forecast"`
	Confidence    Probability `json:"confidence" jsonschema:"confidence, between 0 and 1"`
	AsOf          time.Time   `json:"asOf" jsonschema:"the time the weather was computed"`
	DailyForecast []Forecast  `json:"dailyForecast" jsonschema:"the daily forecast"`
	Source        string      `json:"source,omitempty" jsonschema:"the organization providing the weather forecast"`
}

func WeatherTool(ctx context.Context, req *mcp.CallToolRequest, in WeatherInput) (*mcp.CallToolResult, WeatherOutput, error) {
	perfectWeather := WeatherOutput{
		Summary:    "perfect",
		Confidence: 1.0,
		AsOf:       time.Now(),
	}
	for range in.Days {
		perfectWeather.DailyForecast = append(perfectWeather.DailyForecast, Forecast{
			Forecast: "another perfect day",
			Type:     Sunny,
			Rain:     0.0,
			High:     72.0,
			Low:      72.0,
		})
	}
	return nil, perfectWeather, nil
}

// !-weathertool

func ExampleAddTool_complexSchema() {
	// This example demonstrates a tool with a more 'realistic' input and output
	// schema. We use a combination of techniques to tune our input and output
	// schemas.

	// !+customschemas

	// Distinguished Go types allow custom schemas to be reused during inference.
	customSchemas := map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[Probability](): {Type: "number", Minimum: jsonschema.Ptr(0.0), Maximum: jsonschema.Ptr(1.0)},
		reflect.TypeFor[WeatherType](): {Type: "string", Enum: []any{Sunny, PartlyCloudy, Cloudy, Rainy, Snowy}},
	}
	opts := &jsonschema.ForOptions{TypeSchemas: customSchemas}
	in, err := jsonschema.For[WeatherInput](opts)
	if err != nil {
		log.Fatal(err)
	}

	// Furthermore, we can tweak the inferred schema, in this case limiting
	// forecasts to 0-10 days.
	daysSchema := in.Properties["days"]
	daysSchema.Minimum = jsonschema.Ptr(0.0)
	daysSchema.Maximum = jsonschema.Ptr(10.0)

	// Output schema inference can reuse our custom schemas from input inference.
	out, err := jsonschema.For[WeatherOutput](opts)
	if err != nil {
		log.Fatal(err)
	}

	// Now add our tool to a server. Since we've customized the schemas, we need
	// to override the default schema inference.
	server := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:         "weather",
		InputSchema:  in,
		OutputSchema: out,
	}, WeatherTool)

	// !-customschemas

	ctx := context.Background()
	session, err := connect(ctx, server) // create an in-memory connection
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	// Check that the client observes the correct schemas.
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		// Formatting the entire schemas would be too much output.
		// Just check that our customizations were effective.
		fmt.Println("max days:", *t.InputSchema.Properties["days"].Maximum)
		fmt.Println("max confidence:", *t.OutputSchema.Properties["confidence"].Maximum)
		fmt.Println("weather types:", t.OutputSchema.Properties["dailyForecast"].Items.Properties["type"].Enum)
	}
	// Output:
	// max days: 10
	// max confidence: 1
	// weather types: [sun partly_cloudy clouds rain snow]
}

func connect(ctx context.Context, server *mcp.Server) (*mcp.ClientSession, error) {
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
	return client.Connect(ctx, t2, nil)
}
