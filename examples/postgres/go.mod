module github.com/modelcontextprotocol/go-sdk/examples/postgres

go 1.23.0

require (
	github.com/lib/pq v1.10.9
	github.com/modelcontextprotocol/go-sdk v0.0.0
)

require github.com/DATA-DOG/go-sqlmock v1.5.2

// Replace with local SDK for development
replace github.com/modelcontextprotocol/go-sdk => ../../
