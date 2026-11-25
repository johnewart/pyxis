// Package toolexecutor is the executor side of the ToolProviderService stream:
// it advertises a set of locally-handled tools (ExecutorHello) and runs the
// ExecutorToolCall frames llmd sends, replying with exactly one ToolCallResult
// per call. It is public so hosts in other modules can import it.
//
// This is the supported way for a host outside llmd's process or Go module to
// declare and serve tools. The wire protocol it speaks (the "executor
// protocol") is documented on llmv1.ToolProviderToServer.
//
// # Usage
//
// Build a toolseam.Handlers holding your tool handlers, pass the matching
// definitions plus an allowlist, and run the client over a gRPC connection to
// llmd. Only tools that are allowlisted AND have a registered handler are
// advertised; each is rebound to execution = executor{ExecutorID} for you.
//
//	handlers := toolseam.NewHandlers()
//	_ = handlers.Register("my_tool", myHandler)
//
//	cli, err := toolexecutor.New(toolexecutor.Options{
//	    ExecutorID:   "my-host",                       // stable per host product
//	    Handlers:     handlers,
//	    Definitions:  []*llmv1.ToolDefinition{myToolDef},
//	    Allowlist:    []string{"my_tool"},
//	    SharedSecret: secret, // same value as llmdhost.Config.ToolProviderSecret
//	})
//	if err != nil { ... }
//	// conn is any grpc.ClientConnInterface to llmd (TCP, bufconn, ...).
//	go cli.Run(ctx, llmv1.NewToolProviderServiceClient(conn),
//	    []tenantscope.Scope{{TenantID: "tenant-a"}})
//
// Run opens one Connect stream per tenant scope and blocks until ctx ends. For
// each stream it sets the tenant metadata (Scope.OutgoingContext) and sends
// SharedSecret in metadata key SecretMetadataKey; llmd refuses the stream if
// it does not match llmd's configured secret (llmdhost.Config.ToolProviderSecret)
// or if llmd has none. An in-process host passes the same value to both. Rejected tools are logged, recorded in Rejections, and passed to
// Options.OnAck along with the full ExecutorHelloAck.
//
// # Scope and session binding (middleware)
//
// Executor registration is tenant-wide, but each call carries the calling
// session's scope: Invocation.Scope is the stream's tenant plus the call's
// scope_id (falling back to the stream's scope when llmd sent none), and
// TurnID, SessionID and Origin are set from the call. Handlers can therefore
// bind per-scope state from inv.Scope exactly as inline handlers do. A host
// that maps sessions to finer-grained state (e.g. a specific workspace within
// a scope) wraps every handler before passing the set to New:
//
//	func bindWorkspace(base *toolseam.Handlers, resolve func(ctx context.Context, sc tenantscope.Scope, sessionID string) (context.Context, error)) *toolseam.Handlers {
//	    out := toolseam.NewHandlers()
//	    for _, name := range base.Names() {
//	        fn, _ := base.Lookup(name)
//	        _ = out.Register(name, func(ctx context.Context, args json.RawMessage, inv toolseam.Invocation) (toolseam.Result, error) {
//	            ctx, err := resolve(ctx, inv.Scope, inv.SessionID)
//	            if err != nil {
//	                return toolseam.Result{}, fmt.Errorf("resolve workspace for session %q: %w", inv.SessionID, err)
//	            }
//	            return fn(ctx, args, inv)
//	        })
//	    }
//	    return out
//	}
//
// The same wrapping works for run accounting, auth checks, or tracing.
//
// # Calls, cancellation and disconnects
//
//   - Each call runs in its own goroutine under a context bounded by
//     ExecutorToolCall.max_duration_seconds (DefaultMaxDuration if unset).
//     A handler error becomes a ToolCallFailure; invalid argument JSON fails
//     the call without invoking the handler.
//   - A ToolCallCancel from llmd (turn aborted, deadline, caller gone) cancels
//     that call's context; handlers should honour ctx.
//   - When the stream ends, in-flight handler contexts are cancelled and llmd
//     fails its pending calls as "disconnected, outcome unknown". The tools
//     stay registered in llmd without an owner, so sessions still see them,
//     but calls fail fast until the executor reconnects.
//   - Run reconnects with capped exponential backoff (BackoffBase..BackoffMax,
//     reset after any acked session) and re-sends the same instance id, which
//     is minted once per Client. A different process (new Client) with the
//     same ExecutorID supersedes the old stream.
//
// See llmdtest/executor_e2e_inprocess_test.go for a complete in-process
// example against a real llmd host.
package toolexecutor

import (
	"fmt"
	"strings"
	"sync"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/tenantscope"
	"github.com/johnewart/pyxis/toolseam"
	"google.golang.org/protobuf/proto"
)

const logModule = "toolexecutor"

// LogFunc receives diagnostics. The default writes through llmdlog.Stderr.
type LogFunc func(level llmdlog.Level, format string, args ...any)

// Options configures a Client.
type Options struct {
	ExecutorID  string
	Handlers    *toolseam.Handlers
	Definitions []*llmv1.ToolDefinition
	Allowlist   []string
	// Logger overrides the diagnostics sink (tests); nil ⇒ llmdlog.Stderr.
	Logger LogFunc

	// SharedSecret is presented to llmd in stream metadata on every connect.
	SharedSecret string
	// BackoffBase / BackoffMax bound the reconnect delay (doubling from base,
	// capped at max). Zero ⇒ DefaultBackoffBase / DefaultBackoffMax.
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// OnAck, when non-nil, observes every ExecutorHelloAck received.
	OnAck func(scope tenantscope.Scope, ack *llmv1.ExecutorHelloAck)
}

// Client dispatches executor tool calls into a toolseam.Handlers set.
type Client struct {
	opts    Options
	allowed map[string]bool
	log     LogFunc

	mu       sync.Mutex
	inflight map[string]*inflightCall

	// instanceID is minted once per Client (one process) and reused on every
	// reconnect so the hub sees the same instance coming back.
	instanceID string

	rejMu    sync.Mutex
	rejected []Rejection
}

// New validates opts and returns a Client.
func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.ExecutorID) == "" {
		return nil, fmt.Errorf("toolexecutor: ExecutorID is required")
	}
	if opts.Handlers == nil {
		return nil, fmt.Errorf("toolexecutor: Handlers is required")
	}
	allowed := make(map[string]bool, len(opts.Allowlist))
	for _, n := range opts.Allowlist {
		if n = strings.TrimSpace(n); n != "" {
			allowed[n] = true
		}
	}
	log := opts.Logger
	if log == nil {
		log = func(level llmdlog.Level, format string, args ...any) {
			llmdlog.Stderr(logModule, level, format, args...)
		}
	}
	inst, err := newInstanceID()
	if err != nil {
		return nil, fmt.Errorf("toolexecutor: mint instance id: %w", err)
	}
	return &Client{opts: opts, allowed: allowed, log: log, inflight: make(map[string]*inflightCall), instanceID: inst}, nil
}

// ExecutorID returns the configured executor id.
func (c *Client) ExecutorID() string { return c.opts.ExecutorID }

func (c *Client) isServed(name string) bool {
	if !c.allowed[name] {
		return false
	}
	_, ok := c.opts.Handlers.Lookup(name)
	return ok
}

// Hello builds the ExecutorHello advertising the allowlisted definitions that
// have a registered handler, each rebound to this executor.
func (c *Client) Hello(instanceID string) *llmv1.ExecutorHello {
	out := &llmv1.ExecutorHello{ExecutorId: c.opts.ExecutorID, InstanceId: instanceID}
	for _, def := range c.opts.Definitions {
		if def == nil || !c.isServed(def.GetName()) {
			continue
		}
		var maxDur uint32
		if ex := def.GetExecutor(); ex != nil {
			maxDur = ex.GetMaxDurationSeconds()
		}
		cl := proto.Clone(def).(*llmv1.ToolDefinition)
		cl.Execution = &llmv1.ToolDefinition_Executor{Executor: &llmv1.ToolExecutionExecutor{
			ExecutorId:         c.opts.ExecutorID,
			MaxDurationSeconds: maxDur,
		}}
		out.Tools = append(out.Tools, cl)
	}
	return out
}
