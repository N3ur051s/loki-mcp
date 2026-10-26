package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func testConfig() config {
	return config{
		transport:         "http",
		addr:              ":0",
		streamablePath:    defaultStreamablePath,
		sessionMode:       "stateless",
		sessionIdleTTL:    30 * time.Minute,
		heartbeatInterval: 30 * time.Second,
		corsOrigins:       []string{"*"},
		corsMaxAge:        600,
		shutdownTimeout:   5 * time.Second,
	}
}

func newTestServer(t *testing.T) (*httptest.Server, *httpApp) {
	t.Helper()

	app := newHTTPApp(newMCPServer(), testConfig())
	server := httptest.NewServer(app.handler)
	t.Cleanup(func() {
		server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		app.shutdown(ctx)
	})
	return server, app
}

func TestHealthEndpoint(t *testing.T) {
	server, _ := newTestServer(t)

	resp, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode health response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %v", body["status"])
	}
	if body["version"] != version {
		t.Errorf("expected version %q, got %v", version, body["version"])
	}
}

func TestRootEndpoint(t *testing.T) {
	server, _ := newTestServer(t)

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("root request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode root response: %v", err)
	}
	if body["name"] != serviceName {
		t.Errorf("expected service name %q, got %v", serviceName, body["name"])
	}
}

func TestCORSHeaders(t *testing.T) {
	server, _ := newTestServer(t)

	req, err := http.NewRequest(http.MethodOptions, server.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, Mcp-Session-Id")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("CORS preflight failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected Access-Control-Allow-Origin *, got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Mcp-Session-Id") {
		t.Errorf("expected Mcp-Session-Id to be allowed, got %q", got)
	}
}

func TestStreamableHTTPClient(t *testing.T) {
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query_range" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"status": "success",
			"data": {
				"resultType": "streams",
				"result": [{
					"stream": {"job": "test"},
					"values": [["1705312245000000000", "hello from loki"]]
				}]
			}
		}`)
	}))
	defer loki.Close()

	server, _ := newTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	mcpClient, err := client.NewStreamableHttpClient(server.URL + "/mcp")
	if err != nil {
		t.Fatalf("failed to create streamable HTTP client: %v", err)
	}
	defer func() { _ = mcpClient.Close() }()

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("failed to start client: %v", err)
	}

	initResult, err := mcpClient.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo:      mcp.Implementation{Name: "loki-mcp-test", Version: "1.0.0"},
		},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if initResult.ServerInfo.Name != "Loki MCP Server" {
		t.Errorf("unexpected server name %q", initResult.ServerInfo.Name)
	}

	tools, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("tools/list failed: %v", err)
	}
	if len(tools.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools.Tools))
	}

	result, err := mcpClient.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "loki_query",
			Arguments: map[string]any{
				"query":  `{job="test"}`,
				"url":    loki.URL,
				"format": "json",
			},
		},
	})
	if err != nil {
		t.Fatalf("tools/call failed: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected tool result content")
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result.Content[0])
	}
	if !strings.Contains(text.Text, "hello from loki") {
		t.Errorf("expected tool result to contain log line, got %q", text.Text)
	}
}

func TestStreamableHTTPAlias(t *testing.T) {
	server, _ := newTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	mcpClient, err := client.NewStreamableHttpClient(server.URL + "/stream")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer func() { _ = mcpClient.Close() }()

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	if _, err := mcpClient.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "loki-mcp-test", Version: "1.0.0"},
		},
	}); err != nil {
		t.Fatalf("initialize through /stream alias failed: %v", err)
	}
}

func TestStatefulSessionLifecycle(t *testing.T) {
	cfg := testConfig()
	cfg.sessionMode = "stateful"
	app := newHTTPApp(newMCPServer(), cfg)
	server := httptest.NewServer(app.handler)
	defer func() {
		server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		app.shutdown(ctx)
	}()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("expected a session ID in stateful mode")
	}

	listReq, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	listReq.Header.Set("Content-Type", "application/json")
	listReq.Header.Set("Accept", "application/json")
	listReq.Header.Set("Mcp-Session-Id", sessionID)

	listResp, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatalf("tools/list failed: %v", err)
	}
	defer func() { _ = listResp.Body.Close() }()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 for tools/list, got %d", listResp.StatusCode)
	}

	deleteReq, err := http.NewRequest(http.MethodDelete, server.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	deleteReq.Header.Set("Mcp-Session-Id", sessionID)
	deleteResp, err := http.DefaultClient.Do(deleteReq)
	if err != nil {
		t.Fatalf("DELETE session failed: %v", err)
	}
	defer func() { _ = deleteResp.Body.Close() }()
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 for DELETE, got %d", deleteResp.StatusCode)
	}
}

func TestUnknownPathReturnsNotFound(t *testing.T) {
	server, _ := newTestServer(t)

	resp, err := http.Get(server.URL + "/does-not-exist")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", resp.StatusCode)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "")
	t.Setenv("MCP_SESSION_MODE", "")
	t.Setenv("MCP_HTTP_ADDR", "")
	t.Setenv("MCP_HOST", "")
	t.Setenv("PORT", "")
	t.Setenv("MCP_STREAMABLE_PATH", "")
	t.Setenv("MCP_SESSION_IDLE_TTL", "")
	t.Setenv("MCP_HEARTBEAT_INTERVAL", "")
	t.Setenv("MCP_SHUTDOWN_TIMEOUT", "")
	t.Setenv("MCP_CORS_MAX_AGE", "")
	t.Setenv("MCP_CORS_ORIGINS", "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if cfg.transport != "stdio" {
		t.Errorf("expected stdio transport by default, got %q", cfg.transport)
	}
	if cfg.addr != ":8080" {
		t.Errorf("expected :8080 address by default, got %q", cfg.addr)
	}
	if cfg.streamablePath != "/mcp" {
		t.Errorf("expected /mcp streamable path, got %q", cfg.streamablePath)
	}
	if len(cfg.corsOrigins) != 1 || cfg.corsOrigins[0] != "*" {
		t.Errorf("expected wildcard CORS origin by default, got %v", cfg.corsOrigins)
	}
}

func TestLoadConfigRejectsReservedPaths(t *testing.T) {
	t.Setenv("MCP_STREAMABLE_PATH", "/health")

	if _, err := loadConfig(); err == nil {
		t.Fatal("expected a reserved path to be rejected")
	}
}

func TestLoadConfigRejectsIncompleteTLS(t *testing.T) {
	t.Setenv("MCP_TLS_CERT", "/tmp/cert.pem")
	t.Setenv("MCP_TLS_KEY", "")

	if _, err := loadConfig(); err == nil {
		t.Fatal("expected incomplete TLS configuration to be rejected")
	}
}

func TestParseOrigins(t *testing.T) {
	testCases := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"none", nil},
		{"*", []string{"*"}},
		{"https://a.example, https://b.example", []string{"https://a.example", "https://b.example"}},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			got := parseOrigins(tc.input)
			if fmt.Sprint(got) != fmt.Sprint(tc.expected) {
				t.Errorf("parseOrigins(%q) = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}
