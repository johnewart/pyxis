package server

import (
	"testing"
)

// This file pins the invariant that matters about llmd's tool surface: a tool
// llmd ADVERTISES as an inline builtin must have a handler llmd can RUN.
//
// It replaces an earlier test that enumerated specific host-owned tool names
// (traverse_graph, read_work_item, the document family, ...) and asserted their
// absence. That shape was wrong twice over. It required llmd to carry a list of
// names it does not own, which goes stale the moment a host adds one. And it
// could only catch leakage of the names it happened to list — while passing
// vacuously, since the registration path it called advertises nothing at all on
// its own (see InstallTools: "there are no builtin tools").
//
// TestInstallCoreTools_EveryCoreToolHasAHandler covers the same property for the
// known core names. These two cover what that one cannot: they iterate whatever
// is ACTUALLY advertised, so a tool contributed by an extra registrar — or one
// added to core without a handler — is caught without naming it in advance.

// TestToolSurface_EveryAdvertisedInlineBuiltinIsExecutable walks the real
// server-wide surface. An inline builtin with no handler fails mid-turn at
// dispatch with "no inline builtin handler registered"; this catches it at
// wiring time instead, whatever its origin.
func TestToolSurface_EveryAdvertisedInlineBuiltinIsExecutable(t *testing.T) {
	s := newCoreToolsServer(t)

	defs := s.ServerWideToolDefinitions()
	if len(defs) == 0 {
		t.Fatal("server advertised no tools at all — this test would pass on nothing")
	}

	handlers := s.HandlerSet()
	inlineCount := 0
	for _, d := range defs {
		if d.GetInlineBuiltin() == nil {
			// Delegated/proxied/webhook tools execute elsewhere by design and
			// legitimately have no in-process handler.
			continue
		}
		inlineCount++
		if name := d.GetName(); !handlerRegistered(t, s, name) {
			t.Errorf("tool %q is advertised as an inline builtin but has no registered handler; "+
				"a turn calling it would fail at dispatch. Registered handlers: %v",
				name, handlers.Names())
		}
	}
	if inlineCount == 0 {
		t.Fatal("no inline builtins advertised — the handler check was vacuous")
	}
}

// TestToolSurface_NoOrphanHandlers is the other direction: a handler with no
// advertised definition is unreachable code, which in practice means a
// registration whose ToolDefinition was dropped or renamed out from under it.
func TestToolSurface_NoOrphanHandlers(t *testing.T) {
	s := newCoreToolsServer(t)

	advertised := map[string]bool{}
	for _, d := range s.ServerWideToolDefinitions() {
		advertised[d.GetName()] = true
	}

	names := s.HandlerSet().Names()
	if len(names) == 0 {
		t.Fatal("no handlers registered — this test would pass on nothing")
	}
	for _, name := range names {
		if !advertised[name] {
			t.Errorf("handler %q is registered but no tool definition advertises it; "+
				"the model can never call it. Advertised: %v", name, advertised)
		}
	}
}

func handlerRegistered(t *testing.T, s *LlmServer, name string) bool {
	t.Helper()
	_, ok := s.HandlerSet().Lookup(name)
	return ok
}
