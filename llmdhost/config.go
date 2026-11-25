// Package llmdhost runs llmd's runtime inside your own process.
//
// It answers the question "how do I host llmd?", the companion to
// llmdtools/toolseam's "how do I contribute a tool?". Between them a consumer
// needs nothing from llmd/internal/...
//
// The shape is deliberate:
//
//	host, err := llmdhost.New(llmdhost.Config{
//	    DatabaseDSN: "llmd.sqlite",
//	    Tools: []llmdtools.RegisterFunc{graphtools.Register, wstools.Register},
//	})
//	defer host.Close()
//	host.Register(myGRPCServer)   // llmd rides on a server the CALLER owns
//
// New wires and migrates but BINDS NOTHING: no listener, no port, no goroutine
// serving traffic. That separation is what lets a host expose llmd as a
// service on its own gRPC server instead of running llmd standalone, while
// llmd's own binary calls Serve.
//
// Config carries VALUES, never environment variables. llmd does not read the
// environment on a host's behalf: a process that embeds llmd has its own
// configuration system, and a library that silently consults LLMD_* behind its
// caller's back is impossible to test and impossible to run twice in one
// process. llmd's own binary does the env parsing and passes the results here.
package llmdhost

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnewart/pyxis/cursorws"
	"github.com/johnewart/pyxis/internal/tracing"
	"github.com/johnewart/pyxis/lexstore"
	"github.com/johnewart/pyxis/llmdtools"
	"github.com/johnewart/pyxis/policyhook"
	"google.golang.org/grpc"
)

// TracingConfig is the OTLP tracing configuration (see Config.Tracing).
type TracingConfig = tracing.Config

// Policy hook types, re-exported so embedders can name them.
type (
	PolicyHooks       = policyhook.Hooks
	PolicyFailMode    = policyhook.FailMode
	PolicyCheckPolicy = policyhook.CheckPolicy
)

// Fail modes for Config.PolicyFailMode.
const (
	PolicyFailClosed = policyhook.FailClosed
	PolicyFailOpen   = policyhook.FailOpen
)

// DefaultPolicyTimeout bounds each remote policy hook call when
// Config.PolicyTimeout is zero.
const DefaultPolicyTimeout = 5 * time.Second

// Config describes one llmd runtime. The zero value is usable: it yields an
// in-process llmd with SQLite files in the working directory, no host tools and
// no upstream MCP proxy.
type Config struct {
	// DatabaseDSN is llmd's main (agent/session/provider) database. Defaults to
	// "llmd.sqlite" in the working directory. Migrations run during New.
	DatabaseDSN string

	// BootStep, when set, runs each part of New's startup work as a named,
	// timed step: opening the database, the schema migration, each pending
	// data migration, opening the knowledge index. It must call fn and
	// return its error. A host passes its startup tracer here so a slow
	// migration shows by name. nil runs everything directly.
	BootStep func(name string, fn func() error) error

	// KnowledgeDir is the directory of llmd's knowledge index: a lexical
	// store (lexstore: BM25 + name trigrams in pebble), deliberately SEPARATE
	// from the main database. Empty derives it from KnowledgeSQLitePath
	// ("x.sqlite" -> "x.pebble"), or "llmd-knowledge.pebble".
	KnowledgeDir string

	// KnowledgeSQLitePath is where the pre-lexical SQLite knowledge database
	// lived. A file there is deleted at startup (the index is derived data
	// clients re-upsert); it also seeds KnowledgeDir's default. Defaults to
	// "llmd-knowledge.sqlite".
	KnowledgeSQLitePath string

	// KnowledgeOptions plugs a corpus into the knowledge index: chunker,
	// namer, reranker, FileOf and an Observer for metrics (see lexstore).
	// Zero values select lexstore's defaults; the reranker defaults to
	// lexstore.TermOverlapReranker.
	KnowledgeOptions lexstore.Options

	// Tools are the host's in-process tool registrars — the only way to install
	// tools at construction time (executors add more at runtime; see below).
	// llmd contributes no product tools of its own; see llmdtools.
	//
	// Each registrar closes over whatever its tools need and resolves it per
	// call from toolseam.Invocation.Scope. llmd never learns what those
	// dependencies are, which is why it can be hosted without importing its
	// consumers.
	//
	// This in-memory path is for tools whose handlers run inside the process
	// that embeds llmdhost. A host that
	// runs in another process or Go module should instead serve its tools with
	// package toolexecutor over ToolProviderService.Connect (the executor
	// protocol; see llmv1.ToolProviderToServer). Executor tools are registered
	// per tenant at runtime and are rejected if their name is already in the
	// host's tool registry (which includes every tool installed here).
	Tools []llmdtools.RegisterFunc

	// ToolProviderSecret is the shared secret every ToolProviderService.Connect
	// stream must present (executors in metadata key
	// "x-llmd-tool-provider-secret", see toolexecutor.Options.SharedSecret;
	// legacy providers in ToolProviderHello.shared_secret). Empty refuses every
	// registration and logs a warning at Register. llmdhost never reads it from
	// the environment; the standalone binary maps
	// LLMD_TOOL_PROVIDER_SHARED_SECRET onto this field.
	ToolProviderSecret string

	// RuntimeIdleEvict unloads an in-memory session runtime after this much
	// inactivity; runtimes are only a cache over DB state and reload on demand.
	// Zero uses DefaultRuntimeIdleEvict; negative disables eviction entirely.
	RuntimeIdleEvict time.Duration

	// RetryMaxAttempts is the default total attempts (first try included) for
	// API-provider calls. Zero uses the retry package default.
	RetryMaxAttempts int

	// RetryMinRemaining is the least time that must remain before the context
	// deadline for another attempt to start. Zero uses the retry package
	// default.
	RetryMinRemaining time.Duration

	// DisableServerQueueDrain stops llmd from running queued messages itself,
	// leaving every queue to client drainers (the pre-server-drain behavior).
	// llmd normally drains a session's queue whenever it goes idle, except on
	// sessions with client-owned tools. Mainly for tests that drive the queue by
	// hand.
	DisableServerQueueDrain bool

	// QueueResumeWindow decides what happens at startup to queues left over
	// from before a restart. A queue whose newest entry is younger than this is
	// drained as usual; an older one is PAUSED (never dropped) so stale messages
	// don't fire unexpectedly — the user resumes it when they want it. Zero uses
	// DefaultQueueResumeWindow; negative pauses every leftover queue.
	QueueResumeWindow time.Duration

	// KeepSessionEventLog retains up to 10k frames per session runtime in
	// memory. Production reads stream replay from the session-events store
	// instead, so this stays off unless a test asserts on hub events.
	KeepSessionEventLog bool

	// MCPProxyEnabled registers the upstream-MCP proxy service. Gating is
	// additive: when false the service is never registered, so no proxied tool
	// or error message can mention it.
	MCPProxyEnabled bool

	// Tracing configures OpenTelemetry tracing. An empty Tracing.Endpoint
	// leaves tracing off: no exporter is created and spans are no-ops. The
	// Host owns the provider and shuts it down in Close; no process-wide
	// OTel global is installed.
	Tracing TracingConfig

	// MCPBearerToken guards the MCP HTTP handler returned by MCPHandler. Empty
	// means unauthenticated — only safe on a loopback listener.
	MCPBearerToken string

	// CursorWorkspace lets llmd configure the Cursor CLI it spawns (writing
	// .cursor/mcp.json and .mdc files so that CLI can reach llmd's MCP
	// endpoint). Nil keeps a no-op that mutates no workspace.
	//
	// NOTE: installing this currently sets process-wide state via
	// cursorws.SetDefault, so two Hosts in one process share one bridge. That
	// is a known wart inherited from the pre-facade design; it belongs on the
	// Host. New does NOT install it for you — call InstallProcessDefaults if
	// you want that global write, so the choice stays visible at your call site.
	CursorWorkspace cursorws.CursorWorkspace

	// There is deliberately NO Prompt field. System prompt sections are
	// per-request state, not host configuration: they arrive on
	// ChatStreamRequest, as gRPC metadata, or from the session's agent profile,
	// and llmd already has a precedence order among those three. A host-wide
	// default would add a fourth rung to that ladder for no one's benefit —
	// compose sections with the systemprompt package and send them with the
	// call. See examples/embed for the shape.

	// Logger receives llmd's diagnostics. Nil leaves them on the process's
	// stdout/stderr as newline-delimited JSON.
	//
	// An embedded llmd otherwise logs into YOUR process's stdout in a format
	// your logging system does not read — so an operator looking at that system
	// concludes llmd said nothing, when in fact it explained the problem to a
	// stream nobody was watching. Set this and llmd's lines land beside yours,
	// with its module name carried as an attribute.
	//
	// Like the other process-wide settings here, it takes effect only on
	// InstallProcessDefaults.
	Logger *slog.Logger

	// UnaryInterceptors and StreamInterceptors run on llmd's own gRPC server
	// (GRPCServer, and so Serve and BrowserSurface), after llmd's own
	// interceptors. They are how a host enforces policy on every call into
	// llmd - refusing a turn, counting the ones in flight - whether the call
	// came from a browser, a remote client or the host itself over bufconn.
	// They do not apply to Register(yourServer): a host that registers llmd on
	// its own server installs its interceptors there.
	UnaryInterceptors  []grpc.UnaryServerInterceptor
	StreamInterceptors []grpc.StreamServerInterceptor

	// ServerOptions are extra options for llmd's own gRPC server, such as
	// grpc.Creds for TLS/mTLS. Optional; embedders can leave it nil.
	ServerOptions []grpc.ServerOption

	// PolicyHooks is an in-process policy hook: any implementation of
	// policyhook.Hooks (github.com/johnewart/pyxis/policyhook — a PUBLIC
	// package, so a host wraps its own decision engine directly instead of
	// standing up a remote PolicyHookService to answer questions asked inside
	// its own binary). policyhook.Funcs adapts plain functions. Nil with an
	// empty PolicyEndpoint means no hook: everything is allowed.
	PolicyHooks PolicyHooks

	// PolicyEndpoint is the address of a remote PolicyHookService. Used only
	// when PolicyHooks is nil. The connection is dialed lazily.
	PolicyEndpoint string

	// PolicyDialOptions are extra dial options for the remote hook
	// connection (e.g. grpc.WithTransportCredentials for mTLS or a custom CA).
	// When empty, the connection uses the system-root TLS credentials unless
	// PolicyInsecure is set; when non-empty, the options must carry the
	// transport credentials. Hook requests
	// carry messages, tool arguments and the system prompt, so plaintext is
	// never the default. Trace propagation is always installed.
	PolicyDialOptions []grpc.DialOption

	// PolicyInsecure opts in to a plaintext connection to PolicyEndpoint (for
	// loopback or an already-secured network). Ignored when PolicyDialOptions
	// supplies credentials.
	PolicyInsecure bool

	// PolicyTimeout bounds each remote hook call. Zero uses
	// DefaultPolicyTimeout.
	PolicyTimeout time.Duration

	// DefaultCheckPolicy is the operator default CheckPolicy; a hook's own
	// policy overrides it. Nil uses the built-in LATEST + EVERY_ROUND.
	DefaultCheckPolicy *PolicyCheckPolicy

	// PolicyFailMode applies only to a configured hook that errors or times
	// out. The zero value is closed (deny).
	PolicyFailMode PolicyFailMode

	// Context is the optional server lifetime context. Assistant turns run on a
	// context derived from it rather than from the client's request, so a
	// client disconnecting (navigation, reload) never cancels a turn — only an
	// explicit CancelTurn does, or this context ending. Nil means llmd owns the
	// lifetime itself. Either way Close cancels in-flight turns and waits
	// briefly for their salvaged partials to commit before closing databases.
	Context context.Context
}

// DefaultRuntimeIdleEvict is how long an idle session runtime survives before
// being unloaded when Config.RuntimeIdleEvict is zero.
const DefaultRuntimeIdleEvict = 30 * time.Minute

// DefaultQueueResumeWindow is how recent a leftover queue must be to resume
// automatically at startup when Config.QueueResumeWindow is zero.
const DefaultQueueResumeWindow = 12 * time.Hour

const (
	defaultDatabaseDSN     = "llmd.sqlite"
	defaultKnowledgeSQLite = "llmd-knowledge.sqlite"
)

// withDefaults returns cfg with empty fields filled in, so New has exactly one
// place where a default is decided.
func (c Config) withDefaults() Config {
	if c.DatabaseDSN == "" {
		c.DatabaseDSN = defaultDatabaseDSN
	}
	if c.KnowledgeSQLitePath == "" {
		c.KnowledgeSQLitePath = defaultKnowledgeSQLite
	}
	if c.KnowledgeDir == "" {
		c.KnowledgeDir = strings.TrimSuffix(c.KnowledgeSQLitePath, filepath.Ext(c.KnowledgeSQLitePath)) + ".pebble"
	}
	if c.RuntimeIdleEvict == 0 {
		c.RuntimeIdleEvict = DefaultRuntimeIdleEvict
	}
	if c.PolicyTimeout <= 0 {
		c.PolicyTimeout = DefaultPolicyTimeout
	}
	if c.DefaultCheckPolicy == nil {
		c.DefaultCheckPolicy = policyhook.DefaultCheckPolicy()
	}
	if c.QueueResumeWindow == 0 {
		c.QueueResumeWindow = DefaultQueueResumeWindow
	}
	return c
}
