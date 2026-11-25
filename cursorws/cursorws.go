// Package cursorws defines the narrow seam core code uses to reach a host's
// Cursor workspace bridge (.cursor/mcp.json and .mdc rendering, and
// diagnostics enablement). MCP base URL discovery lives in mcpbridge; the
// no-op default here simply defers to it. The host provides a concrete
// implementation and installs it via SetDefault at process start.
package cursorws

import (
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/mcpbridge"
)

// CursorWorkspace is the surface the Cursor CLI provider clients need from the
// host's Cursor workspace bridge.
type CursorWorkspace interface {
	// ResolveHTTPBaseURL returns the base URL the Cursor CLI should use to
	// reach llmd's MCP HTTP endpoint.
	ResolveHTTPBaseURL() string
	// DiagEnabled reports whether Cursor workspace diagnostics are enabled.
	DiagEnabled() bool
	// Ensure writes/refreshes the workspace's .cursor/mcp.json and any .mdc
	// files needed for the Cursor CLI to reach llmd's MCP server.
	Ensure(absWorkspace, llmdBin, mcpBaseURL string, mdcFiles map[string]string) error
}

// noop is the nil-safe default used until a real implementation is installed.
// ResolveHTTPBaseURL/DiagEnabled read llmd-owned env vars directly (no
// workspace mutation involved), so callers get correct base-URL discovery even
// before a workspace-writing adapter is installed. Ensure is a true no-op: it
// performs no workspace mutation until a real adapter is installed via
// SetDefault.
type noop struct{}

// ResolveHTTPBaseURL defers to mcpbridge, which owns MCP endpoint discovery.
func (noop) ResolveHTTPBaseURL() string {
	return mcpbridge.BaseURL()
}

func (noop) DiagEnabled() bool {
	return llmdlog.DebugEnabled() || llmdlog.ChatDebugEnabled()
}

func (noop) Ensure(string, string, string, map[string]string) error { return nil }

var current CursorWorkspace = noop{}

// SetDefault installs the process-wide CursorWorkspace implementation. Passing
// nil restores the no-op default.
func SetDefault(cw CursorWorkspace) {
	if cw == nil {
		current = noop{}
		return
	}
	current = cw
}

// Default returns the process-wide CursorWorkspace implementation (never nil).
func Default() CursorWorkspace {
	return current
}
