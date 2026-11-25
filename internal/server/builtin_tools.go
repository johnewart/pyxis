package server

// Inline-builtin tool wiring.
//
// llmd owns the registration seam (toolseam/toolexec.Handlers, the registry,
// dispatch) and the core session tools (see session_tools.go). It owns no
// product tools — those are contributed by the host. It does not own, and must
// not resolve, the per-tool dependencies of anything registered through
// InstallTools' extra registrars — those belong to the module that owns the
// tool. Handlers for those tools close over their own provider at registration
// time and resolve it per invocation from inv.WorkspaceID.
//
// What llmd resolves here, once per invocation, is the workspace binding
// itself (from the calling workspace) so every handler sees it identically.

import (
	"context"
	"fmt"

	"github.com/johnewart/pyxis/llmdlog"

	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/llmdtools"
	"github.com/johnewart/pyxis/toolregistry"
)

// InstallTools is llmdtools.InstallTools, re-exported for call sites inside
// this package. The real thing is PUBLIC — see llmd/llmdtools — because
// contributing tools is something hosts do, and it needs nothing from this
// package to work.
func InstallTools(reg *toolregistry.Registry, handlers *toolexec.Handlers, extra ...llmdtools.RegisterFunc) (*toolexec.Handlers, error) {
	return llmdtools.InstallTools(reg, handlers, extra...)
}

// InstallBuiltinTools is the former name of InstallTools.
//
// Deprecated: there are no builtin tools; the name promised a guarantee
// nothing made. Call llmdtools.InstallTools. Kept for one release.
func InstallBuiltinTools(reg *toolregistry.Registry, handlers *toolexec.Handlers, extra ...llmdtools.RegisterFunc) (*toolexec.Handlers, error) {
	return llmdtools.InstallTools(reg, handlers, extra...)
}

// InstallCoreTools wires a server's tool surface in ONE call: it creates the
// registry and handler set if the caller left them nil, registers llmd's
// inline builtins, then registers the core session tools (see
// internal/coretools) that every agent must have.
//
// This exists so a composition root cannot half-wire an agent. Previously each
// root called InstallTools and RegisterSessionTools separately, and
// forgetting the second one produced agents with no way to set a status, keep
// a todo list, retitle the session or ask the user anything — with no error,
// because registration is additive and an unregistered tool is simply never
// advertised. Anything that builds an LlmServer intended to serve agents calls
// this; extra carries the host's own product-tool registrars, which remain
// optional and deployment-specific.
func InstallCoreTools(s *LlmServer, deps toolexec.SessionToolsDeps, extra ...llmdtools.RegisterFunc) error {
	if s == nil {
		return fmt.Errorf("llm server is required")
	}
	if s.Tools == nil {
		s.Tools = toolregistry.New()
	}
	handlers := s.handlerSet()
	if _, err := InstallTools(s.Tools, handlers, extra...); err != nil {
		return fmt.Errorf("install tools: %w", err)
	}
	if err := toolexec.RegisterSessionTools(s.Tools, handlers, deps); err != nil {
		return fmt.Errorf("register session tools: %w", err)
	}
	return nil
}

// handlerSet returns the server's explicit inline-builtin handler set,
// falling back to a fresh empty set (never toolexec.DefaultHandlers) when a
// caller left Handlers unwired — unknown tools fail closed instead of
// leaking into process-wide state.
func (s *LlmServer) handlerSet() *toolexec.Handlers {
	if s.Handlers == nil {
		s.Handlers = toolexec.NewHandlers()
	}
	// Cards tools return (toolseam.Result.Cards) are delivered by this
	// server, on every dispatch path that uses this set (chat loop, MCP,
	// InvokeTool). A host may install its own sink first.
	if s.Handlers.CardSink() == nil {
		s.Handlers.SetCardSink(s)
	}
	return s.Handlers
}

// HandlerSet is the exported form of handlerSet, for peer packages (e.g.
// mcpserver) that dispatch tools through this server's explicit handler set
// rather than any process-wide global.
func (s *LlmServer) HandlerSet() *toolexec.Handlers {
	return s.handlerSet()
}

// bindWorkspace resolves the workspace this invocation belongs to (once, from
// the calling workspace) and returns the Invocation carrying that binding, so
// every handler sees it identically. Callers MUST pass the RETURNED
// Invocation to RunWithHandlers, not their original one.
//
// This is the whole of what llmd contributes to a tool invocation. A handler
// that needs a per-scope dependency reads inv.Scope and resolves it
// itself, via whatever provider it closed over at registration time. llmd
// deliberately carries no field for such a dependency: see toolseam.Handler.
func (s *LlmServer) bindWorkspace(ctx context.Context, inv toolexec.Invocation) toolexec.Invocation {
	sc, err := s.scope(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"bindWorkspace: no scope on tool invocation: %v", err)
		return inv
	}
	inv.Scope = sc
	return inv
}
