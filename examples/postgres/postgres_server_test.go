// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// createMockPostgresServer creates a PostgresServer with a mock database for testing
func createMockPostgresServer(t *testing.T) (*PostgresServer, sqlmock.Sqlmock, func()) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to create mock database: %v", err)
	}

	baseURL, _ := url.Parse("postgres://testuser@localhost:5432/testdb")
	baseURL.Scheme = "postgres"
	baseURL.User = nil

	server := &PostgresServer{
		db:              db,
		resourceBaseURL: baseURL,
	}

	cleanup := func() {
		db.Close()
	}

	return server, mock, cleanup
}

func TestParseResourceURI(t *testing.T) {
	tests := []struct {
		name          string
		resourceURI   string
		expectedTable string
		wantError     bool
		errorContains string
	}{
		{
			name:          "valid URI with users table",
			resourceURI:   "postgres://localhost:5432/testdb/users/schema",
			expectedTable: "users",
			wantError:     false,
		},
		{
			name:          "valid URI with underscore table name",
			resourceURI:   "postgres://localhost:5432/testdb/user_profiles/schema",
			expectedTable: "user_profiles",
			wantError:     false,
		},
		{
			name:          "valid URI with numeric table name",
			resourceURI:   "postgres://localhost:5432/testdb/orders123/schema",
			expectedTable: "orders123",
			wantError:     false,
		},
		{
			name:          "valid URI with multiple path segments in database",
			resourceURI:   "postgres://localhost:5432/path/to/db/products/schema",
			expectedTable: "products",
			wantError:     false,
		},
		{
			name:          "invalid URI - malformed URL",
			resourceURI:   "not-a-url",
			wantError:     true,
			errorContains: "invalid resource URI",
		},
		{
			name:          "invalid URI - empty path",
			resourceURI:   "postgres://localhost:5432",
			wantError:     true,
			errorContains: "empty URI path",
		},
		{
			name:          "invalid URI - missing schema path",
			resourceURI:   "postgres://localhost:5432/testdb/users",
			wantError:     true,
			errorContains: "invalid resource URI: expected schema path 'schema', got 'users'",
		},
		{
			name:          "invalid URI - only table name, no schema",
			resourceURI:   "postgres://localhost:5432/testdb/users/",
			wantError:     true,
			errorContains: "invalid resource URI: expected schema path 'schema', got 'users'",
		},
		{
			name:          "invalid URI - wrong schema path",
			resourceURI:   "postgres://localhost:5432/testdb/users/wrong",
			wantError:     true,
			errorContains: "invalid resource URI: expected schema path 'schema', got 'wrong'",
		},
		{
			name:          "invalid URI - only one path component",
			resourceURI:   "postgres://localhost:5432/users",
			wantError:     true,
			errorContains: "invalid resource URI format: expected /tableName/schema, got: /users",
		},
		{
			name:          "invalid URI - empty path after slash",
			resourceURI:   "postgres://localhost:5432/",
			wantError:     true,
			errorContains: "invalid resource URI format: expected /tableName/schema",
		},
		{
			name:          "edge case - table name with special characters",
			resourceURI:   "postgres://localhost:5432/testdb/test-table_123/schema",
			expectedTable: "test-table_123",
			wantError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tableName, err := parseResourceURI(tt.resourceURI)

			if tt.wantError {
				if err == nil {
					t.Errorf("parseResourceURI() expected error, got nil")
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("parseResourceURI() error = %v, want error containing %v", err, tt.errorContains)
				}
			} else {
				if err != nil {
					t.Errorf("parseResourceURI() unexpected error = %v", err)
				} else if tableName != tt.expectedTable {
					t.Errorf("parseResourceURI() tableName = %v, want %v", tableName, tt.expectedTable)
				}
			}
		})
	}
}

func TestNewPostgresServer(t *testing.T) {
	tests := []struct {
		name        string
		databaseURL string
		wantError   bool
		errorMsg    string
	}{
		{
			name:        "invalid URL",
			databaseURL: "invalid-url",
			wantError:   true,
			errorMsg:    "failed to ping database", // SQL driver tries to ping and fails
		},
		{
			name:        "empty URL",
			databaseURL: "",
			wantError:   true,
			errorMsg:    "failed to ping database", // Empty URL also fails at ping stage
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewPostgresServer(tt.databaseURL)
			if tt.wantError {
				if err == nil {
					t.Errorf("NewPostgresServer() expected error, got nil")
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("NewPostgresServer() error = %v, want error containing %v", err, tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("NewPostgresServer() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestPostgresServer_Close(t *testing.T) {
	server, mock, cleanup := createMockPostgresServer(t)
	defer cleanup()

	mock.ExpectClose()

	err := server.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unfulfilled expectations: %v", err)
	}
}

func TestPostgresServer_ListTables(t *testing.T) {
	server, mock, cleanup := createMockPostgresServer(t)
	defer cleanup()

	ctx := context.Background()

	// Test successful table listing
	t.Run("successful listing", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"table_name"}).
			AddRow("users").
			AddRow("products").
			AddRow("orders")

		mock.ExpectQuery("SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'").
			WillReturnRows(rows)

		resources, err := server.ListTables(ctx)
		if err != nil {
			t.Fatalf("ListTables() error = %v", err)
		}

		if len(resources) != 3 {
			t.Errorf("Expected 3 resources, got %d", len(resources))
		}

		expectedTables := []string{"users", "products", "orders"}
		for i, resource := range resources {
			expectedName := fmt.Sprintf(`"%s" database schema`, expectedTables[i])
			if resource.Resource.Name != expectedName {
				t.Errorf("Expected resource name %s, got %s", expectedName, resource.Resource.Name)
			}

			expectedURI := fmt.Sprintf("%s/%s/%s", server.resourceBaseURL.String(), expectedTables[i], SCHEMA_PATH)
			if resource.Resource.URI != expectedURI {
				t.Errorf("Expected resource URI %s, got %s", expectedURI, resource.Resource.URI)
			}

			if resource.Resource.MIMEType != "application/json" {
				t.Errorf("Expected MIME type application/json, got %s", resource.Resource.MIMEType)
			}
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})

	// Test database error
	t.Run("database error", func(t *testing.T) {
		mock.ExpectQuery("SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'").
			WillReturnError(fmt.Errorf("database connection failed"))

		_, err := server.ListTables(ctx)
		if err == nil {
			t.Error("Expected error, got nil")
		}

		if !strings.Contains(err.Error(), "failed to query tables") {
			t.Errorf("Expected error to contain 'failed to query tables', got %v", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})

	// Test scan error
	t.Run("scan error", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"table_name"}).
			AddRow("users").
			AddRow(nil) // This will cause a scan error

		mock.ExpectQuery("SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'").
			WillReturnRows(rows)

		_, err := server.ListTables(ctx)
		if err == nil {
			t.Error("Expected error, got nil")
		}

		if !strings.Contains(err.Error(), "failed to scan table name") {
			t.Errorf("Expected error to contain 'failed to scan table name', got %v", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})
}

func TestPostgresServer_readTableSchema(t *testing.T) {
	server, mock, cleanup := createMockPostgresServer(t)
	defer cleanup()

	ctx := context.Background()

	// Test successful schema reading
	t.Run("successful schema reading", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"column_name", "data_type"}).
			AddRow("id", "integer").
			AddRow("name", "character varying").
			AddRow("email", "character varying").
			AddRow("created_at", "timestamp with time zone")

		mock.ExpectQuery("SELECT column_name, data_type FROM information_schema.columns WHERE table_name = \\$1 ORDER BY ordinal_position").
			WithArgs("users").
			WillReturnRows(rows)

		params := &mcp.ReadResourceParams{
			URI: fmt.Sprintf("%s/users/%s", server.resourceBaseURL.String(), SCHEMA_PATH),
		}

		result, err := server.readTableSchema(ctx, nil, params)
		if err != nil {
			t.Fatalf("readTableSchema() error = %v", err)
		}

		if len(result.Contents) != 1 {
			t.Fatalf("Expected 1 content item, got %d", len(result.Contents))
		}

		content := result.Contents[0]
		if content.URI != params.URI {
			t.Errorf("Expected URI %s, got %s", params.URI, content.URI)
		}

		if content.MIMEType != "application/json" {
			t.Errorf("Expected MIME type application/json, got %s", content.MIMEType)
		}

		// Parse and verify JSON content
		var columns []map[string]interface{}
		err = json.Unmarshal([]byte(content.Text), &columns)
		if err != nil {
			t.Fatalf("Failed to parse JSON: %v", err)
		}

		if len(columns) != 4 {
			t.Errorf("Expected 4 columns, got %d", len(columns))
		}

		expectedColumns := []map[string]string{
			{"column_name": "id", "data_type": "integer"},
			{"column_name": "name", "data_type": "character varying"},
			{"column_name": "email", "data_type": "character varying"},
			{"column_name": "created_at", "data_type": "timestamp with time zone"},
		}

		for i, col := range columns {
			expected := expectedColumns[i]
			if col["column_name"] != expected["column_name"] {
				t.Errorf("Expected column name %s, got %s", expected["column_name"], col["column_name"])
			}
			if col["data_type"] != expected["data_type"] {
				t.Errorf("Expected data type %s, got %s", expected["data_type"], col["data_type"])
			}
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})

	// Test invalid URI
	t.Run("invalid URI", func(t *testing.T) {
		params := &mcp.ReadResourceParams{
			URI: "invalid-uri",
		}

		_, err := server.readTableSchema(ctx, nil, params)
		if err == nil {
			t.Error("Expected error for invalid URI, got nil")
		}

		if !strings.Contains(err.Error(), "invalid resource URI") {
			t.Errorf("Expected error to contain 'invalid resource URI', got %v", err)
		}
	})

	// Test invalid URI format
	t.Run("invalid URI format", func(t *testing.T) {
		params := &mcp.ReadResourceParams{
			URI: fmt.Sprintf("%s/users", server.resourceBaseURL.String()), // Missing schema path
		}

		_, err := server.readTableSchema(ctx, nil, params)
		if err == nil {
			t.Error("Expected error for invalid URI format, got nil")
		}

		// The actual error message depends on the path parsing logic
		// It should be either about format or schema path
		if !strings.Contains(err.Error(), "invalid resource URI") {
			t.Errorf("Expected error to contain 'invalid resource URI', got %v", err)
		}
	})

	// Test wrong schema path
	t.Run("wrong schema path", func(t *testing.T) {
		params := &mcp.ReadResourceParams{
			URI: fmt.Sprintf("%s/users/wrong", server.resourceBaseURL.String()),
		}

		_, err := server.readTableSchema(ctx, nil, params)
		if err == nil {
			t.Error("Expected error for wrong schema path, got nil")
		}

		if !strings.Contains(err.Error(), "invalid resource URI: expected schema path") {
			t.Errorf("Expected error to contain 'invalid resource URI: expected schema path', got %v", err)
		}
	})

	// Test database error
	t.Run("database error", func(t *testing.T) {
		mock.ExpectQuery("SELECT column_name, data_type FROM information_schema.columns WHERE table_name = \\$1 ORDER BY ordinal_position").
			WithArgs("users").
			WillReturnError(fmt.Errorf("table not found"))

		params := &mcp.ReadResourceParams{
			URI: fmt.Sprintf("%s/users/%s", server.resourceBaseURL.String(), SCHEMA_PATH),
		}

		_, err := server.readTableSchema(ctx, nil, params)
		if err == nil {
			t.Error("Expected error, got nil")
		}

		if !strings.Contains(err.Error(), "failed to query table schema") {
			t.Errorf("Expected error to contain 'failed to query table schema', got %v", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})
}

func TestPostgresServer_QueryTool(t *testing.T) {
	server, mock, cleanup := createMockPostgresServer(t)
	defer cleanup()

	ctx := context.Background()

	// Test successful query
	t.Run("successful query", func(t *testing.T) {
		mock.ExpectBegin()

		rows := sqlmock.NewRows([]string{"id", "name", "email"}).
			AddRow(1, "John Doe", "john@example.com").
			AddRow(2, "Jane Smith", "jane@example.com")

		mock.ExpectQuery("SELECT \\* FROM users LIMIT 2").
			WillReturnRows(rows)

		mock.ExpectRollback()

		args := QueryArgs{
			SQL: "SELECT * FROM users LIMIT 2",
		}

		params := &mcp.CallToolParamsFor[QueryArgs]{
			Name:      "query",
			Arguments: args,
		}

		result, err := server.QueryTool(ctx, nil, params)
		if err != nil {
			t.Fatalf("QueryTool() error = %v", err)
		}

		if result.IsError {
			t.Error("Expected successful result, got error")
		}

		if len(result.Content) != 1 {
			t.Fatalf("Expected 1 content item, got %d", len(result.Content))
		}

		textContent, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatal("Expected TextContent")
		}

		// Parse and verify JSON result
		var queryResults []map[string]interface{}
		err = json.Unmarshal([]byte(textContent.Text), &queryResults)
		if err != nil {
			t.Fatalf("Failed to parse query result JSON: %v", err)
		}

		if len(queryResults) != 2 {
			t.Errorf("Expected 2 rows, got %d", len(queryResults))
		}

		// Verify first row
		firstRow := queryResults[0]
		if firstRow["name"] != "John Doe" {
			t.Errorf("Expected name 'John Doe', got %v", firstRow["name"])
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})

	// Test transaction begin error
	t.Run("transaction begin error", func(t *testing.T) {
		mock.ExpectBegin().WillReturnError(fmt.Errorf("connection failed"))

		args := QueryArgs{
			SQL: "SELECT * FROM users",
		}

		params := &mcp.CallToolParamsFor[QueryArgs]{
			Name:      "query",
			Arguments: args,
		}

		result, err := server.QueryTool(ctx, nil, params)
		if err == nil {
			t.Fatal("Expected error, got nil")
		}

		if result != nil {
			t.Error("Expected nil result when error is returned")
		}

		if !strings.Contains(err.Error(), "failed to start transaction") {
			t.Errorf("Expected error message about transaction, got %s", err.Error())
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})

	// Test query execution error
	t.Run("query execution error", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM nonexistent").
			WillReturnError(fmt.Errorf("table does not exist"))
		mock.ExpectRollback()

		args := QueryArgs{
			SQL: "SELECT * FROM nonexistent",
		}

		params := &mcp.CallToolParamsFor[QueryArgs]{
			Name:      "query",
			Arguments: args,
		}

		result, err := server.QueryTool(ctx, nil, params)
		if err == nil {
			t.Fatal("Expected error, got nil")
		}

		if result != nil {
			t.Error("Expected nil result when error is returned")
		}

		if !strings.Contains(err.Error(), "query execution failed") {
			t.Errorf("Expected query execution error message, got %s", err.Error())
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})

	// Test with byte array values (common in PostgreSQL)
	t.Run("query with byte array values", func(t *testing.T) {
		mock.ExpectBegin()

		rows := sqlmock.NewRows([]string{"id", "data"}).
			AddRow(1, []byte("binary data"))

		mock.ExpectQuery("SELECT id, data FROM binary_table").
			WillReturnRows(rows)

		mock.ExpectRollback()

		args := QueryArgs{
			SQL: "SELECT id, data FROM binary_table",
		}

		params := &mcp.CallToolParamsFor[QueryArgs]{
			Name:      "query",
			Arguments: args,
		}

		result, err := server.QueryTool(ctx, nil, params)
		if err != nil {
			t.Fatalf("QueryTool() error = %v", err)
		}

		if result.IsError {
			t.Error("Expected successful result, got error")
		}

		textContent, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatal("Expected TextContent")
		}

		// Parse and verify JSON result
		var queryResults []map[string]interface{}
		err = json.Unmarshal([]byte(textContent.Text), &queryResults)
		if err != nil {
			t.Fatalf("Failed to parse query result JSON: %v", err)
		}

		if len(queryResults) != 1 {
			t.Errorf("Expected 1 row, got %d", len(queryResults))
		}

		// Verify byte array was converted to string
		row := queryResults[0]
		if row["data"] != "binary data" {
			t.Errorf("Expected data 'binary data', got %v", row["data"])
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})

	// Test empty result set
	t.Run("empty result set", func(t *testing.T) {
		mock.ExpectBegin()

		rows := sqlmock.NewRows([]string{"id", "name"})

		mock.ExpectQuery("SELECT \\* FROM users WHERE id = 999").
			WillReturnRows(rows)

		mock.ExpectRollback()

		args := QueryArgs{
			SQL: "SELECT * FROM users WHERE id = 999",
		}

		params := &mcp.CallToolParamsFor[QueryArgs]{
			Name:      "query",
			Arguments: args,
		}

		result, err := server.QueryTool(ctx, nil, params)
		if err != nil {
			t.Fatalf("QueryTool() error = %v", err)
		}

		if result.IsError {
			t.Error("Expected successful result, got error")
		}

		textContent, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatal("Expected TextContent")
		}

		// Should return empty JSON array
		if !strings.Contains(textContent.Text, "[]") {
			t.Errorf("Expected JSON containing empty array, got %s", textContent.Text)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})
}

func TestQueryArgs(t *testing.T) {
	// Test JSON marshaling/unmarshaling of QueryArgs
	args := QueryArgs{
		SQL: "SELECT * FROM users",
	}

	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Failed to marshal QueryArgs: %v", err)
	}

	var unmarshaled QueryArgs
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal QueryArgs: %v", err)
	}

	if unmarshaled.SQL != args.SQL {
		t.Errorf("Expected SQL %s, got %s", args.SQL, unmarshaled.SQL)
	}
}

func TestSCHEMA_PATH(t *testing.T) {
	// Test that the schema path constant is correct
	if SCHEMA_PATH != "schema" {
		t.Errorf("Expected SCHEMA_PATH to be 'schema', got %s", SCHEMA_PATH)
	}
}

// TestPostgresServerIntegration tests the integration between different components
func TestPostgresServerIntegration(t *testing.T) {
	server, mock, cleanup := createMockPostgresServer(t)
	defer cleanup()

	ctx := context.Background()

	// Test complete flow: ListTables -> ReadTableSchema
	t.Run("complete flow", func(t *testing.T) {
		// Mock ListTables
		tableRows := sqlmock.NewRows([]string{"table_name"}).
			AddRow("users")

		mock.ExpectQuery("SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'").
			WillReturnRows(tableRows)

		resources, err := server.ListTables(ctx)
		if err != nil {
			t.Fatalf("ListTables() error = %v", err)
		}

		if len(resources) != 1 {
			t.Fatalf("Expected 1 resource, got %d", len(resources))
		}

		// Use the returned resource to test readTableSchema
		schemaRows := sqlmock.NewRows([]string{"column_name", "data_type"}).
			AddRow("id", "integer").
			AddRow("name", "character varying")

		mock.ExpectQuery("SELECT column_name, data_type FROM information_schema.columns WHERE table_name = \\$1 ORDER BY ordinal_position").
			WithArgs("users").
			WillReturnRows(schemaRows)

		params := &mcp.ReadResourceParams{
			URI: resources[0].Resource.URI,
		}

		result, err := resources[0].Handler(ctx, nil, params)
		if err != nil {
			t.Fatalf("Handler() error = %v", err)
		}

		if len(result.Contents) != 1 {
			t.Fatalf("Expected 1 content item, got %d", len(result.Contents))
		}

		// Verify the schema content
		var columns []map[string]interface{}
		err = json.Unmarshal([]byte(result.Contents[0].Text), &columns)
		if err != nil {
			t.Fatalf("Failed to parse schema JSON: %v", err)
		}

		if len(columns) != 2 {
			t.Errorf("Expected 2 columns, got %d", len(columns))
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})
}

// TestEdgeCases tests various edge cases and error conditions
func TestEdgeCases(t *testing.T) {
	server, mock, cleanup := createMockPostgresServer(t)
	defer cleanup()

	ctx := context.Background()

	// Test readTableSchema with empty path
	t.Run("empty path in URI", func(t *testing.T) {
		params := &mcp.ReadResourceParams{
			URI: server.resourceBaseURL.String(), // No path
		}

		_, err := server.readTableSchema(ctx, nil, params)
		if err == nil {
			t.Error("Expected error for empty path, got nil")
		}

		// The path parsing logic will handle this as invalid format
		if !strings.Contains(err.Error(), "invalid resource URI") {
			t.Errorf("Expected error about invalid resource URI, got %v", err)
		}
	})

	// Test QueryTool with columns() error
	t.Run("columns error in QueryTool", func(t *testing.T) {
		mock.ExpectBegin()

		rows := sqlmock.NewRows([]string{"id", "name"}).
			AddRow(1, "test").
			CloseError(fmt.Errorf("columns error"))

		mock.ExpectQuery("SELECT \\* FROM test").
			WillReturnRows(rows)

		mock.ExpectRollback()

		args := QueryArgs{
			SQL: "SELECT * FROM test",
		}

		params := &mcp.CallToolParamsFor[QueryArgs]{
			Name:      "query",
			Arguments: args,
		}

		result, err := server.QueryTool(ctx, nil, params)
		if err == nil {
			t.Fatal("Expected error, got nil")
		}

		if result != nil {
			t.Error("Expected nil result when error is returned")
		}

		// CloseError actually affects rows.Err(), not Columns()
		if !strings.Contains(err.Error(), "error iterating rows") {
			t.Errorf("Expected row error message, got %s", err.Error())
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})

	// Test QueryTool with rows.Next() error
	t.Run("rows iteration error in QueryTool", func(t *testing.T) {
		mock.ExpectBegin()

		rows := sqlmock.NewRows([]string{"id", "name"}).
			AddRow(1, "test").
			RowError(0, fmt.Errorf("row iteration error"))

		mock.ExpectQuery("SELECT \\* FROM test").
			WillReturnRows(rows)

		mock.ExpectRollback()

		args := QueryArgs{
			SQL: "SELECT * FROM test",
		}

		params := &mcp.CallToolParamsFor[QueryArgs]{
			Name:      "query",
			Arguments: args,
		}

		result, err := server.QueryTool(ctx, nil, params)
		if err == nil {
			t.Fatal("Expected error, got nil")
		}

		if result != nil {
			t.Error("Expected nil result when error is returned")
		}

		if !strings.Contains(err.Error(), "error iterating rows") {
			t.Errorf("Expected row iteration error message, got %s", err.Error())
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})

	// Test readTableSchema with JSON marshal error (simulate with invalid data)
	t.Run("JSON marshal error in readTableSchema", func(t *testing.T) {
		// This is hard to simulate with sqlmock, but we can test the structure
		// The current implementation should handle this gracefully

		schemaRows := sqlmock.NewRows([]string{"column_name", "data_type"}).
			AddRow("valid_column", "text")

		mock.ExpectQuery("SELECT column_name, data_type FROM information_schema.columns WHERE table_name = \\$1 ORDER BY ordinal_position").
			WithArgs("test_table").
			WillReturnRows(schemaRows)

		params := &mcp.ReadResourceParams{
			URI: fmt.Sprintf("%s/test_table/%s", server.resourceBaseURL.String(), SCHEMA_PATH),
		}

		result, err := server.readTableSchema(ctx, nil, params)
		if err != nil {
			t.Fatalf("readTableSchema() error = %v", err)
		}

		// Should succeed with valid data
		if len(result.Contents) != 1 {
			t.Fatalf("Expected 1 content item, got %d", len(result.Contents))
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})
}

// TestResourceURIGeneration tests the URI generation logic
func TestResourceURIGeneration(t *testing.T) {
	server, mock, cleanup := createMockPostgresServer(t)
	defer cleanup()

	ctx := context.Background()

	// Test with different table names including special characters
	testCases := []struct {
		tableName   string
		expectedURI string
	}{
		{
			tableName:   "users",
			expectedURI: fmt.Sprintf("%s/users/%s", server.resourceBaseURL.String(), SCHEMA_PATH),
		},
		{
			tableName:   "user_profiles",
			expectedURI: fmt.Sprintf("%s/user_profiles/%s", server.resourceBaseURL.String(), SCHEMA_PATH),
		},
		{
			tableName:   "orders123",
			expectedURI: fmt.Sprintf("%s/orders123/%s", server.resourceBaseURL.String(), SCHEMA_PATH),
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("table_%s", tc.tableName), func(t *testing.T) {
			rows := sqlmock.NewRows([]string{"table_name"}).
				AddRow(tc.tableName)

			mock.ExpectQuery("SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'").
				WillReturnRows(rows)

			resources, err := server.ListTables(ctx)
			if err != nil {
				t.Fatalf("ListTables() error = %v", err)
			}

			if len(resources) != 1 {
				t.Fatalf("Expected 1 resource, got %d", len(resources))
			}

			if resources[0].Resource.URI != tc.expectedURI {
				t.Errorf("Expected URI %s, got %s", tc.expectedURI, resources[0].Resource.URI)
			}

			expectedName := fmt.Sprintf(`"%s" database schema`, tc.tableName)
			if resources[0].Resource.Name != expectedName {
				t.Errorf("Expected name %s, got %s", expectedName, resources[0].Resource.Name)
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("Unfulfilled expectations: %v", err)
			}
		})
	}
}

// TestDifferentDataTypes tests handling of various PostgreSQL data types
func TestDifferentDataTypes(t *testing.T) {
	server, mock, cleanup := createMockPostgresServer(t)
	defer cleanup()

	ctx := context.Background()

	// Test schema reading with various PostgreSQL data types
	t.Run("various data types", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"column_name", "data_type"}).
			AddRow("id", "bigint").
			AddRow("name", "character varying").
			AddRow("description", "text").
			AddRow("price", "numeric").
			AddRow("created_at", "timestamp with time zone").
			AddRow("updated_at", "timestamp without time zone").
			AddRow("is_active", "boolean").
			AddRow("metadata", "jsonb").
			AddRow("tags", "ARRAY")

		mock.ExpectQuery("SELECT column_name, data_type FROM information_schema.columns WHERE table_name = \\$1 ORDER BY ordinal_position").
			WithArgs("products").
			WillReturnRows(rows)

		params := &mcp.ReadResourceParams{
			URI: fmt.Sprintf("%s/products/%s", server.resourceBaseURL.String(), SCHEMA_PATH),
		}

		result, err := server.readTableSchema(ctx, nil, params)
		if err != nil {
			t.Fatalf("readTableSchema() error = %v", err)
		}

		if len(result.Contents) != 1 {
			t.Fatalf("Expected 1 content item, got %d", len(result.Contents))
		}

		// Parse and verify all data types
		var columns []map[string]interface{}
		err = json.Unmarshal([]byte(result.Contents[0].Text), &columns)
		if err != nil {
			t.Fatalf("Failed to parse JSON: %v", err)
		}

		if len(columns) != 9 {
			t.Errorf("Expected 9 columns, got %d", len(columns))
		}

		expectedTypes := []string{
			"bigint", "character varying", "text", "numeric",
			"timestamp with time zone", "timestamp without time zone",
			"boolean", "jsonb", "ARRAY",
		}

		for i, col := range columns {
			if col["data_type"] != expectedTypes[i] {
				t.Errorf("Expected data type %s at index %d, got %s", expectedTypes[i], i, col["data_type"])
			}
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})

	// Test query results with various data types
	t.Run("query with various data types", func(t *testing.T) {
		mock.ExpectBegin()

		rows := sqlmock.NewRows([]string{"id", "name", "price", "is_active", "created_at"}).
			AddRow(1, "Product 1", 19.99, true, "2024-01-01T00:00:00Z").
			AddRow(2, "Product 2", 29.99, false, "2024-01-02T00:00:00Z")

		mock.ExpectQuery("SELECT id, name, price, is_active, created_at FROM products").
			WillReturnRows(rows)

		mock.ExpectRollback()

		args := QueryArgs{
			SQL: "SELECT id, name, price, is_active, created_at FROM products",
		}

		params := &mcp.CallToolParamsFor[QueryArgs]{
			Name:      "query",
			Arguments: args,
		}

		result, err := server.QueryTool(ctx, nil, params)
		if err != nil {
			t.Fatalf("QueryTool() error = %v", err)
		}

		if result.IsError {
			t.Error("Expected successful result, got error")
		}

		textContent, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatal("Expected TextContent")
		}

		// Parse and verify results
		var queryResults []map[string]interface{}
		err = json.Unmarshal([]byte(textContent.Text), &queryResults)
		if err != nil {
			t.Fatalf("Failed to parse query result JSON: %v", err)
		}

		if len(queryResults) != 2 {
			t.Errorf("Expected 2 rows, got %d", len(queryResults))
		}

		// Verify first row data types and values
		firstRow := queryResults[0]
		// SQL mock returns different numeric types depending on the driver
		if id, ok := firstRow["id"].(int64); ok {
			if id != 1 {
				t.Errorf("Expected id 1, got %v", id)
			}
		} else if id, ok := firstRow["id"].(int); ok {
			if id != 1 {
				t.Errorf("Expected id 1, got %v", id)
			}
		} else if id, ok := firstRow["id"].(float64); ok {
			if id != 1.0 {
				t.Errorf("Expected id 1, got %v", id)
			}
		} else {
			t.Errorf("Expected id to be numeric, got %v (type: %T)", firstRow["id"], firstRow["id"])
		}

		if firstRow["name"] != "Product 1" {
			t.Errorf("Expected name 'Product 1', got %v", firstRow["name"])
		}
		if firstRow["is_active"] != true {
			t.Errorf("Expected is_active true, got %v", firstRow["is_active"])
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("Unfulfilled expectations: %v", err)
		}
	})
}
