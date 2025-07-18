#!/bin/bash

# PostgreSQL MCP Server Demo Script
# This script demonstrates the PostgreSQL MCP server functionality

set -e

echo "🐘 PostgreSQL MCP Server Demo"
echo "============================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check if Docker is running
if ! docker info >/dev/null 2>&1; then
    echo -e "${RED}❌ Docker is not running. Please start Docker first.${NC}"
    exit 1
fi

echo -e "${YELLOW}🚀 Starting PostgreSQL database with sample data...${NC}"
docker-compose up -d postgres

echo -e "${YELLOW}⏳ Waiting for PostgreSQL to be ready...${NC}"
sleep 10

# Check if PostgreSQL is ready
until docker-compose exec postgres pg_isready -U testuser -d testdb >/dev/null 2>&1; do
    echo "Waiting for PostgreSQL..."
    sleep 2
done

echo -e "${GREEN}✅ PostgreSQL is ready!${NC}"

# Build the MCP server
echo -e "${YELLOW}🔨 Building the MCP server...${NC}"
go build -o postgres-mcp-server main.go

echo -e "${GREEN}✅ MCP server built successfully!${NC}"

# Set the database URL
export DATABASE_URL="postgres://testuser:testpass@localhost:5432/testdb?sslmode=disable"

echo -e "${YELLOW}📊 Testing server functionality...${NC}"

# Start the server in HTTP mode for testing
echo -e "${YELLOW}🌐 Starting MCP server in HTTP mode on :8080...${NC}"
./postgres-mcp-server -http=:8080 &
SERVER_PID=$!

# Wait for server to start
sleep 3

echo -e "${GREEN}✅ Server started! PID: $SERVER_PID${NC}"

echo -e "${YELLOW}📋 Available database tables:${NC}"
docker-compose exec postgres psql -U testuser -d testdb -c "\dt"

echo -e "${YELLOW}👥 Sample users in the database:${NC}"
docker-compose exec postgres psql -U testuser -d testdb -c "SELECT * FROM users;"

echo -e "${YELLOW}🛍️ Sample products in the database:${NC}"
docker-compose exec postgres psql -U testuser -d testdb -c "SELECT * FROM products;"

echo -e "${YELLOW}📦 Sample orders in the database:${NC}"
docker-compose exec postgres psql -U testuser -d testdb -c "SELECT * FROM orders;"

echo ""
echo -e "${GREEN}🎉 Demo setup complete!${NC}"
echo ""
echo "The PostgreSQL MCP server is now running and ready to use."
echo ""
echo "You can:"
echo "  • Test the HTTP endpoint at http://localhost:8080"
echo "  • Connect your MCP client to the server"
echo "  • Query the database using the 'query' tool"
echo "  • Browse table schemas as MCP resources"
echo ""
echo "Example query to try:"
echo '  {"tool": "query", "arguments": {"sql": "SELECT name, email FROM users LIMIT 3"}}'
echo ""
echo -e "${YELLOW}Press any key to stop the demo...${NC}"
read -n 1 -s

# Clean up
echo -e "${YELLOW}🧹 Cleaning up...${NC}"
kill $SERVER_PID 2>/dev/null || true
docker-compose down -v
rm -f postgres-mcp-server

echo -e "${GREEN}✅ Demo cleanup complete!${NC}"
