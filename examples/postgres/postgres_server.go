// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/lib/pq" // PostgreSQL driver
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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

// parseResourceURI extracts the table name from a MCP resource URI.
//
// The expected URI format is: postgres://host:port/database/tableName/schema
// where:
//   - postgres://host:port/database is the base URL
//   - tableName is the name of the database table
//   - schema is the literal string "schema" (defined by SCHEMA_PATH constant)
//
// Examples:
//   - "postgres://localhost:5432/mydb/users/schema" → tableName: "users"
//   - "postgres://localhost:5432/mydb/order_items/schema" → tableName: "order_items"
//
// Returns the extracted table name or an error if the URI format is invalid.
func parseResourceURI(resourceURI string) (tableName string, err error) {
	resourceURL, err := url.Parse(resourceURI)
	if err != nil {
		return "", fmt.Errorf("invalid resource URI: %w", err)
	}

	// Extract path from URI
	urlPath := resourceURL.Path
	if urlPath == "" {
		return "", fmt.Errorf("empty URI path")
	}

	// Remove leading slash and split by '/'
	trimmedPath := strings.TrimPrefix(urlPath, "/")

	// Split path into components using strings.Split for better performance (O(n) vs O(n²))
	var pathParts []string
	if trimmedPath != "" {
		pathParts = strings.Split(trimmedPath, "/")
		// Filter out empty parts that might result from consecutive slashes
		var filteredParts []string
		for _, part := range pathParts {
			if part != "" {
				filteredParts = append(filteredParts, part)
			}
		}
		pathParts = filteredParts
	}

	// Validate path format: expect at least 2 parts (tableName/schema)
	if len(pathParts) < 2 {
		return "", fmt.Errorf("invalid resource URI format: expected /tableName/schema, got: %s", urlPath)
	}

	// Extract table name and schema path
	tableName = pathParts[len(pathParts)-2]
	schema := pathParts[len(pathParts)-1]

	// Validate schema path
	if schema != SCHEMA_PATH {
		return "", fmt.Errorf("invalid resource URI: expected schema path '%s', got '%s'", SCHEMA_PATH, schema)
	}

	return tableName, nil
}

// readTableSchema handles reading table schema information
func (ps *PostgresServer) readTableSchema(ctx context.Context, ss *mcp.ServerSession, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	// Parse the resource URI to extract table name
	tableName, err := parseResourceURI(params.URI)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback() // Always rollback read-only transaction

	rows, err := tx.QueryContext(ctx, sqlQuery)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	// Prepare result slice
	var results []map[string]any

	// Scan rows
	for rows.Next() {
		// Create slice of any to hold column values
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Convert to map
		row := make(map[string]any)
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
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	// Ensure results is not nil for proper JSON marshaling
	if results == nil {
		results = []map[string]any{}
	}

	// Convert results to JSON
	jsonData, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal results: %w", err)
	}

	return &mcp.CallToolResultFor[struct{}]{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(jsonData)},
		},
	}, nil
}
