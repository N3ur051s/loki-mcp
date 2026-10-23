#!/usr/bin/env bash
#
# Minimal Streamable HTTP client for the Loki MCP server.
#
# Requirements: curl, jq
#
# Usage:
#   ./examples/streamable-http.sh [endpoint] [logql-query] [format]
#
# Example:
#   ./examples/streamable-http.sh http://localhost:8080/mcp '{job="varlogs"}' raw

set -euo pipefail

ENDPOINT="${1:-http://localhost:8080/mcp}"
QUERY="${2:-}"
FORMAT="${3:-raw}"

if [[ -z "$QUERY" ]]; then
  QUERY='{job="varlogs"}'
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required: https://jqlang.github.io/jq/" >&2
  exit 1
fi

SESSION_ID=""
RPC_RESPONSE=""

# Extract JSON from either a plain JSON response or an SSE-formatted streaming response.
extract_json() {
  local body
  body="$(cat)"
  if printf '%s' "$body" | jq -e . >/dev/null 2>&1; then
    printf '%s' "$body"
    return
  fi
  printf '%s\n' "$body" | sed -n 's/^data: //p' | tail -n 1
}

# rpc sends a JSON-RPC message and stores the response in RPC_RESPONSE.
# It also captures Mcp-Session-Id when the server uses stateful sessions.
rpc() {
  local payload="$1"
  local header_file response header_session
  header_file="$(mktemp "${TMPDIR:-/tmp}/loki-mcp-headers.XXXXXX")"

  response="$(curl -sS -D "$header_file" \
    -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" \
    -H "MCP-Protocol-Version: 2025-03-26" \
    ${SESSION_ID:+-H "Mcp-Session-Id: $SESSION_ID"} \
    -X POST "$ENDPOINT" \
    --data "$payload")"

  header_session="$(grep -i '^Mcp-Session-Id:' "$header_file" | tr -d '\r' | awk '{print $2}' | tail -n 1 || true)"
  if [[ -n "$header_session" ]]; then
    SESSION_ID="$header_session"
  fi
  rm -f "$header_file"

  RPC_RESPONSE="$response"
}

echo "==> initialize"
rpc '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"curl","version":"1.0.0"}}}'
printf '%s\n' "$RPC_RESPONSE" | extract_json | jq .
echo "session: ${SESSION_ID:-<stateless>}"

echo "==> notifications/initialized"
rpc '{"jsonrpc":"2.0","method":"notifications/initialized"}'

echo "==> tools/list"
rpc '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
printf '%s\n' "$RPC_RESPONSE" | extract_json | jq '.result.tools[] | {name, description}'

echo "==> tools/call loki_query"
CALL_PAYLOAD="$(jq -n --arg query "$QUERY" --arg format "$FORMAT" '{
  jsonrpc: "2.0",
  id: 3,
  method: "tools/call",
  params: {
    name: "loki_query",
    arguments: {
      query: $query,
      start: "-1h",
      end: "now",
      limit: 20,
      format: $format
    }
  }
}')"
rpc "$CALL_PAYLOAD"
printf '%s\n' "$RPC_RESPONSE" | extract_json | jq -r '.result.content[]? | select(.type == "text") | .text'

if [[ -n "$SESSION_ID" ]]; then
  echo "==> DELETE session"
  curl -sS -o /dev/null -X DELETE \
    -H "Mcp-Session-Id: $SESSION_ID" \
    "$ENDPOINT" || true
fi
