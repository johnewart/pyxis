package server

import (
	"context"
	"strings"
	"sync"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// noToolsTurns tracks sessions whose CURRENT turn runs with
// TurnOptions.disable_tools. It lives on the server rather than in the turn's
// context because tools reach llmd by two routes: the ChatStream tool loop
// (API providers) and the MCP HTTP server (Claude Code / Cursor, which call
// back into llmd from a separate process with no access to the turn ctx).
// Both consult this, so "no tools" holds whichever route the model takes.
type noToolsTurns struct {
	mu sync.Mutex
	// key → number of active no-tools turns (turns are serialized per session
	// by the turn gate, so this is 0 or 1 in practice; a count keeps begin/end
	// pairs safe regardless).
	active map[string]int
}

func noToolsKey(sc serverutil.Scope, sessionID string) string {
	return sc.TenantID + "\x00" + sc.ScopeID + "\x00" + strings.TrimSpace(sessionID)
}

func (s *LlmServer) noToolsRegistry() *noToolsTurns {
	s.noToolsOnce.Do(func() { s.noTools = &noToolsTurns{active: map[string]int{}} })
	return s.noTools
}

// beginNoToolsTurn marks sessionID's current turn as tools-disabled until the
// returned func is called (once, when the turn ends).
func (s *LlmServer) beginNoToolsTurn(sc serverutil.Scope, sessionID string) func() {
	reg := s.noToolsRegistry()
	key := noToolsKey(sc, sessionID)
	reg.mu.Lock()
	reg.active[key]++
	reg.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			reg.mu.Lock()
			if reg.active[key] <= 1 {
				delete(reg.active, key)
			} else {
				reg.active[key]--
			}
			reg.mu.Unlock()
		})
	}
}

// BeginNoToolsTurn is beginNoToolsTurn for callers outside this package (tests
// of other surfaces, hosts running turns of their own). Call the returned func
// when the turn ends.
func (s *LlmServer) BeginNoToolsTurn(sc serverutil.Scope, sessionID string) func() {
	return s.beginNoToolsTurn(sc, sessionID)
}

// ToolsDisabledForTurn reports whether sessionID's running turn was started
// with TurnOptions.disable_tools. Hosts and the MCP surface use it to offer no
// tools and refuse every call for the rest of that turn.
func (s *LlmServer) ToolsDisabledForTurn(sc serverutil.Scope, sessionID string) bool {
	if s == nil {
		return false
	}
	reg := s.noToolsRegistry()
	reg.mu.Lock()
	defer reg.mu.Unlock()
	return reg.active[noToolsKey(sc, sessionID)] > 0
}

// toolsDisabledForTurnCtx is ToolsDisabledForTurn with the scope taken from ctx.
func (s *LlmServer) toolsDisabledForTurnCtx(ctx context.Context, sessionID string) bool {
	sc, err := s.scope(ctx)
	if err != nil {
		return false
	}
	return s.ToolsDisabledForTurn(sc, sessionID)
}

// NoToolsTurnRefusal is the tool result returned for any tool call made during
// a no-tools turn.
const NoToolsTurnRefusal = "Tools are disabled for this turn. Reply in text only; do not call tools."
