package llmdhost

import (
	"context"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/tenantscope"
)

// SessionAPI is the supported way to drive llmd's sessions from inside the host
// process, for the things a host does OUT OF BAND of a chat turn: standing a
// session up before the first turn, rebinding it to a different model, tearing
// it down, and managing runtime occupancy.
//
// Every argument and return here is either a protobuf type from gen/llmv1 or a
// Go primitive. That is deliberate: the session runtime, its hub and its history
// store are llmd internals whose shapes change, and handing them out would make
// every one of them a compatibility promise. What a host needs is the ability to
// ACT on a session, which is what this is.
//
// Reading conversation history is NOT here. History is served by the
// llm.v1 RPCs a host already gets from Host.Register, so it has one
// implementation and one authorization path rather than a second, in-process
// back door that would quietly skip the checks the RPC layer applies.
//
// Every per-session method takes an explicit tenantscope.Scope (tenant plus
// optional scope id) rather than reading one from ctx: these calls happen out
// of band of any RPC, so there is usually no incoming metadata to read, and a
// hidden context dependency would fail silently instead of at compile time.
// Session ids are only unique within a Scope.
type SessionAPI interface {
	// Ensure creates or updates a session from the same request shape the
	// EnsureSession RPC takes, so in-process and over-the-wire callers cannot
	// drift in what "ensure" means. sc scopes it.
	Ensure(ctx context.Context, req *llmv1.EnsureSessionRequest, sc tenantscope.Scope) error

	// EnsureLoaded makes sure the session's in-memory runtime exists, restoring
	// it from the database when it was evicted. A host does not normally need
	// this — chat paths load on demand — but it is required before acting on a
	// runtime that may have gone idle.
	EnsureLoaded(ctx context.Context, sc tenantscope.Scope, sessionID string) error

	// Rebind points an existing session at a different provider and/or model.
	// Nil leaves that half unchanged, so a caller can change the model without
	// restating the provider. This is the supported form of what would
	// otherwise be an in-place edit of the session row.
	Rebind(ctx context.Context, sc tenantscope.Scope, sessionID string, providerID, model *string) error

	// Destroy removes a session and its runtime. It reports whether a session
	// was actually removed, so "already gone" is distinguishable from "removed
	// now" without a second lookup.
	Destroy(ctx context.Context, sc tenantscope.Scope, sessionID string) bool

	// Loaded reports whether a session's runtime is currently in memory under
	// sc. It is
	// occupancy, NOT existence: a session absent here may still exist in the
	// database and load on next use. Do not use it to decide whether a session
	// exists.
	Loaded(sc tenantscope.Scope, sessionID string) bool

	// Count is the number of runtimes currently loaded.
	Count() int

	// EvictIdle unloads runtimes idle for longer than maxIdle and returns their
	// ids. Runtimes are a cache over database state, so this frees memory
	// without losing anything. Zero or negative evicts nothing.
	EvictIdle(maxIdle time.Duration) []string
}

// Sessions returns this Host's session API.
func (h *Host) Sessions() SessionAPI { return sessionAPI{h: h} }

// sessionAPI adapts the internal session manager. It is a thin pass-through by
// design: any behaviour added here would be behaviour the equivalent RPC does
// not have, and the two drifting is exactly what this facade exists to prevent.
type sessionAPI struct{ h *Host }

func (s sessionAPI) Ensure(ctx context.Context, req *llmv1.EnsureSessionRequest, sc tenantscope.Scope) error {
	return s.h.runtimes.EnsureSession(ctx, req, sc)
}

func (s sessionAPI) EnsureLoaded(ctx context.Context, sc tenantscope.Scope, sessionID string) error {
	return s.h.runtimes.EnsureLoaded(ctx, sc, sessionID)
}

func (s sessionAPI) Rebind(ctx context.Context, sc tenantscope.Scope, sessionID string, providerID, model *string) error {
	return s.h.runtimes.UpdateLlm(ctx, sc, sessionID, providerID, model)
}

func (s sessionAPI) Destroy(ctx context.Context, sc tenantscope.Scope, sessionID string) bool {
	return s.h.runtimes.Destroy(ctx, sc, sessionID)
}

func (s sessionAPI) Loaded(sc tenantscope.Scope, sessionID string) bool {
	_, ok := s.h.runtimes.Get(sc, sessionID)
	return ok
}

func (s sessionAPI) Count() int { return s.h.runtimes.RuntimeCount() }

func (s sessionAPI) EvictIdle(maxIdle time.Duration) []string {
	return s.h.runtimes.EvictIdle(maxIdle)
}
