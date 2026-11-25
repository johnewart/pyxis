// Command llmd runs llmd standalone.
//
// This binary is a THIN CALLER of llmdhost and nothing else: it reads the
// environment, binds listeners, prints operator-facing banners, and hands the
// rest to llmdhost.New. Every piece of runtime assembly — databases,
// migrations, repositories, the session manager, providers, the tool surface,
// the gRPC services — lives in llmdhost so that any other process can stand up
// the same runtime.
//
// Keeping main.go thin is the point, not a nicety. If llmd's own binary wired
// the runtime by hand, llmdhost would be a SECOND composition root that drifts
// from this one, and an embedder following it would hit exactly the gaps that
// once made a host reach into llmd/internal. Anything this file needs and
// llmdhost cannot provide is a missing method on Host.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/johnewart/pyxis/cmd/llmd/debugserver"
	"github.com/johnewart/pyxis/internal/tracing"
	"github.com/johnewart/pyxis/llmdhost"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/policyhook"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const logModuleMain = "llmd-main"

// isLoopbackAddr reports whether addr binds only the loopback interface. A bare
// port (":8080") or any non-loopback host is rejected so the debug/pprof
// surface can never be exposed off-box.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "mcp-stdio-bridge" {
		if err := llmdhost.RunMCPStdioBridge(os.Args[2:]); err != nil {
			llmdlog.Fatalf("mcp-stdio-bridge", "%v", err)
		}
		return
	}

	startTime := time.Now()

	cfg, err := configFromEnv()
	if err != nil {
		llmdlog.Fatalf(logModuleMain, "config: %v", err)
	}
	authEnabled, err := applyAuthFromEnv(&cfg)
	if err != nil {
		llmdlog.Fatalf(logModuleMain, "auth config: %v", err)
	}

	host, err := llmdhost.New(cfg)
	if err != nil {
		llmdlog.Fatalf(logModuleMain, "%v", err)
	}
	defer host.Close()

	// Explicit, because it writes process-wide state: the agent-PID registry
	// the MCP stdio bridge looks up, and the Cursor workspace bridge. A
	// single-runtime binary like this one wants that; an embedder hosting
	// several llmd runtimes does not, which is why New does not do it.
	host.InstallProcessDefaults()

	logStartupConfig(host)

	addr := os.Getenv("LLMD_GRPC_ADDR")
	if addr == "" {
		// Default is loopback, not the wildcard. A search for LLMD_GRPC_ADDR
		// users found only the test harness and the desktop launcher (both
		// set it explicitly) and the client, whose default is
		// 127.0.0.1:50051, so nothing relies on the wildcard. Loopback keeps
		// a default start working with auth off; exposing llmd needs an
		// explicit address plus auth.
		addr = "127.0.0.1:50051"
	}
	if err := validateListenAuth(addr, authEnabled); err != nil {
		llmdlog.Fatalf(logModuleMain, "%v", err)
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		llmdlog.Fatalf(logModuleMain, "listen %s: %v", addr, err)
	}

	mcpHTTPListener := startMCPHTTP(host)
	startDebugHTTP(host, startTime)
	metricsListener := startMetricsHTTP(host)
	connectListener := startBrowserSurface(host)

	if strings.TrimSpace(os.Getenv("LLMD_EMIT_MANAGED_LISTEN_JSON")) == "1" {
		emitManagedListenJSON(lis, mcpHTTPListener, connectListener, metricsListener)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo,
		"llmd listening on %s; LLM providers load from the database (global); chat uses EnsureSession + session_id", lis.Addr())
	if err := host.Serve(ctx, lis); err != nil && err != context.Canceled {
		llmdlog.Fatalf(logModuleMain, "grpc serve: %v", err)
	}
}

// configFromEnv is the ONLY place this process reads its configuration.
// llmdhost takes values, never environment variables, so every LLMD_* name is
// visible here in one list instead of scattered through the wiring.
//
// LLMD_DEFAULT_POLICY_MODE=open|closed (default closed) says what happens
// when a configured policy hook errors or times out. Any other value is an
// error, which makes startup fail.
//
// LLMD_POLICY_ENDPOINT=host:port selects a remote PolicyHookService. It is
// dialed over TLS (system roots) unless LLMD_POLICY_INSECURE=1 opts in to
// plaintext. LLMD_POLICY_TIMEOUT_MS bounds each hook call (default 5000).
// Hook metrics (llmd_policy_hook_*) are served at /metrics on the debug HTTP
// listener (LLMD_DEBUG_HTTP_ADDR).
func configFromEnv() (llmdhost.Config, error) {
	failMode, err := policyhook.FailModeFromEnv(os.Getenv)
	if err != nil {
		return llmdhost.Config{}, err
	}
	cfg := llmdhost.Config{
		PolicyFailMode: failMode,
		PolicyEndpoint: strings.TrimSpace(os.Getenv("LLMD_POLICY_ENDPOINT")),
		PolicyTimeout:  time.Duration(envPositiveInt("LLMD_POLICY_TIMEOUT_MS")) * time.Millisecond,
		PolicyInsecure: envTruthy("LLMD_POLICY_INSECURE"),
		DatabaseDSN:    strings.TrimSpace(os.Getenv("LLMD_DATABASE_URL")),
		// LLMD_KNOWLEDGE_DIR is the lexical knowledge index directory;
		// LLMD_KNOWLEDGE_SQLITE_PATH names the retired SQLite file, which is
		// deleted on start and seeds the directory's default.
		KnowledgeDir:        strings.TrimSpace(os.Getenv("LLMD_KNOWLEDGE_DIR")),
		KnowledgeSQLitePath: strings.TrimSpace(os.Getenv("LLMD_KNOWLEDGE_SQLITE_PATH")),
		MCPProxyEnabled:     mcpProxyEnabled(),
		MCPBearerToken:      strings.TrimSpace(os.Getenv("LLMD_MCP_BEARER_TOKEN")),
		RuntimeIdleEvict:    runtimeIdleEvictFromEnv(),
		ToolProviderSecret:  strings.TrimSpace(os.Getenv("LLMD_TOOL_PROVIDER_SHARED_SECRET")),
		// LLMD_DISABLE_SERVER_QUEUE_DRAIN=1 leaves queues to client drainers
		// (e2e suites that drive the queue by hand set it).
		// LLMD_RETRY_MAX_ATTEMPTS: total attempts per API-provider call.
		// LLMD_RETRY_MIN_REMAINING_MS: least time left before the deadline
		// for another attempt to start.
		RetryMaxAttempts:  envPositiveInt("LLMD_RETRY_MAX_ATTEMPTS"),
		RetryMinRemaining: time.Duration(envPositiveInt("LLMD_RETRY_MIN_REMAINING_MS")) * time.Millisecond,

		DisableServerQueueDrain: envTruthy("LLMD_DISABLE_SERVER_QUEUE_DRAIN"),

		// Standard OTEL_* variables (OTEL_EXPORTER_OTLP_ENDPOINT, _PROTOCOL,
		// _HEADERS, OTEL_SERVICE_NAME, OTEL_SDK_DISABLED). No endpoint means
		// tracing is off.
		Tracing: tracing.FromEnv(),
		// No Tools: llmd's own binary links no tool subsystems, so it serves the
		// core session tools only. A host that wants read_code/traverse_graph/
		// search passes its own registrars here.
		//
		// No CursorWorkspace either: installing one requires linking a
		// workspace implementation, which this binary deliberately does not
		// depend on. cursorws keeps its env-var-only no-op default.
	}
	return cfg, nil
}

// envPositiveInt reads a positive integer from the environment; unset,
// malformed or non-positive values yield 0 (use the default), with a warning
// for malformed ones.
func envPositiveInt(name string) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		llmdlog.Stdout(logModuleMain, llmdlog.LevelWarn, "ignoring %s=%q: want a positive integer", name, v)
		return 0
	}
	return n
}

// runtimeIdleEvictFromEnv reads LLMD_RUNTIME_IDLE_EVICT_MINUTES. 0 disables
// eviction; unset uses llmdhost's default.
func runtimeIdleEvictFromEnv() time.Duration {
	v := strings.TrimSpace(os.Getenv("LLMD_RUNTIME_IDLE_EVICT_MINUTES"))
	if v == "" {
		return 0 // llmdhost applies DefaultRuntimeIdleEvict
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	if n == 0 {
		return -1 // explicit off
	}
	return time.Duration(n) * time.Minute
}

// logStartupConfig prints where llmd's state lives and which debug switches are
// on, so an operator who set a flag can confirm llmd saw it.
func logStartupConfig(host *llmdhost.Host) {
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo,
		"llmd: LLM providers load from the database only (RegisterLlmProvider, scoped per tenant/scope via llmd-tenant-id / llmd-scope-id); no providers file")

	if strings.TrimSpace(os.Getenv("LLMD_DATABASE_URL")) == "" {
		llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo,
			"llmd: using default sqlite file %q (set LLMD_DATABASE_URL to override)", host.DatabaseDSN())
	} else {
		llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "llmd: using database %q", host.DatabaseDSN())
	}
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "llmd: knowledge service enabled (db=%q)", host.KnowledgeDSN())

	cwd, err := os.Getwd()
	if err != nil {
		cwd = fmt.Sprintf("<getwd: %v>", err)
	}
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo,
		"llmd: startup data paths cwd=%q agent_db=%q knowledge_db=%q embedding_cache=%q",
		cwd, host.DatabaseDSN(), host.KnowledgeDSN(), strings.TrimSpace(os.Getenv("LLMD_EMBEDDING_CACHE_DIR")))

	debugSettings := llmdlog.ResolveDebugSettings()
	if debugSettings.Any() {
		llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "llmd: debug enabled: %s", debugSettings.Summary())
	}
	if debugSettings.ChatFile != "" {
		llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo,
			"llmd: chat debug records are being written as NDJSON to %s", debugSettings.ChatFile)
	}
}

// startMCPHTTP serves llmd's MCP endpoint when LLMD_MCP_HTTP_ADDR is set.
func startMCPHTTP(host *llmdhost.Host) net.Listener {
	mcpAddr := strings.TrimSpace(os.Getenv("LLMD_MCP_HTTP_ADDR"))
	if mcpAddr == "" {
		return nil
	}
	lis, err := net.Listen("tcp", mcpAddr)
	if err != nil {
		llmdlog.Fatalf(logModuleMain, "LLMD_MCP_HTTP_ADDR listen %s: %v", mcpAddr, err)
	}
	// A managing host passes 127.0.0.1:0 for an ephemeral port; only the
	// listener knows the real one. Publish it so Cursor CLI shared-workspace
	// resolution at request time matches what we log, not the pre-bind :0 spec.
	// Kept for out-of-process readers of the env (a host-installed cursorws
	// adapter); in-process providers find the listener via mcpbridge.
	if err := os.Setenv("LLMD_MCP_HTTP_ADDR", lis.Addr().String()); err != nil {
		llmdlog.Fatalf(logModuleMain, "set LLMD_MCP_HTTP_ADDR after MCP listen: %v", err)
	}
	// Same path an embedding host uses; Host.Close (deferred in main) stops it.
	if err := host.StartMCP(lis); err != nil {
		llmdlog.Fatalf(llmdhost.MCPLogModule, "LLMD_MCP_HTTP_ADDR: %v", err)
	}
	llmdlog.Stdout(llmdhost.MCPLogModule, llmdlog.LevelInfo,
		"MCP scope headers: X-Llmd-Agent-Id and/or X-Llmd-Session-Id + X-Llmd-Tenant-Id (optional X-Llmd-Scope-Id)")
	return lis
}

// startDebugHTTP binds the loopback-only statusz/pprof surface, either
// explicitly via LLMD_DEBUG_HTTP_ADDR or implicitly from LLMD_DEBUG=1.
func startDebugHTTP(host *llmdhost.Host, startTime time.Time) {
	dbgAddr := strings.TrimSpace(os.Getenv("LLMD_DEBUG_HTTP_ADDR"))
	autoEnabled := false
	if dbgAddr == "" && llmdhost.DebugEnabled() {
		dbgAddr = "127.0.0.1:0"
		autoEnabled = true
	}
	if dbgAddr == "" {
		llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo,
			"llmd: debug HTTP disabled (set LLMD_DEBUG=1 for an auto ephemeral port, or LLMD_DEBUG_HTTP_ADDR=127.0.0.1:PORT for a fixed one; serves /debug/statusz + pprof)")
		return
	}
	if !isLoopbackAddr(dbgAddr) {
		llmdlog.Fatalf(logModuleMain,
			"LLMD_DEBUG_HTTP_ADDR=%q must bind loopback only (127.0.0.1, ::1, or localhost); refusing to expose pprof off-box", dbgAddr)
	}
	lis, err := net.Listen("tcp", dbgAddr)
	if err != nil {
		llmdlog.Fatalf(logModuleMain, "LLMD_DEBUG_HTTP_ADDR listen %s: %v", dbgAddr, err)
	}

	agentSQL, knowledgeSQL := host.SQLDatabases()
	handler := debugserver.Handler(debugserver.Config{
		StartTime: startTime,
		Databases: []debugserver.DBEntry{
			{Label: "agent", Path: host.DatabaseDSN(), DB: agentSQL},
			{Label: "knowledge", Path: host.KnowledgeDSN(), DB: knowledgeSQL},
		},
		Runtimes:          host,
		Metrics:           promhttp.HandlerFor(host.MetricsRegistry(), promhttp.HandlerOpts{}),
		EmbeddingCacheDir: strings.TrimSpace(os.Getenv("LLMD_EMBEDDING_CACHE_DIR")),
	})

	// Loud, easy-to-grep banner: with several llmd copies on a dev box, the pid
	// plus ephemeral port make it obvious which one this is.
	base := fmt.Sprintf("http://%s", lis.Addr().String())
	trigger := "LLMD_DEBUG_HTTP_ADDR"
	if autoEnabled {
		trigger = llmdlog.EnvDebug + "=1"
	}
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "")
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "========================================================================")
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "  llmd DEBUG HTTP (%s)  pid=%d", trigger, os.Getpid())
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "    statusz : %s/debug/statusz", base)
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "    pprof   : %s/debug/pprof/", base)
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "    (loopback only)")
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "========================================================================")
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo, "")
	go func() {
		if err := http.Serve(lis, handler); err != nil {
			llmdlog.Fatalf(logModuleMain, "LLMD_DEBUG_HTTP_ADDR: %v", err)
		}
	}()
}

// startBrowserSurface binds the grpc-web/Connect + session-events WebSocket
// surface for the renderer. Enabled only when the host supplies a per-spawn
// shared secret; loopback only.
func startBrowserSurface(host *llmdhost.Host) net.Listener {
	connectToken := strings.TrimSpace(os.Getenv("LLMD_CONNECT_TOKEN"))
	if connectToken == "" {
		// A handler that was never registered is indistinguishable, from the
		// browser, from a broken dev-server proxy: the renderer sees "closed
		// before handshake" and llmd logs NOTHING, because every line that
		// would explain it lives in code that never ran. Announce the absence.
		llmdlog.Stdout(logModuleMain, llmdlog.LevelWarn,
			"llmd: Connect/grpc-web + /ws/session-events surface DISABLED — LLMD_CONNECT_TOKEN is unset or empty. "+
				"Nothing is listening for the renderer's session-events WebSocket, so a proxy forwarding to it will "+
				"report \"closed before handshake\" with no further explanation. Set LLMD_CONNECT_TOKEN (and "+
				"LLMD_CONNECT_ALLOWED_ORIGINS to the exact browser page origin, scheme included) when spawning llmd.")
		return nil
	}

	connectAddr := strings.TrimSpace(os.Getenv("LLMD_CONNECT_HTTP_ADDR"))
	if connectAddr == "" {
		connectAddr = "127.0.0.1:0"
	}
	if !isLoopbackAddr(connectAddr) {
		llmdlog.Fatalf(logModuleMain,
			"LLMD_CONNECT_HTTP_ADDR=%q must bind loopback only; refusing to expose the renderer surface off-box", connectAddr)
	}
	lis, err := net.Listen("tcp", connectAddr)
	if err != nil {
		// Non-fatal: the renderer surface is additive. Losing it must not take
		// llmd down — the renderer keeps its existing data path.
		llmdlog.Stdout(logModuleMain, llmdlog.LevelWarn,
			"llmd: Connect/grpc-web listener disabled — listen %s failed: %v", connectAddr, err)
		return nil
	}

	var origins []string
	for _, o := range strings.Split(os.Getenv("LLMD_CONNECT_ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	if len(origins) == 0 {
		llmdlog.Stdout(logModuleMain, llmdlog.LevelWarn,
			"llmd: LLMD_CONNECT_ALLOWED_ORIGINS is empty — every cross-origin request to this listener will be "+
				"rejected: CORS preflights on the Connect surface, and /ws/session-events handshakes whose Origin "+
				"differs from the request Host (exactly what a dev-server proxy produces). Set it to the browser "+
				"page origin(s), scheme included, comma-separated (e.g. http://127.0.0.1:5174).")
	}

	mux, err := host.BrowserSurface(llmdhost.BrowserSurfaceConfig{
		Token:          connectToken,
		AllowedOrigins: origins,
	})
	if err != nil {
		llmdlog.Fatalf(logModuleMain, "browser surface: %v", err)
	}

	// Route inventory. What this listener serves was previously invisible: a
	// service that never registered, or a stale binary missing a method the
	// renderer calls, is indistinguishable at runtime from a transport problem.
	routes := host.ServiceRoutes()
	methodCount := 0
	for _, r := range routes {
		methodCount += len(r.Methods)
	}
	llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo,
		"llmd Connect/grpc-web routes on %s: GET %s (WebSocket session events; matched by path SUFFIX, so a "+
			"dev-server proxy prefix like /host-connect/ws/session-events also routes here) + %d grpc-web "+
			"method(s) across %d service(s), POSTed as /<service>/<method>",
		lis.Addr().String(), llmdhost.SessionEventsWSPath, methodCount, len(routes))
	for _, r := range routes {
		llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo,
			"llmd Connect/grpc-web route: /%s/{%s}", r.Service, strings.Join(r.Methods, ","))
	}

	go func() {
		llmdlog.Stdout(logModuleMain, llmdlog.LevelInfo,
			"llmd Connect/grpc-web listening on %s (token-guarded, loopback only; %d allowed origin(s)); WebSocket session events at /ws/session-events",
			lis.Addr().String(), len(origins))
		if err := http.Serve(lis, mux); err != nil {
			llmdlog.Stdout(logModuleMain, llmdlog.LevelWarn, "llmd: Connect/grpc-web serve ended: %v", err)
		}
	}()
	return lis
}

// emitManagedListenJSON reports the bound addresses to a managing parent
// process, which needs them when it asked for ephemeral ports.
func emitManagedListenJSON(grpcLis, mcpLis, connectLis, metricsLis net.Listener) {
	export := map[string]string{"grpc": grpcLis.Addr().String()}
	if mcpLis != nil {
		export["mcp"] = mcpLis.Addr().String()
	}
	if connectLis != nil {
		export["connect"] = connectLis.Addr().String()
	}
	if metricsLis != nil {
		export["metrics"] = metricsLis.Addr().String()
	}
	raw, err := json.Marshal(export)
	if err != nil {
		llmdlog.Fatalf(logModuleMain, "marshal managed listen export: %v", err)
	}
	if _, err := fmt.Fprintf(os.Stdout, "LLMD_MANAGED_LISTEN_JSON=%s\n", string(raw)); err != nil {
		llmdlog.Fatalf(logModuleMain, "write managed listen export: %v", err)
	}
}

// mcpProxyEnabled is the upstream-MCP feature gate. It defaults to on (the
// surface it replaces was always on), and an explicit off unregisters the
// service entirely rather than pruning it downstream.
func mcpProxyEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LLMD_MCP_PROXY_ENABLED"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// envTruthy reports whether the named variable is set to an explicit "on"
// value (1/true/yes/on). Unset or anything else is false.
func envTruthy(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
