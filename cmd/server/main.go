package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/N3ur051s/loki-mcp/internal/handlers"
)

// version is overridden at build time with:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/server
var version = "0.2.0"

const (
	serviceName = "loki-mcp-server"

	defaultTransport       = "stdio"
	defaultPort            = "8080"
	defaultStreamablePath  = "/mcp"
	defaultSessionMode     = "stateless"
	defaultSessionIdleTTL  = 30 * time.Minute
	defaultHeartbeat       = 30 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

// config contains the runtime configuration of the server.
type config struct {
	transport                     string
	addr                          string
	streamablePath                string
	sessionMode                   string
	sessionIdleTTL                time.Duration
	heartbeatInterval             time.Duration
	corsOrigins                   []string
	corsAllowCredentials          bool
	corsMaxAge                    int
	disableDNSRebindingProtection bool
	tlsCertFile                   string
	tlsKeyFile                    string
	shutdownTimeout               time.Duration
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-version", "--version", "version":
			fmt.Println(version)
			return
		case "-help", "--help", "help":
			printUsage()
			return
		}
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLogLevel(os.Getenv("LOG_LEVEL")),
	}))
	slog.SetDefault(logger)

	cfg, err := loadConfig()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	mcpServer := newMCPServer()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting Loki MCP server",
		"version", version,
		"transport", cfg.transport,
		"loki_url", envOrDefault(handlers.EnvLokiURL, handlers.DefaultLokiURL),
	)

	switch cfg.transport {
	case "stdio":
		err = server.ServeStdio(mcpServer)
	case "http":
		err = serveHTTP(ctx, mcpServer, cfg)
	case "both":
		err = serveBoth(ctx, mcpServer, cfg)
	}

	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server stopped with an error", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
}

// loadConfig reads the configuration from the environment.
func loadConfig() (config, error) {
	transport := strings.ToLower(strings.TrimSpace(envOrDefault("MCP_TRANSPORT", defaultTransport)))
	switch transport {
	case "stdio", "http", "both":
	case "streamable-http", "streamable":
		transport = "http"
	default:
		return config{}, fmt.Errorf("unsupported MCP_TRANSPORT %q (expected stdio, http, or both)", transport)
	}

	sessionMode := strings.ToLower(strings.TrimSpace(envOrDefault("MCP_SESSION_MODE", defaultSessionMode)))
	if sessionMode != "stateless" && sessionMode != "stateful" {
		return config{}, fmt.Errorf("unsupported MCP_SESSION_MODE %q (expected stateless or stateful)", sessionMode)
	}

	sessionIdleTTL, err := parseDurationEnv("MCP_SESSION_IDLE_TTL", defaultSessionIdleTTL)
	if err != nil {
		return config{}, err
	}
	heartbeat, err := parseDurationEnv("MCP_HEARTBEAT_INTERVAL", defaultHeartbeat)
	if err != nil {
		return config{}, err
	}
	shutdownTimeout, err := parseDurationEnv("MCP_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return config{}, err
	}

	corsMaxAge, err := parseIntEnv("MCP_CORS_MAX_AGE", 600)
	if err != nil {
		return config{}, err
	}

	cfg := config{
		transport:                     transport,
		addr:                          httpAddr(),
		streamablePath:                normalizePath(envOrDefault("MCP_STREAMABLE_PATH", defaultStreamablePath)),
		sessionMode:                   sessionMode,
		sessionIdleTTL:                sessionIdleTTL,
		heartbeatInterval:             heartbeat,
		corsOrigins:                   parseOrigins(envOrDefault("MCP_CORS_ORIGINS", "*")),
		corsAllowCredentials:          parseBoolEnv("MCP_CORS_ALLOW_CREDENTIALS", false),
		corsMaxAge:                    corsMaxAge,
		disableDNSRebindingProtection: parseBoolEnv("MCP_DISABLE_DNS_REBINDING_PROTECTION", false),
		tlsCertFile:                   strings.TrimSpace(os.Getenv("MCP_TLS_CERT")),
		tlsKeyFile:                    strings.TrimSpace(os.Getenv("MCP_TLS_KEY")),
		shutdownTimeout:               shutdownTimeout,
	}

	if (cfg.tlsCertFile == "") != (cfg.tlsKeyFile == "") {
		return config{}, fmt.Errorf("MCP_TLS_CERT and MCP_TLS_KEY must be set together")
	}

	reservedPaths := map[string]bool{"/": true, "/health": true, "/healthz": true}
	if reservedPaths[cfg.streamablePath] {
		return config{}, fmt.Errorf("MCP_STREAMABLE_PATH %q conflicts with a built-in endpoint", cfg.streamablePath)
	}

	return cfg, nil
}

// newMCPServer creates the MCP server and registers all Loki tools.
func newMCPServer() *server.MCPServer {
	s := server.NewMCPServer(
		"Loki MCP Server",
		version,
		server.WithRecovery(),
		server.WithLogging(),
		server.WithInstructions(
			"Use loki_query to run LogQL queries against Grafana Loki, "+
				"loki_label_names to discover label names, and loki_label_values to discover "+
				"the values of a label. Connection defaults can be configured with the "+
				"LOKI_URL, LOKI_ORG_ID, LOKI_USERNAME, LOKI_PASSWORD and LOKI_TOKEN environment variables.",
		),
	)

	s.AddTool(handlers.NewLokiQueryTool(), handlers.HandleLokiQuery)
	s.AddTool(handlers.NewLokiLabelNamesTool(), handlers.HandleLokiLabelNames)
	s.AddTool(handlers.NewLokiLabelValuesTool(), handlers.HandleLokiLabelValues)

	return s
}

// httpApp bundles the HTTP handler with the transports that need to be closed
// during a graceful shutdown.
type httpApp struct {
	handler    http.Handler
	streamable *server.StreamableHTTPServer
}

// newHTTPApp builds the HTTP router with the Streamable HTTP transport plus
// health and discovery endpoints.
func newHTTPApp(mcpServer *server.MCPServer, cfg config) *httpApp {
	corsOptions := []server.CORSOption{
		server.WithCORSAllowedMethods(http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodOptions),
		server.WithCORSAllowedHeaders(
			"Content-Type",
			"Authorization",
			"Mcp-Session-Id",
			"Mcp-Protocol-Version",
			"Mcp-Method",
			"Mcp-Name",
			"Last-Event-ID",
		),
		server.WithCORSExposedHeaders("Mcp-Session-Id", "Mcp-Protocol-Version"),
		server.WithCORSMaxAge(cfg.corsMaxAge),
	}
	if len(cfg.corsOrigins) > 0 {
		corsOptions = append(corsOptions, server.WithCORSAllowedOrigins(cfg.corsOrigins...))
	}
	if cfg.corsAllowCredentials {
		corsOptions = append(corsOptions, server.WithCORSAllowCredentials())
	}

	streamableOptions := []server.StreamableHTTPOption{
		server.WithEndpointPath(cfg.streamablePath),
		server.WithHeartbeatInterval(cfg.heartbeatInterval),
		server.WithSessionIdleTTL(cfg.sessionIdleTTL),
	}
	if cfg.sessionMode == "stateful" {
		streamableOptions = append(streamableOptions, server.WithStateful(true))
	} else {
		streamableOptions = append(streamableOptions, server.WithStateLess(true))
	}
	if len(cfg.corsOrigins) > 0 {
		streamableOptions = append(streamableOptions, server.WithStreamableHTTPCORS(corsOptions...))
	}
	if cfg.disableDNSRebindingProtection {
		streamableOptions = append(streamableOptions, server.WithDisableLocalhostProtection(true))
	}
	streamable := server.NewStreamableHTTPServer(mcpServer, streamableOptions...)

	mux := http.NewServeMux()
	mux.Handle(cfg.streamablePath, streamable)
	// /stream is kept as an alias for deployments that were created before the
	// endpoint was standardised on /mcp.
	if cfg.streamablePath != "/stream" {
		mux.Handle("/stream", streamable)
	}
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/", rootHandler(cfg))

	return &httpApp{
		handler:    mux,
		streamable: streamable,
	}
}

func (a *httpApp) shutdown(ctx context.Context) {
	if a.streamable != nil {
		if err := a.streamable.Shutdown(ctx); err != nil {
			slog.Warn("failed to shut down streamable HTTP transport", "error", err)
		}
	}
}

// serveHTTP runs the HTTP transports until the context is cancelled or the
// listener fails.
func serveHTTP(ctx context.Context, mcpServer *server.MCPServer, cfg config) error {
	app := newHTTPApp(mcpServer, cfg)
	httpServer := &http.Server{
		Addr:              cfg.addr,
		Handler:           app.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	errCh := make(chan error, 1)
	go func() {
		logEndpoints(cfg)
		errCh <- listenAndServe(httpServer, cfg)
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
			defer cancel()
			app.shutdown(shutdownCtx)
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
	defer cancel()

	app.shutdown(shutdownCtx)
	err := httpServer.Shutdown(shutdownCtx)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP shutdown failed: %w", err)
	}
	return nil
}

// serveBoth keeps the historical "stdio + HTTP" behaviour. A failure of one
// transport does not bring down the other, which makes it safe to use from
// desktop clients that spawn several server processes.
func serveBoth(ctx context.Context, mcpServer *server.MCPServer, cfg config) error {
	app := newHTTPApp(mcpServer, cfg)
	httpServer := &http.Server{
		Addr:              cfg.addr,
		Handler:           app.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	httpErrCh := make(chan error, 1)
	go func() {
		logEndpoints(cfg)
		httpErrCh <- listenAndServe(httpServer, cfg)
	}()

	stdioErrCh := make(chan error, 1)
	go func() {
		slog.Info("starting stdio transport")
		stdioErrCh <- server.ServeStdio(mcpServer)
	}()

	var httpErr, stdioErr error
	httpRunning, stdioRunning := true, true

	for httpRunning || stdioRunning {
		select {
		case <-ctx.Done():
			stdioRunning = false
			httpRunning = false
		case err := <-httpErrCh:
			httpRunning = false
			httpErr = err
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("HTTP transport stopped; continuing with stdio", "error", err)
			}
		case err := <-stdioErrCh:
			stdioRunning = false
			stdioErr = err
			if err != nil {
				slog.Error("stdio transport stopped; continuing with HTTP", "error", err)
			}
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
	defer cancel()
	app.shutdown(shutdownCtx)
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Warn("HTTP shutdown failed", "error", err)
	}

	if stdioErr != nil {
		return stdioErr
	}
	if httpErr != nil && !errors.Is(httpErr, http.ErrServerClosed) {
		return httpErr
	}
	return nil
}

func listenAndServe(httpServer *http.Server, cfg config) error {
	if cfg.tlsCertFile != "" {
		return httpServer.ListenAndServeTLS(cfg.tlsCertFile, cfg.tlsKeyFile)
	}
	return httpServer.ListenAndServe()
}

func logEndpoints(cfg config) {
	scheme := "http"
	if cfg.tlsCertFile != "" {
		scheme = "https"
	}
	slog.Info("HTTP server listening",
		"addr", cfg.addr,
		"scheme", scheme,
		"streamable_http", cfg.streamablePath,
		"streamable_http_alias", "/stream",
		"health", "/healthz",
		"session_mode", cfg.sessionMode,
	)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": serviceName,
		"version": version,
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

func rootHandler(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"name":     serviceName,
			"version":  version,
			"protocol": "Model Context Protocol (MCP)",
			"transports": map[string]string{
				"streamable-http":       cfg.streamablePath,
				"streamable-http-alias": "/stream",
			},
			"health": "/healthz",
			"tools":  []string{"loki_query", "loki_label_names", "loki_label_values"},
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func httpAddr() string {
	if addr := strings.TrimSpace(os.Getenv("MCP_HTTP_ADDR")); addr != "" {
		return addr
	}
	host := strings.TrimSpace(os.Getenv("MCP_HOST"))
	port := strings.TrimSpace(envOrDefault("PORT", defaultPort))
	return net.JoinHostPort(host, port)
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}

// parseOrigins parses a comma-separated CORS origin list. "none" disables CORS.
func parseOrigins(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "none") {
		return nil
	}
	origins := make([]string, 0, 4)
	for _, origin := range strings.Split(value, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parseDurationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", key, value, err)
	}
	if duration < 0 {
		return 0, fmt.Errorf("invalid %s %q: must not be negative", key, value)
	}
	return duration, nil
}

func parseIntEnv(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", key, value, err)
	}
	if number < 0 {
		return 0, fmt.Errorf("invalid %s %q: must not be negative", key, value)
	}
	return number, nil
}

func parseBoolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		slog.Warn("invalid boolean environment variable, using default", "key", key, "value", value, "default", fallback)
		return fallback
	}
	return parsed
}

func parseLogLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func printUsage() {
	fmt.Printf(`%s %s

Usage:
  %s [version|help]

The server is configured through environment variables.

Transport:
  MCP_TRANSPORT              stdio, http, or both (default: stdio)
  MCP_HTTP_ADDR              Listen address, e.g. 0.0.0.0:8080
  MCP_HOST                   Listen host when MCP_HTTP_ADDR is unset
  PORT                       Listen port when MCP_HTTP_ADDR is unset (default: 8080)

HTTP endpoints:
  MCP_STREAMABLE_PATH        Streamable HTTP endpoint (default: /mcp)

Sessions:
  MCP_SESSION_MODE           stateless or stateful (default: stateless)
  MCP_SESSION_IDLE_TTL       Idle session TTL (default: 30m)
  MCP_HEARTBEAT_INTERVAL     Streamable HTTP heartbeat interval (default: 30s)

HTTP behaviour:
  MCP_CORS_ORIGINS           Comma-separated origins, * for any, none to disable (default: *)
  MCP_CORS_ALLOW_CREDENTIALS Allow credentialed CORS requests (default: false)
  MCP_CORS_MAX_AGE           CORS preflight cache in seconds (default: 600)
  MCP_DISABLE_DNS_REBINDING_PROTECTION
                             Disable localhost DNS-rebinding protection (default: false)
  MCP_TLS_CERT               Path to a TLS certificate (requires MCP_TLS_KEY)
  MCP_TLS_KEY                Path to the TLS private key (requires MCP_TLS_CERT)
  MCP_SHUTDOWN_TIMEOUT       Graceful shutdown timeout (default: 10s)
  LOG_LEVEL                  debug, info, warn, or error (default: info)

Loki:
  LOKI_URL                   Default Loki URL (default: %s)
  LOKI_ORG_ID                Default tenant / X-Scope-OrgID
  LOKI_USERNAME              Default basic-auth username
  LOKI_PASSWORD              Default basic-auth password
  LOKI_TOKEN                 Default bearer token
  LOKI_TIMEOUT               Request timeout for Loki (default: 30s)
`, serviceName, version, serviceName, handlers.DefaultLokiURL)
}
