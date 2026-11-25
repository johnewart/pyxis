// Package agentpid maps Cursor agent OS process IDs to llmd chat scope for MCP stdio bridge lookup.
package agentpid

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
)

const defaultTTL = 45 * time.Minute

type ctxKey struct{}

// Run is chat session for one spawned Cursor CLI process.
// SessionID is the primary routing key for chat.
type Run struct {
	SessionID string
	// Scope is llmd's own tenant/scope (the same value carried in scope gRPC
	// metadata / HTTP headers everywhere else). Not to be confused with
	// ClientWorkspaceID below, which is the host's own concept.
	Scope serverutil.Scope
	// AgentID is retained while the session manager and MCP stdio bridge still
	// route by this identity. It is slated for removal once hubs are
	// re-keyed to session id (workspace+session consolidation, Option A
	// follow-up) — chat is already session-native; agent_id is not resolved
	// separately from session_id today.
	AgentID string
	// ClientWorkspaceID is the CLIENT's own workspace identifier, when llmd is
	// driven by a host that has its own notion of a workspace. The MCP stdio
	// bridge sets X-Client-Workspace-Id on POST /mcp so delegate_to_client tools
	// can reach back into that client via LLMD_CLIENT_TOOL_HTTP_URL without the
	// agent process passing a workspace header. Entirely unrelated to
	// Scope above, which is llmd's own tenancy key.
	ClientWorkspaceID string
	// ReadOnly is true when the active ChatStream turn forbids mutation tools.
	ReadOnly bool
}

// WithRunDetails attaches a full Run (including optional Scope) to ctx for PID registration.
func WithRunDetails(ctx context.Context, run Run) context.Context {
	if strings.TrimSpace(run.SessionID) == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, run)
}

// FromContext returns run data if ctx carries a session id.
func FromContext(ctx context.Context) (Run, bool) {
	if ctx == nil {
		return Run{}, false
	}
	v, ok := ctx.Value(ctxKey{}).(Run)
	if !ok {
		return Run{}, false
	}
	if strings.TrimSpace(v.SessionID) != "" {
		return v, true
	}
	return Run{}, false
}

type entry struct {
	run Run
	at  time.Time
}

// Registry is a PID → run map with TTL (Cursor MCP bridge phones home with parent PID).
type Registry struct {
	mu   sync.RWMutex
	byPP map[int]entry
	ttl  time.Duration
}

// NewRegistry constructs an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		byPP: make(map[int]entry),
		ttl:  defaultTTL,
	}
}

// Register records agentPID as the Cursor CLI child for this chat scope.
func (r *Registry) Register(agentPID int, run Run) {
	if r == nil || agentPID <= 0 {
		return
	}
	if strings.TrimSpace(run.SessionID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byPP[agentPID] = entry{run: run, at: time.Now()}
}

// Lookup returns scope for an agent PID (stdio bridge parent is the Cursor agent).
func (r *Registry) Lookup(agentPID int) (Run, bool) {
	if r == nil || agentPID <= 0 {
		return Run{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for pid, e := range r.byPP {
		if now.Sub(e.at) > r.ttl {
			delete(r.byPP, pid)
		}
	}
	e, ok := r.byPP[agentPID]
	if !ok || now.Sub(e.at) > r.ttl {
		return Run{}, false
	}
	return e.run, true
}

var defaultReg *Registry

// SetDefaultRegistry is called from cmd/llmd before loading providers so all cursor backends share one map.
func SetDefaultRegistry(reg *Registry) {
	defaultReg = reg
}

// DefaultRegistry returns the process-wide registry (may be nil in tests).
func DefaultRegistry() *Registry {
	return defaultReg
}
