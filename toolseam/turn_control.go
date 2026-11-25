package toolseam

import (
	"context"
	"sync"
)

// EndTurnRequest records one tool's request to end the current chat turn.
type EndTurnRequest struct {
	ToolCallID string
	ToolName   string
	// Source names the dispatch path that observed the request
	// ("inline", "executor", "provider", "client").
	Source string
}

// TurnControl collects end-turn requests from the tool calls of one agent-loop
// round. The loop installs one per round with WithTurnControl, runs every tool
// call in the round, then checks EndRequested: if any successful tool asked to
// end the turn, the loop stops instead of querying the model again.
//
// Only dispatch code calls RequestEnd. Handlers ask by returning
// Result{EndsTurn: true}; executors and clients by setting ends_turn on their
// result message. Failed results never end the turn. Safe for concurrent use.
type TurnControl struct {
	mu       sync.Mutex
	requests []EndTurnRequest
}

// RequestEnd records an end-turn request. Safe on a nil receiver (no-op), so
// dispatch paths with no installed TurnControl need no special-casing.
func (t *TurnControl) RequestEnd(req EndTurnRequest) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.requests = append(t.requests, req)
	t.mu.Unlock()
}

// EndRequested reports whether any tool this round asked to end the turn.
func (t *TurnControl) EndRequested() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.requests) > 0
}

// Requests returns a copy of the recorded end-turn requests, in arrival order.
func (t *TurnControl) Requests() []EndTurnRequest {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]EndTurnRequest(nil), t.requests...)
}

// turnControlKey is the unexported context key for TurnControl; the only way
// to set or read it is WithTurnControl / TurnControlFrom.
type turnControlKey struct{}

// WithTurnControl attaches tc to ctx for one agent-loop round. nil tc ⇒ ctx unchanged.
func WithTurnControl(ctx context.Context, tc *TurnControl) context.Context {
	if tc == nil {
		return ctx
	}
	return context.WithValue(ctx, turnControlKey{}, tc)
}

// TurnControlFrom returns the TurnControl on ctx, or nil when none is
// installed (MCP, InvokeTool, and any path outside the chat loop). The nil
// result is still safe to call RequestEnd on.
func TurnControlFrom(ctx context.Context) *TurnControl {
	tc, _ := ctx.Value(turnControlKey{}).(*TurnControl)
	return tc
}
