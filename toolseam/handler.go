// Package toolseam holds the tool-dispatch seam shared by llmd core and any
// module that contributes tools to it. Core owns the seam; contributors
// implement it.
//
// It is a standalone package precisely so two independent tool-contributing
// modules can import it without a cycle: Handlers.Register requires the SAME
// Handler func type on both sides, so a locally-declared interface on either
// side cannot substitute for a shared package.
package toolseam

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/tenantscope"
)

// Handler executes one inline-builtin tool in-process. It receives the raw
// arguments JSON the model produced and the Invocation identifying this call.
//
// A handler receives NO capabilities. Anything a tool needs to do its work —
// workspace file access, a dependency graph, a work tracker, an embedding
// index — is closed over by the registrar that owns it and resolved per call
// from Invocation.Scope. This seam deliberately has no field, no
// interface and no `any` for such a capability: adding one would require a
// change to llmd every time a consumer contributes a tool with a new
// dependency, which is precisely what this package exists to avoid.
//
// A handler returns a Result whose Content becomes the assistant-visible tool
// message. Returning a non-nil error marks the invocation as failed; the error
// text is surfaced as the telemetry/UI error message.
type Handler func(ctx context.Context, args json.RawMessage, inv Invocation) (Result, error)

// Result is what an inline-builtin handler produced.
type Result struct {
	// Content is the assistant-visible tool message (typically a JSON document).
	Content string
	// EndsTurn asks the agent loop to end the turn after this round's tool
	// calls finish, instead of querying the model again (e.g. a tool that
	// proposes a change and waits for user approval). Honored only when the
	// handler returns a nil error; see TurnControl.
	EndsTurn bool
	// Cards are display-only attachments llmd puts in the session right after
	// this call (an image, a chart; see Card). Delivered only on success; if
	// they cannot be shown the call fails, so the model is never told the user
	// saw something they did not.
	Cards []Card
}

// Invocation is one tool_use the model requested.
type Invocation struct {
	ToolCallID    string
	Name          string
	ArgumentsJSON string
	AgentID       string
	SessionID     string
	ReadOnly      bool
	// Scope is the (tenant, scope) this invocation is bound to, resolved once
	// by the host and handed to every handler unchanged. A self-contained
	// handler closure — e.g. one that dials its own per-scope dependency (file
	// access, graph, work tracker) — uses this to resolve that dependency
	// itself, without an out-of-band session→scope lookup.
	Scope tenantscope.Scope
	// ToolContext is the caller-supplied key/value context for this call: the
	// ChatStreamRequest tool_context if non-empty, else the session's
	// EnsureSession tool_context. It is a copy owned by this Invocation (nil
	// when none). Identity (tenant, scope, principal) is Scope, which already
	// carries PrincipalID, so there is no separate principal field.
	ToolContext map[string]string
	// TurnID identifies the chat turn this invocation belongs to, when known.
	TurnID string
	// Origin records which dispatch path produced this invocation.
	Origin Origin
}

// Origin identifies the dispatch path a tool invocation came from.
type Origin int

const (
	// OriginUnspecified is the zero value: the caller did not say.
	OriginUnspecified Origin = iota
	// OriginChatLoop is a tool_use requested by the model in the agent chat loop.
	OriginChatLoop
	// OriginMCP is a call arriving through llmd's MCP server surface.
	OriginMCP
	// OriginInvokeTool is a direct InvokeTool RPC.
	OriginInvokeTool
)

// String returns a stable lowercase name for the origin.
func (o Origin) String() string {
	switch o {
	case OriginChatLoop:
		return "chat_loop"
	case OriginMCP:
		return "mcp"
	case OriginInvokeTool:
		return "invoke_tool"
	default:
		return "unspecified"
	}
}

// ExecutorDispatcher dispatches a tool whose definition names an external
// executor (e.g. a connected tool provider stream). It is a dispatch seam,
// not a Handler capability.
type ExecutorDispatcher interface {
	Call(ctx context.Context, def *llmv1.ToolDefinition, inv Invocation) (content string, success bool, errMsg string)
}

// Handlers is a name→Handler map for inline-builtin tools. It is safe for
// concurrent use. Registration is additive: a tool with no registered handler
// is simply unknown and fails to execute.
type Handlers struct {
	mu     sync.RWMutex
	byName map[string]Handler
	// executor dispatches executor-backed tools; guarded by mu.
	executor ExecutorDispatcher
	// cards delivers tool-returned cards; guarded by mu.
	cards CardSink
	// observer sees every dispatch through this set; guarded by mu.
	observer InvocationObserver
}

// Binding names how a tool definition executes.
type Binding string

const (
	BindingInline   Binding = "inline"
	BindingWebhook  Binding = "webhook"
	BindingExecutor Binding = "executor"
	BindingDelegate Binding = "delegate"
	BindingNone     Binding = "none"
)

// Outcome classifies how one dispatch ended. The set is closed so it can be
// used as a metric label.
type Outcome string

const (
	OutcomeOK            Outcome = "ok"
	OutcomeError         Outcome = "error"
	OutcomeReadOnly      Outcome = "read_only_rejected"
	OutcomeNotRegistered Outcome = "not_registered"
	OutcomeNotConfigured Outcome = "not_configured"
	OutcomeTimeout       Outcome = "timeout"
	OutcomeCanceled      Outcome = "canceled"
)

// InvocationResult is what an observer learns when a dispatch ends.
type InvocationResult struct {
	Outcome Outcome
	// ResultBytes is len(content) returned to the caller.
	ResultBytes int
}

// InvocationObserver watches tool dispatches (metrics, tracing). Begin is
// called when a dispatch starts and returns the func called exactly once when
// it ends. tool is the registered definition's name, or "" when the call named
// no registered tool (the model-supplied name is deliberately withheld so an
// observer never labels by arbitrary input).
type InvocationObserver interface {
	Begin(tool string, origin Origin, binding Binding) (done func(InvocationResult))
}

// SetObserver installs (or clears, with nil) the dispatch observer.
func (h *Handlers) SetObserver(o InvocationObserver) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.observer = o
}

// Observer returns the installed observer, or nil. Safe on a nil receiver.
func (h *Handlers) Observer() InvocationObserver {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.observer
}

// SetCardSink installs (or clears, with nil) the sink for tool-returned cards.
func (h *Handlers) SetCardSink(s CardSink) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cards = s
}

// CardSink returns the installed card sink, or nil. Safe on a nil receiver.
func (h *Handlers) CardSink() CardSink {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cards
}

// SetExecutor installs (or clears, with nil) the executor dispatcher.
func (h *Handlers) SetExecutor(d ExecutorDispatcher) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.executor = d
}

// Executor returns the installed executor dispatcher, or nil. Safe on a nil receiver.
func (h *Handlers) Executor() ExecutorDispatcher {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.executor
}

// NewHandlers returns an empty handler set.
func NewHandlers() *Handlers {
	return &Handlers{byName: make(map[string]Handler)}
}

// Register adds (or replaces) the handler for a tool name.
func (h *Handlers) Register(name string, fn Handler) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("handler name is required")
	}
	if fn == nil {
		return fmt.Errorf("handler is nil")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byName == nil {
		h.byName = make(map[string]Handler)
	}
	h.byName[name] = fn
	return nil
}

// Unregister removes a handler. It reports whether one was present.
func (h *Handlers) Unregister(name string) bool {
	name = strings.TrimSpace(name)
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.byName[name]; !ok {
		return false
	}
	delete(h.byName, name)
	return true
}

// Lookup returns the handler registered for name, if any.
func (h *Handlers) Lookup(name string) (Handler, bool) {
	if h == nil {
		return nil, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	fn, ok := h.byName[strings.TrimSpace(name)]
	return fn, ok
}

// Names returns the registered handler names in stable order.
func (h *Handlers) Names() []string {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.byName))
	for n := range h.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// RunInlineBuiltin dispatches one inline-builtin tool invocation by name
// through handlers, using inv.ArgumentsJSON as the argument payload. It is
// exported (unlike toolexec's unexported runInlineBuiltin) so
// llmd/internal/toolexec can alias it as the implementation of its own
// runInlineBuiltin helper.
func RunInlineBuiltin(ctx context.Context, name string, handlers *Handlers, inv Invocation) (content string, success bool, errMsg string) {
	fn, ok := handlers.Lookup(name)
	if !ok {
		msg := fmt.Sprintf("no inline builtin handler registered for tool %q", name)
		return msg, false, msg
	}
	var args json.RawMessage
	if raw := strings.TrimSpace(inv.ArgumentsJSON); raw != "" {
		if !json.Valid([]byte(raw)) {
			msg := fmt.Sprintf("invalid arguments JSON for tool %q", name)
			return msg, false, msg
		}
		args = json.RawMessage(raw)
	} else {
		args = json.RawMessage(`{}`)
	}
	res, err := fn(ctx, args, inv)
	if err != nil {
		return err.Error(), false, err.Error()
	}
	if len(res.Cards) > 0 {
		if sink := handlers.CardSink(); sink != nil && CardSinkFrom(ctx) == nil {
			ctx = WithCardSink(ctx, sink)
		}
		if cerr := ShowCards(ctx, inv, res.Cards); cerr != nil {
			msg := cardFailure(res.Content, cerr)
			return msg, false, cerr.Error()
		}
	}
	if res.EndsTurn {
		TurnControlFrom(ctx).RequestEnd(EndTurnRequest{ToolCallID: inv.ToolCallID, ToolName: name, Source: "inline"})
	}
	return res.Content, true, ""
}
