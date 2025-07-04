// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"

	_ "github.com/lib/pq" // PostgreSQL driver
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var httpAddr = flag.String("http", "", "if set, use streamable HTTP at this address, instead of stdin/stdout")

const SCHEMA_PATH = "schema"

type PostgresServer struct {
	db              *sql.DB
	resourceBaseURL *url.URL
}

// QueryArgs represents the arguments for the SQL query tool
type QueryArgs struct {
	SQL string `json:"sql"`
}

// NewPostgresServer creates a new PostgreSQL MCP server
func NewPostgresServer(databaseURL string) (*PostgresServer, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	resourceBaseURL, err := url.Parse(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}
	resourceBaseURL.Scheme = "postgres"
	resourceBaseURL.User = nil // Remove credentials for security

	return &PostgresServer{
		db:              db,
		resourceBaseURL: resourceBaseURL,
	}, nil
}

// Close closes the database connection
func (ps *PostgresServer) Close() error {
	return ps.db.Close()
}

// ListTables returns all tables in the public schema as resources
func (ps *PostgresServer) ListTables(ctx context.Context) ([]*mcp.ServerResource, error) {
	query := "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'"
	rows, err := ps.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}
	defer rows.Close()

	var resources []*mcp.ServerResource
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			return nil, fmt.Errorf("failed to scan table name: %w", err)
		}

		resourceURI := fmt.Sprintf("%s/%s/%s", ps.resourceBaseURL.String(), tableName, SCHEMA_PATH)
		resource := &mcp.ServerResource{
			Resource: &mcp.Resource{
				URI:      resourceURI,
				MIMEType: "application/json",
				Name:     fmt.Sprintf(`"%s" database schema`, tableName),
			},
			Handler: ps.readTableSchema,
		}
		resources = append(resources, resource)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tables: %w", err)
	}

	return resources, nil
}

// readTableSchema handles reading table schema information
func (ps *PostgresServer) readTableSchema(ctx context.Context, ss *mcp.ServerSession, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	resourceURL, err := url.Parse(params.URI)
	if err != nil {
		return nil, fmt.Errorf("invalid resource URI: %w", err)
	}

	pathComponents := []string{}
	for _, component := range []string{resourceURL.Path} {
		if component != "" {
			pathComponents = append(pathComponents, component)
		}
	}

	// Parse path: /tableName/schema
	parts := []string{}
	for _, part := range pathComponents {
		if part == "/" {
			continue
		}
		subParts := []string{}
		for _, subPart := range []string{part} {
			if subPart != "" {
				for _, p := range []string{subPart} {
					if p != "/" {
						subParts = append(subParts, p)
					}
				}
			}
		}
		parts = append(parts, subParts...)
	}

	// Extract table name and schema path from URI
	urlPath := resourceURL.Path
	if urlPath == "" {
		return nil, fmt.Errorf("empty URI path")
	}

	// Remove leading slash and split by '/'
	trimmedPath := urlPath
	if trimmedPath[0] == '/' {
		trimmedPath = trimmedPath[1:]
	}
	pathParts := []string{}
	for _, part := range []string{trimmedPath} {
		if part != "" {
			// Split by '/'
			for i, p := range []string{part} {
				if i == 0 {
					subparts := []string{}
					current := ""
					for _, r := range p {
						if r == '/' {
							if current != "" {
								subparts = append(subparts, current)
								current = ""
							}
						} else {
							current += string(r)
						}
					}
					if current != "" {
						subparts = append(subparts, current)
					}
					pathParts = append(pathParts, subparts...)
				}
			}
		}
	}

	if len(pathParts) < 2 {
		return nil, fmt.Errorf("invalid resource URI format: expected /tableName/schema")
	}

	tableName := pathParts[len(pathParts)-2]
	schema := pathParts[len(pathParts)-1]

	if schema != SCHEMA_PATH {
		return nil, fmt.Errorf("invalid resource URI: expected schema path")
	}

	// Query table columns
	query := "SELECT column_name, data_type FROM information_schema.columns WHERE table_name = $1 ORDER BY ordinal_position"
	rows, err := ps.db.QueryContext(ctx, query, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to query table schema: %w", err)
	}
	defer rows.Close()

	type ColumnInfo struct {
		ColumnName string `json:"column_name"`
		DataType   string `json:"data_type"`
	}

	var columns []ColumnInfo
	for rows.Next() {
		var column ColumnInfo
		if err := rows.Scan(&column.ColumnName, &column.DataType); err != nil {
			return nil, fmt.Errorf("failed to scan column info: %w", err)
		}
		columns = append(columns, column)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating columns: %w", err)
	}

	jsonData, err := json.MarshalIndent(columns, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal columns to JSON: %w", err)
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      params.URI,
				MIMEType: "application/json",
				Text:     string(jsonData),
			},
		},
	}, nil
}

// QueryTool executes a read-only SQL query
func (ps *PostgresServer) QueryTool(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[QueryArgs]) (*mcp.CallToolResultFor[struct{}], error) {
	sqlQuery := params.Arguments.SQL

	// Start a read-only transaction
	tx, err := ps.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return &mcp.CallToolResultFor[struct{}]{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Failed to start transaction: %v", err)},
			},
			IsError: true,
		}, nil
	}
	defer tx.Rollback() // Always rollback read-only transaction

	rows, err := tx.QueryContext(ctx, sqlQuery)
	if err != nil {
		return &mcp.CallToolResultFor[struct{}]{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Query execution failed: %v", err)},
			},
			IsError: true,
		}, nil
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return &mcp.CallToolResultFor[struct{}]{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Failed to get columns: %v", err)},
			},
			IsError: true,
		}, nil
	}

	// Prepare result slice
	var results []map[string]interface{}

	// Scan rows
	for rows.Next() {
		// Create slice of interface{} to hold column values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return &mcp.CallToolResultFor[struct{}]{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to scan row: %v", err)},
				},
				IsError: true,
			}, nil
		}

		// Convert to map
		row := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return &mcp.CallToolResultFor[struct{}]{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Error iterating rows: %v", err)},
			},
			IsError: true,
		}, nil
	}

	// Ensure results is not nil for proper JSON marshaling
	if results == nil {
		results = []map[string]interface{}{}
	}

	// Convert results to JSON
	jsonData, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return &mcp.CallToolResultFor[struct{}]{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Failed to marshal results: %v", err)},
			},
			IsError: true,
		}, nil
	}

	return &mcp.CallToolResultFor[struct{}]{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(jsonData)},
		},
	}, nil
}

func main() {
	flag.Parse()

	// Get database URL from environment variable or command line
	var databaseURL string

	// First try environment variable
	if envURL := os.Getenv("DATABASE_URL"); envURL != "" {
		databaseURL = envURL
		log.Printf("Using DATABASE_URL from environment")
	} else {
		// Fall back to command line argument
		args := os.Args[1:]
		if len(args) == 0 {
			// Default to local development database if not specified
			databaseURL = "postgres://testuser:testpass@localhost:5432/testdb?sslmode=disable"
			log.Printf("No DATABASE_URL or command line argument provided, using default: %s", databaseURL)
		} else {
			databaseURL = args[0]
			log.Printf("Using database URL from command line")
		}
	}

	// Create PostgreSQL server
	postgresServer, err := NewPostgresServer(databaseURL)
	if err != nil {
		log.Fatalf("Failed to create PostgreSQL server: %v", err)
	}
	defer postgresServer.Close()

	log.Printf("Connected to PostgreSQL database successfully")

	// Create MCP server
	server := mcp.NewServer("postgres", "0.1.0", nil)

	// Add the query tool
	server.AddTools(mcp.NewServerTool("query", "Run a read-only SQL query", postgresServer.QueryTool, mcp.Input(
		mcp.Property("sql", mcp.Description("The SQL query to execute")),
	)))

	// Get and add resources (tables) dynamically
	ctx := context.Background()
	resources, err := postgresServer.ListTables(ctx)
	if err != nil {
		log.Fatalf("Failed to list database tables: %v", err)
	}
	server.AddResources(resources...)

	// Start server
	if *httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		log.Printf("PostgreSQL MCP server listening at %s", *httpAddr)
		http.ListenAndServe(*httpAddr, handler)
	} else {
		log.Printf("PostgreSQL MCP server running on stdio")
		t := mcp.NewLoggingTransport(mcp.NewStdioTransport(), os.Stderr)
		if err := server.Run(context.Background(), t); err != nil {
			log.Printf("Server failed: %v", err)
		}
	}
}
