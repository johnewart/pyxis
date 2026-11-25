// Package llmdtools is how a host contributes tools to llmd.
//
// It sits beside toolregistry and toolseam and completes that trio: toolseam
// defines what a tool IS (a Handler over an Invocation), toolregistry holds
// what is ADVERTISED, and llmdtools is the entry point that installs a host's
// registrars into both at once.
//
// The direction is deliberate and must not be reversed: llmd never names a
// consumer's package. A host passes RegisterFunc values that close over
// whatever their tools need — workspace file access, a dependency graph, a
// work tracker, an embedding index — and llmd calls them without knowing what
// any of those things are. That is why there is no capability type anywhere in
// this package or in toolseam.
//
// Registrars run in the process that embeds llmd (llmdhost.Config.Tools) and
// their handlers execute in that process. To serve tools from another process or Go
// module, use package toolexecutor and the executor protocol on
// ToolProviderService.Connect instead; the same toolseam.Handlers can back
// either path.
package llmdtools

import (
	"github.com/johnewart/pyxis/toolregistry"
	"github.com/johnewart/pyxis/toolseam"
)

// RegisterFunc installs one subsystem's tools: their definitions into reg (so
// they are advertised) and their handlers into handlers (so they dispatch).
//
// Registration is ADDITIVE and the two halves are the caller's to keep in
// step: a definition with no handler is advertised but fails to execute, and a
// handler with no definition is never offered to a model. Register both.
//
// A RegisterFunc is the seam's dependency-injection point. Close over your
// providers here and resolve them per call from Invocation.Scope:
//
//	func Register(files *myFileClient) llmdtools.RegisterFunc {
//	    return func(reg *toolregistry.Registry, h *toolseam.Handlers) error {
//	        return h.Register("my_tool", func(ctx context.Context, args json.RawMessage, inv toolseam.Invocation) (toolseam.Result, error) {
//	            return run(ctx, args, files.For(inv.Scope))
//	        })
//	    }
//	}
type RegisterFunc func(*toolregistry.Registry, *toolseam.Handlers) error

// InstallTools runs each registrar in order, installing its tools into reg and
// its handlers into handlers. Nil registrars are skipped. The first error stops
// installation and is returned; reg and handlers keep whatever earlier
// registrars already added, because registration is additive and partial
// installation is visible rather than rolled back.
//
// llmd contributes NO tools of its own here. Every tool in an assembled
// surface comes from a registrar the host supplies. (llmd's core session tools
// — add_todo, set_status, ask_user and friends — are registered separately,
// with their own dependencies, as part of standing up a server.)
//
// Returns handlers unchanged, for chaining at the call site.
func InstallTools(reg *toolregistry.Registry, handlers *toolseam.Handlers, registrars ...RegisterFunc) (*toolseam.Handlers, error) {
	for _, register := range registrars {
		if register == nil {
			continue
		}
		if err := register(reg, handlers); err != nil {
			return handlers, err
		}
	}
	return handlers, nil
}
