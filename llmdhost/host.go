package llmdhost

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/johnewart/pyxis/agentpid"
	"github.com/johnewart/pyxis/cursorws"
	knowledgev1 "github.com/johnewart/pyxis/gen/knowledge/v1"
	llamacppv1 "github.com/johnewart/pyxis/gen/llamacpp/v1"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	mcpproxyv1 "github.com/johnewart/pyxis/gen/mcpproxy/v1"
	"github.com/johnewart/pyxis/grpcutf8"
	"github.com/johnewart/pyxis/internal/assistant/history"
	"github.com/johnewart/pyxis/internal/assistant/session"
	knowledgestore "github.com/johnewart/pyxis/internal/knowledge/store"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/llm/retry"
	"github.com/johnewart/pyxis/internal/mcpproxy"
	"github.com/johnewart/pyxis/internal/mcpserver"
	"github.com/johnewart/pyxis/internal/metrics"
	"github.com/johnewart/pyxis/internal/serve"
	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/internal/sessionstate"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/tooldelegate"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/internal/toolprovider"
	"github.com/johnewart/pyxis/internal/toolsession"
	"github.com/johnewart/pyxis/internal/tracing"
	"github.com/johnewart/pyxis/lexstore"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/mcpbridge"
	"github.com/johnewart/pyxis/policyhook"
	"github.com/johnewart/pyxis/toolregistry"
	"github.com/johnewart/pyxis/toolseam"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/gorm"
)

// Host is one wired llmd runtime: databases open and migrated, session
// manager, provider registry, tool surface and gRPC services ready to be
// attached to a server.
//
// A Host binds nothing on its own. Call Register to attach llmd's services to
// a gRPC server you own, or Serve to run one llmd owns.
//
// Every field is unexported on purpose. The types involved (the LlmServer, the
// session manager, ~20 repositories) are llmd's internals and would be a
// compatibility liability if exposed; the methods below are the supported
// surface. When something you need is missing, that is a gap in this facade to
// be filled here — not a reason to reach into llmd/internal.
type Host struct {
	cfg Config

	db *gorm.DB

	llm      *server.LlmServer
	runtimes *sessionmanager.Manager
	toolHub  *toolprovider.Hub
	sessions *toolsession.Service

	sessionRepo      *store.SessionRepo
	sessionEventRepo *store.SessionEventRepo
	sessionStates    *sessionstate.Tracker
	sessionCacheRepo *store.SessionCacheRepo
	knowledge        *knowledgestore.Store
	mcpProxy         *mcpproxy.Registry
	pids             *agentpid.Registry

	// metrics is this runtime's own Prometheus registry (never the global
	// default), so two Hosts in one process keep separate metrics.
	metrics *metrics.Metrics

	// serveSrv is created lazily: only a Host that serves traffic itself (or
	// exposes a browser surface) needs llmd's own *grpc.Server with its
	// interceptors. A Host that only calls Register never builds one.
	serveOnce sync.Once
	serveSrv  *serve.Server

	// MCP HTTP bridge started by StartMCP (nil until then). Guarded by mcpMu:
	// MCPBaseURL is read from provider goroutines via mcpbridge.
	mcpMu  sync.Mutex
	mcpLis net.Listener
	mcpSrv *http.Server

	stopEvict chan struct{}
	closeOnce sync.Once

	// baseCancel ends the server lifetime context that in-flight assistant
	// turns run on (see Config.Context). Called first thing in Close.
	baseCancel context.CancelFunc

	// tracer is owned by the Host (like its other resources) and shut down in
	// closeDatabases; it is a disabled no-op when Config.Tracing has no
	// endpoint.
	tracer *tracing.Provider

	// registry holds the Host's Prometheus collectors (policy hook metrics).
	registry *prometheus.Registry
	// hooks is the guarded policy hook; never nil (no-op when unconfigured).
	hooks      *policyhook.Guard
	policyConn *grpc.ClientConn
}

// turnDrainTimeout bounds how long shutdown waits for cancelled turns to commit
// their salvaged partials before the databases are closed underneath them.
const turnDrainTimeout = 5 * time.Second

// gracefulStopTimeout bounds GracefulStop in Serve. Turns and session-event
// tails are ended before it runs, so anything still open after this is stuck
// and is stopped hard rather than holding the process hostage.
const gracefulStopTimeout = 5 * time.Second

// tracingShutdownTimeout bounds the final span flush at Close.
const tracingShutdownTimeout = 5 * time.Second

// endTurns cancels the server lifetime context — cutting every running turn and
// ending every open session-event tail — and waits (bounded) for the turns to
// commit. Idempotent; called by both Serve's shutdown and Close.
func (h *Host) endTurns() {
	if h.baseCancel != nil {
		h.baseCancel()
	}
	if h.llm != nil && !h.llm.WaitForTurns(turnDrainTimeout) {
		llmdlog.Stdout("llmdhost", llmdlog.LevelWarn,
			"shutdown: in-flight turns did not finish committing within %s; continuing", turnDrainTimeout)
	}
}

// retryPolicyFromConfig builds the API-provider retry policy from the config,
// keeping the retry package defaults for unset (non-positive) values.
func retryPolicyFromConfig(cfg Config) *retry.Policy {
	p := retry.DefaultPolicy()
	if cfg.RetryMaxAttempts > 0 {
		p.MaxAttempts = cfg.RetryMaxAttempts
	}
	if cfg.RetryMinRemaining > 0 {
		p.MinRemaining = cfg.RetryMinRemaining
	}
	return &p
}

// New wires an llmd runtime: it opens and MIGRATES both databases, builds the
// session manager and provider registry, installs the core session tools
// together with the host's own registrars from Config.Tools, and starts idle
// runtime eviction.
//
// It binds no listener and registers nothing. On error, anything already opened
// is closed before returning.
func New(cfg Config) (*Host, error) {
	cfg = cfg.withDefaults()
	step := store.StepFunc(cfg.BootStep)

	var db *gorm.DB
	err := step.Run("open llmd database", func() error {
		var err error
		db, err = store.Open(cfg.DatabaseDSN)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("database %q: open: %w", cfg.DatabaseDSN, err)
	}
	// Refuse pre-tenant (workspace_id) databases BEFORE migrating anything:
	// AutoMigrate cannot convert them (it dies adding NOT NULL tenant_id). The
	// database is closed untouched; the host decides whether to back it up
	// (see LegacyDatabaseError / BackupLegacyDatabases).
	if hasLegacyColumn(db, store.SessionRow{}.TableName()) || hasLegacyColumn(db, store.ChatMessageRow{}.TableName()) {
		if cerr := closeGorm(db); cerr != nil {
			llmdlog.Stdout("llmdhost", llmdlog.LevelWarn, "close legacy database %q: %v", cfg.DatabaseDSN, cerr)
		}
		return nil, &LegacyDatabaseError{Databases: []LegacyDatabase{{Kind: "llmd", DSN: cfg.DatabaseDSN, Path: sqliteFilePath(cfg.DatabaseDSN)}}}
	}

	if err := store.AutoMigrateSteps(db, step); err != nil {
		_ = closeGorm(db)
		return nil, fmt.Errorf("database %q: migrate: %w", cfg.DatabaseDSN, err)
	}

	// The knowledge index is lexical (no embeddings, no cgo). The old SQLite
	// knowledge file, if present, is derived data and is removed.
	var knowledge *knowledgestore.Store
	err = step.Run("open llmd knowledge index", func() error {
		var err error
		knowledge, err = knowledgestore.Open(cfg.KnowledgeDir, cfg.KnowledgeSQLitePath, cfg.KnowledgeOptions)
		return err
	})
	if err != nil {
		_ = closeGorm(db)
		return nil, fmt.Errorf("knowledge index %q: %w", cfg.KnowledgeDir, err)
	}

	// Server lifetime context for in-flight turns: the host's, if given, else
	// our own. Either way we hold the cancel, so Close always ends it.
	parentCtx := cfg.Context
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	baseCtx, baseCancel := context.WithCancel(parentCtx)

	tracer, err := tracing.New(parentCtx, cfg.Tracing)
	if err != nil {
		baseCancel()
		_ = closeGorm(db)
		if cerr := knowledge.Close(); cerr != nil {
			llmdlog.Stdout("llmdhost", llmdlog.LevelWarn, "close knowledge index: %v", cerr)
		}
		return nil, fmt.Errorf("tracing: %w", err)
	}

	registry := prometheus.NewRegistry()
	var policyConn *grpc.ClientConn
	inner := cfg.PolicyHooks
	if inner == nil && strings.TrimSpace(cfg.PolicyEndpoint) != "" {
		var dialOpts []grpc.DialOption
		switch {
		case len(cfg.PolicyDialOptions) > 0:
			dialOpts = append(dialOpts, cfg.PolicyDialOptions...)
		case cfg.PolicyInsecure:
			dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		default:
			dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
		}
		dialOpts = append(dialOpts, grpcutf8.DialOptionsWithTracer(tracer.TracerProvider())...)
		conn, derr := grpc.NewClient(strings.TrimSpace(cfg.PolicyEndpoint), dialOpts...)
		if derr != nil {
			baseCancel()
			_ = closeGorm(db)
			if cerr := knowledge.Close(); cerr != nil {
				llmdlog.Stdout("llmdhost", llmdlog.LevelWarn, "close knowledge index: %v", cerr)
			}
			return nil, fmt.Errorf("policy hook endpoint %q: %w", cfg.PolicyEndpoint, derr)
		}
		policyConn = conn
		gc := policyhook.NewGRPCClient(conn, cfg.PolicyTimeout)
		// The watcher ends when Close shuts the connection down.
		gc.WatchReconnects(baseCtx, conn)
		inner = gc
	}

	h := &Host{
		registry:   registry,
		hooks:      policyhook.NewGuard(inner, cfg.PolicyFailMode, policyhook.NewMetrics(registry), tracer.Tracer()),
		policyConn: policyConn,
		cfg:        cfg,
		db:         db,
		knowledge:  knowledge,
		pids:       agentpid.NewRegistry(),
		metrics:    metrics.New(),
		stopEvict:  make(chan struct{}),
		baseCancel: baseCancel,
		tracer:     tracer,
	}

	h.sessionRepo = store.NewSessionRepo(db)
	h.sessionEventRepo = store.NewSessionEventRepo(db)
	// Created with the repo, before any turn can run, so the fold sees every
	// turn start (see sessionstate.New).
	h.sessionStates = sessionstate.New(h.sessionEventRepo)
	h.sessionCacheRepo = store.NewSessionCacheRepo(db)
	decorationRepo := store.NewTurnDecorationRepo(db)
	sidecarRepo := store.NewToolResultSidecarRepo(db)
	attachmentRepo := store.NewChatAttachmentRepo(db)
	userLlmRepo := store.NewLlmProviderRepo(db)
	userEmbeddingRepo := store.NewEmbeddingProviderRepo(db)
	turnRegister := store.NewTurnRegister(db)

	// The model-catalog cache is derived from userLlmRepo here rather than left
	// to the caller: it is exactly the kind of extra field a host could forget,
	// and did — forgetting it made ListLlmCatalogModels unstable across
	// restarts with no error anywhere.
	composite := providers.NewComposite(nil, userLlmRepo)

	runtimes, err := sessionmanager.New(
		session.HubOptions{KeepEventLog: cfg.KeepSessionEventLog},
		sessionmanager.Options{
			SessionPersist: h.sessionRepo,
			NewSessionHistory: func(sc serverutil.Scope, sessionID string) history.Store {
				if strings.TrimSpace(sc.TenantID) == "" || strings.TrimSpace(sessionID) == "" {
					// Returning nil here would surface much later as a
					// mysterious nil history; refuse at the seam instead.
					return nil
				}
				return store.NewHistory(db, sc, strings.TrimSpace(sessionID))
			},
		},
	)
	if err != nil {
		h.closeDatabases()
		return nil, fmt.Errorf("session manager: %w", err)
	}
	h.runtimes = runtimes
	h.toolHub = toolprovider.NewHub()
	// One shared per-session tool-result pipeline for both the gRPC server and
	// the MCP handler, so dedup state and sidecar writes stay consistent.
	h.sessions = toolsession.New(sidecarRepo, h.sessionRepo, decorationRepo)

	h.llm = &server.LlmServer{
		Providers:           composite,
		Tools:               toolregistry.New(),
		SessionRuntimes:     runtimes,
		ToolWaits:           tooldelegate.NewRegistry(),
		ToolProviderHub:     h.toolHub,
		ProviderSessionSync: store.NewProviderSessionSyncRepo(db),
		EmbeddingProviders:  userEmbeddingRepo,
		TurnMetadata:        store.NewTurnMetadataRepo(db),
		Usage:               store.NewUsageRepo(db),
		ToolUsage:           store.NewToolUsageRepo(db),
		ToolResultSidecar:   sidecarRepo,
		Attachments:         attachmentRepo,
		SessionRepo:         h.sessionRepo,
		AgentProfileRepo:    store.NewAgentProfileRepo(db),
		DecorationsRepo:     decorationRepo,
		TrackedDocs:         store.NewTrackedDocumentRepo(db),
		SessionCache:        h.sessionCacheRepo,
		SessionEvents:       h.sessionEventRepo,
		Storage:             store.NewStorageRepo(db),
		SessionDrafts:       store.NewSessionDraftRepo(db),
		SessionQueue:        store.NewSessionQueueRepo(db),
		Sessions:            h.sessions,
		TurnRegister:        turnRegister,
		Handlers:            toolseam.NewHandlers(),
		BaseContext:         baseCtx,
		Tracer:              tracer.Tracer(),
		RetryPolicy:         retryPolicyFromConfig(cfg),

		DisableServerQueueDrain: cfg.DisableServerQueueDrain,
	}
	if inner != nil {
		// The guard applies the configured fail mode and metrics. Left nil
		// when no hook is configured so the no-op default skips all work.
		h.llm.PolicyHooks = h.hooks
		h.llm.DefaultCheckPolicy = cfg.DefaultCheckPolicy
	}
	// Executor-bound tools dispatch through the hub; inline names are read
	// live from the registry (tools are installed after New returns).
	h.llm.HandlerSet().SetExecutor(h.toolHub)
	// Every tool dispatch (chat loop, MCP, InvokeTool) goes through this one
	// handler set, so observing it here counts all of them.
	h.llm.HandlerSet().SetObserver(metrics.NewTools(h.metrics))
	llmSrv := h.llm
	h.toolHub.InlineNames = func() map[string]struct{} {
		out := map[string]struct{}{}
		if llmSrv.Tools == nil {
			return out
		}
		for _, def := range llmSrv.Tools.Snapshot() {
			if def != nil && def.GetName() != "" {
				out[def.GetName()] = struct{}{}
			}
		}
		return out
	}

	// InstallCoreTools, not InstallTools: it registers the host's registrars AND
	// llmd's core session tools (add_todo, update_todo, list_todos, set_status,
	// set_session_title, ask_user) in one call. Those two were once separate,
	// and a root that forgot the second produced agents unable to set a status
	// or keep a todo list — with no error, because registration is additive and
	// an unregistered tool is simply never advertised. A Host cannot half-wire
	// an agent, which is the main reason this facade exists at all.
	if err := server.InstallCoreTools(h.llm, toolexec.SessionToolsDeps{
		Sessions:    h.sessionRepo,
		Events:      h.sessionEventRepo,
		Turns:       turnRegister,
		Attachments: attachmentRepo,
		Cache:       h.sessionCacheRepo,
	}, cfg.Tools...); err != nil {
		h.closeDatabases()
		return nil, fmt.Errorf("install tools: %w", err)
	}

	if cfg.MCPProxyEnabled {
		h.mcpProxy = mcpproxy.NewRegistry(mcpproxy.RegistryOptions{
			Enabled: true,
			Factory: mcpproxy.DefaultTransportFactory(nil),
		})
	}

	h.startIdleEviction()
	// Queues left over from before a restart: recent ones resume, stale ones are
	// paused for the user to resume. Done here (not in a separate call a host
	// could forget) so every host gets it; the drains themselves run async.
	h.llm.SweepQueuesOnStartup(baseCtx, cfg.QueueResumeWindow)
	return h, nil
}

// InstallProcessDefaults writes this Host's settings into llmd's remaining
// process-wide slots: the agent-PID registry the MCP stdio bridge looks up, the
// Cursor workspace bridge, and the diagnostics sink behind Config.Logger.
//
// It is a SEPARATE call because it mutates global state. New deliberately does
// not do it: with two Hosts in one process the second would silently win, and a
// hidden global write inside a constructor is how that becomes a debugging
// session instead of a compile error. A single-Host process calls this once at
// startup; a process hosting several llmd runtimes calls it at most once, aware
// that these three settings are shared.
func (h *Host) InstallProcessDefaults() {
	agentpid.SetDefaultRegistry(h.pids)
	// A resolver, not a URL: StartMCP may run before or after this call, and
	// until it does the CLI providers fall back to LLMD_MCP_HTTP_* as before.
	mcpbridge.SetDefault(h.MCPBaseURL)
	if h.cfg.CursorWorkspace != nil {
		cursorws.SetDefault(h.cfg.CursorWorkspace)
	}
	if h.cfg.Logger != nil {
		logger := h.cfg.Logger
		llmdlog.SetSink(func(rec llmdlog.Record) {
			logger.LogAttrs(context.Background(), slogLevel(rec.Level), rec.Message,
				slog.String("component", rec.Component),
				slog.String("module", rec.Module),
			)
		})
	}
}

// slogLevel maps llmd's coarse level names onto slog levels. An unrecognised
// name becomes Info rather than being dropped: losing a diagnostic because its
// level string was unexpected is strictly worse than filing it slightly wrong.
func slogLevel(level string) slog.Level {
	switch llmdlog.Level(level) {
	case llmdlog.LevelDebug:
		return slog.LevelDebug
	case llmdlog.LevelWarn:
		return slog.LevelWarn
	case llmdlog.LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Register attaches every llmd gRPC service to a registrar the CALLER owns.
//
// This is the method that makes llmd embeddable: a host with its own gRPC
// server, its own interceptors and its own services adds llmd's alongside them
// instead of running llmd as a separate process. Serve is the standalone
// alternative; do not call both.
func (h *Host) Register(reg grpc.ServiceRegistrar) {
	llmv1.RegisterLlmServiceServer(reg, h.llm)

	// Surface a missing tool-provider secret at startup rather than leaving the
	// operator to infer it from providers whose tools never appear.
	tps := &toolprovider.Server{Hub: h.toolHub, SharedSecret: h.cfg.ToolProviderSecret}
	tps.WarnIfSharedSecretMissing()
	llmv1.RegisterToolProviderServiceServer(reg, tps)
	llmv1.RegisterSessionCacheServiceServer(reg, &server.SessionCacheServer{Repo: h.sessionCacheRepo})

	// llmd owns the managed llama.cpp runtime (binary resolution, GGUF
	// downloads, process health); a host calls through and renders the reported
	// status verbatim.
	llamacppv1.RegisterLlamaCppServiceServer(reg, &server.LlamaCppServer{})

	// Always registered: a host that wants no knowledge index still gets the
	// service, which answers honestly over an empty store rather than being
	// absent and indistinguishable from a version mismatch.
	knowledgev1.RegisterKnowledgeServiceServer(reg, &server.KnowledgeServer{Store: h.knowledge})

	// Additive gating: with MCPProxyEnabled false this service does not exist,
	// so nothing can advertise a proxied tool or mention the feature.
	if h.mcpProxy != nil {
		mcpproxyv1.RegisterMcpProxyServiceServer(reg, &server.McpProxyServer{Registry: h.mcpProxy})
	}
}

// Serve runs llmd standalone on lis using llmd's own gRPC server (with its
// interceptors), and blocks until ctx is cancelled or serving fails. It stops
// the server gracefully on cancellation.
//
// A host embedding llmd in its own server uses Register instead.
func (h *Host) Serve(ctx context.Context, lis net.Listener) error {
	// Services are registered when the server is created, not here: callers
	// legitimately inspect ServiceRoutes (or add their own service) BEFORE
	// serving, and registering at Serve time would have reported an empty
	// surface to anyone who looked early.
	srv := h.serveServer()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.GRPC.Serve(lis) }()

	select {
	case <-ctx.Done():
		// Order matters. Turns run on the server lifetime context, not on their
		// RPC's, so GracefulStop alone would wait forever on any turn still
		// running (and on every open SubscribeSessionEvents tail). End them
		// first — each salvages and commits its partial — then drain.
		h.endTurns()
		stopped := make(chan struct{})
		go func() {
			srv.GRPC.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(gracefulStopTimeout):
			llmdlog.Stdout("llmdhost", llmdlog.LevelWarn,
				"Serve: graceful stop did not finish within %s; forcing stop of remaining RPCs", gracefulStopTimeout)
			srv.GRPC.Stop()
			<-stopped
		}
		// Drain: Serve returns once the server has stopped.
		<-errCh
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// GRPCServer returns the *grpc.Server backing Serve, with llmd's services
// already registered, for a standalone host that must add a service of its own
// on the same server. Created on first use; safe to call before Serve.
//
// A host that owns its own gRPC server calls Register instead and never touches
// this one.
func (h *Host) GRPCServer() *grpc.Server { return h.serveServer().GRPC }

// MetricsHandler serves this runtime's metrics in the Prometheus text format
// (mount it at /metrics). Serve it on a listener you own; llmd does not choose
// ports.
func (h *Host) MetricsHandler() http.Handler { return h.metrics.Handler() }

// MCPHandler is llmd's MCP HTTP surface (POST /mcp JSON-RPC, GET /mcp/health),
// guarded by Config.MCPBearerToken. Serve it on a listener you own; llmd does
// not choose ports.
func (h *Host) MCPHandler() http.Handler {
	var hooks policyhook.Hooks
	if h.llm.PolicyHooks != nil {
		hooks = h.hooks
	}
	return mcpserver.New(mcpserver.Config{
		PolicyHooks:      hooks,
		DB:               h.db,
		SessionRuntimes:  h.runtimes,
		LLM:              h.llm,
		Sessions:         h.sessions,
		AgentPIDRegistry: h.pids,
		BearerToken:      strings.TrimSpace(h.cfg.MCPBearerToken),
	}).Handler()
}

// StartMCP serves MCPHandler on lis, a listener the CALLER owns and chose the
// address of (llmd does not choose ports), until Close. This is the MCP bridge
// llmd's CLI providers (claude-code, cursor-cli) hand to the CLIs they spawn so
// those can call llmd's tools; without it, any turn with tools on those
// providers is refused. Pass a loopback listener (e.g. 127.0.0.1:0) unless
// Config.MCPBearerToken is set.
//
// Once InstallProcessDefaults has run, the providers find this listener
// through mcpbridge on their own. Serve errors after start are logged, not
// returned: they happen on the background goroutine, after this call.
func (h *Host) StartMCP(lis net.Listener) error {
	if lis == nil {
		return fmt.Errorf("llmdhost: StartMCP: nil listener")
	}
	h.mcpMu.Lock()
	defer h.mcpMu.Unlock()
	if h.mcpSrv != nil {
		return fmt.Errorf("llmdhost: StartMCP: already serving MCP on %s", h.mcpLis.Addr())
	}
	srv := &http.Server{Handler: h.MCPHandler(), ReadHeaderTimeout: 10 * time.Second}
	h.mcpLis, h.mcpSrv = lis, srv
	go func() {
		llmdlog.Stdout(MCPLogModule, llmdlog.LevelInfo, "MCP HTTP listening on %s (POST /mcp, GET /mcp/health)", lis.Addr())
		if err := srv.Serve(lis); err != nil && err != http.ErrServerClosed {
			llmdlog.Stdout(MCPLogModule, llmdlog.LevelError, "MCP HTTP on %s stopped: %v", lis.Addr(), err)
		}
	}()
	return nil
}

// MCPBaseURL is the base URL of the bridge started by StartMCP (no /mcp
// suffix), or "" when none is running.
func (h *Host) MCPBaseURL() string {
	h.mcpMu.Lock()
	defer h.mcpMu.Unlock()
	if h.mcpLis == nil {
		return ""
	}
	return mcpbridge.BaseURLFromListenAddr(h.mcpLis.Addr().String())
}

// stopMCP shuts down the StartMCP server, if any. Bounded: a stuck MCP request
// must not hold Close hostage.
func (h *Host) stopMCP() {
	h.mcpMu.Lock()
	srv := h.mcpSrv
	h.mcpSrv, h.mcpLis = nil, nil
	h.mcpMu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), gracefulStopTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		llmdlog.Stdout(MCPLogModule, llmdlog.LevelWarn, "MCP HTTP shutdown: %v; forcing close", err)
		if cerr := srv.Close(); cerr != nil {
			llmdlog.Stdout(MCPLogModule, llmdlog.LevelWarn, "MCP HTTP close: %v", cerr)
		}
	}
}

// BrowserSurfaceConfig configures the grpc-web/Connect + session-events
// WebSocket surface for a browser client.
type BrowserSurfaceConfig struct {
	// Token is the shared secret the browser must present. Required: an empty
	// token would expose the surface unauthenticated.
	Token string
	// AllowedOrigins are exact browser page origins, scheme included. An empty
	// list rejects every cross-origin request, including the WebSocket
	// handshake a dev-server proxy produces.
	AllowedOrigins []string
}

// BrowserSurface returns ONE handler serving both halves of the browser
// surface: grpc-web/Connect RPCs and the /ws/session-events WebSocket, under
// one auth and origin policy.
//
// It is one call by design. Assembling the two halves by hand is how a
// composition root ships the RPC half alone, which fails silently: the browser
// reports "closed before handshake" and llmd logs nothing, because every line
// that would explain it lives in code that never ran.
func (h *Host) BrowserSurface(cfg BrowserSurfaceConfig) (http.Handler, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("browser surface: token is required (an empty token would serve it unauthenticated)")
	}
	return h.serveServer().BrowserSurface(serve.BrowserSurfaceConfig{
		SessionEvents:  h.sessionEventRepo,
		SessionStates:  h.sessionStates,
		Token:          cfg.Token,
		AllowedOrigins: cfg.AllowedOrigins,
	}), nil
}

// ServiceRoute is one gRPC service and the methods it serves.
type ServiceRoute struct {
	Service string
	Methods []string
}

// ServiceRoutes reports what the standalone server actually serves. A service
// that never registered, or a stale binary missing a method the client calls,
// is otherwise indistinguishable at runtime from a transport failure — both
// surface client-side as a failed call.
func (h *Host) ServiceRoutes() []ServiceRoute {
	src := serve.ServiceRoutes(h.serveServer().GRPC)
	out := make([]ServiceRoute, 0, len(src))
	for _, r := range src {
		out = append(out, ServiceRoute{Service: r.Service, Methods: r.Methods})
	}
	return out
}

// Tools is the live registry of everything this Host advertises. A registrar
// may add to it after New; registration is additive.
func (h *Host) Tools() *toolregistry.Registry { return h.llm.Tools }

// Handlers is the live handler set this Host dispatches through. Pair every
// addition with a Tools registration or the tool is never offered to a model.
func (h *Host) Handlers() *toolseam.Handlers { return h.llm.HandlerSet() }

// Hooks is the guarded policy hook (fail mode and metrics applied). With no
// hook configured it allows everything.
func (h *Host) Hooks() *policyhook.Guard { return h.hooks }

// PolicyCheckPolicy is the operator default CheckPolicy.
func (h *Host) PolicyCheckPolicy() *policyhook.CheckPolicy { return h.cfg.DefaultCheckPolicy }

// MetricsRegistry is the Host's Prometheus registry.
func (h *Host) MetricsRegistry() *prometheus.Registry { return h.registry }

// AgentPIDs maps spawned agent-CLI process ids to chat scope, for a host
// running llmd's MCP stdio bridge.
func (h *Host) AgentPIDs() *agentpid.Registry { return h.pids }

// RuntimeCount is the number of session runtimes currently loaded in memory.
// Runtimes are a cache over DB state, so this is an occupancy gauge, not a
// count of live sessions.
func (h *Host) RuntimeCount() int { return h.runtimes.RuntimeCount() }

// DatabaseDSN and KnowledgeDSN report the resolved data locations, for a host
// that logs where its state lives. KnowledgeDSN is the NORMALIZED form, which
// is what actually got opened.
func (h *Host) DatabaseDSN() string  { return h.cfg.DatabaseDSN }
func (h *Host) KnowledgeDSN() string { return h.cfg.KnowledgeDir }

// KnowledgeStore is the knowledge index itself, for a host that indexes or
// queries in-process instead of over the KnowledgeService RPCs. Spaces used
// through the RPCs are tenant "\x1f" scope; a host may use any other space.
func (h *Host) KnowledgeStore() *lexstore.Store { return h.knowledge.Lex() }

// SQLDatabases exposes the underlying *sql.DB handles for diagnostics (page
// counts, table sizes). knowledge is always nil: the knowledge index is a
// pebble store, not SQL. These are for INSPECTION: writing through them
// bypasses llmd's models.
func (h *Host) SQLDatabases() (agent, knowledge *sql.DB) {
	if sdb, err := h.db.DB(); err == nil {
		agent = sdb
	}
	return agent, nil
}

// Close stops background work and releases the Host's resources. Safe to call
// more than once. It does not stop a server passed to Register — that one
// belongs to the caller.
func (h *Host) Close() error {
	h.closeOnce.Do(func() {
		// Cut in-flight turns first, then give them a bounded window to commit
		// their salvaged partials while the databases are still open.
		h.endTurns()
		h.stopMCP()
		close(h.stopEvict)
		if h.mcpProxy != nil {
			h.mcpProxy.Close()
		}
		h.closeDatabases()
	})
	return nil
}

// serveServer builds llmd's own gRPC server on first use and registers llmd's
// services into it immediately, so every accessor below observes the same fully
// populated surface no matter what order a caller uses them in. Guarded by
// sync.Once because registering a service twice panics.
func (h *Host) serveServer() *serve.Server {
	h.serveOnce.Do(func() {
		var opts []grpc.ServerOption
		if len(h.cfg.UnaryInterceptors) > 0 {
			opts = append(opts, grpc.ChainUnaryInterceptor(h.cfg.UnaryInterceptors...))
		}
		if len(h.cfg.StreamInterceptors) > 0 {
			opts = append(opts, grpc.ChainStreamInterceptor(h.cfg.StreamInterceptors...))
		}
		opts = append(opts, h.cfg.ServerOptions...)
		h.serveSrv = serve.NewServer(opts...)
		h.Register(h.serveSrv.GRPC)
	})
	return h.serveSrv
}

func (h *Host) closeDatabases() {
	if h.tracer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), tracingShutdownTimeout)
		if err := h.tracer.Shutdown(ctx); err != nil {
			llmdlog.Stdout("llmdhost", llmdlog.LevelWarn, "tracing shutdown: %v", err)
		}
		cancel()
	}
	if h.policyConn != nil {
		if err := h.policyConn.Close(); err != nil {
			llmdlog.Stdout("llmdhost", llmdlog.LevelWarn, "close policy hook connection: %v", err)
		}
	}
	if h.db != nil {
		if sdb, err := h.db.DB(); err == nil {
			_ = sdb.Close()
		}
	}
	if h.knowledge != nil {
		_ = h.knowledge.Close()
	}
}

// startIdleEviction unloads runtimes that have gone quiet. The sweep runs at a
// sixth of the threshold (min 1 minute) so eviction is timely without polling
// hot.
func (h *Host) startIdleEviction() {
	idle := h.cfg.RuntimeIdleEvict
	if idle <= 0 {
		return
	}
	sweep := idle / 6
	if sweep < time.Minute {
		sweep = time.Minute
	}
	go func() {
		t := time.NewTicker(sweep)
		defer t.Stop()
		for {
			select {
			case <-h.stopEvict:
				return
			case <-t.C:
				h.runtimes.EvictIdle(idle)
			}
		}
	}()
}
