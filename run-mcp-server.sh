#!/bin/bash
set -euo pipefail

# Set environment variables
export HOME="/Users/<username>"
export GOPATH="/Users/<username>/go"
export GOMODCACHE="/Users/<username>/go/pkg/mod"
export GOCACHE="/Users/<username>/Library/Caches/go-build"

# Use the stdio transport when the server is spawned by a desktop MCP client.
export MCP_TRANSPORT="stdio"

# Change to the project directory
cd /Users/<username>/code/loki-mcp

# Run the server
exec go run ./cmd/server
