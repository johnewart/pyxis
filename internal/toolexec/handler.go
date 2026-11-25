// Package toolexec's tool-dispatch seam lives in llmd/toolseam so it can be
// shared, without an import cycle, by any module that contributes tools. Every
// name below is an alias: no core call site changes.
package toolexec

import (
	"context"

	"github.com/johnewart/pyxis/toolseam"
)

// Handler executes one inline-builtin tool in-process. Alias of
// toolseam.Handler — see there for the full doc comment.
type Handler = toolseam.Handler

// Result is what an inline-builtin handler produced. Alias of toolseam.Result.
type Result = toolseam.Result

// Handlers is a name→Handler map for inline-builtin tools. Alias of
// toolseam.Handlers.
type Handlers = toolseam.Handlers

// NewHandlers returns an empty handler set.
func NewHandlers() *Handlers {
	return toolseam.NewHandlers()
}

// Deprecated: DefaultHandlers is a process-wide handler set no longer
// consulted by any production dispatch path (RunWithHandlers requires an
// explicit set). It remains only as a deprecated shim pending removal in the
// stage-end cleanup sweep; do not register into it or read from it.
var DefaultHandlers = NewHandlers()

func runInlineBuiltin(ctx context.Context, name string, handlers *Handlers, inv Invocation) (content string, success bool, errMsg string) {
	return toolseam.RunInlineBuiltin(ctx, name, handlers, inv)
}
