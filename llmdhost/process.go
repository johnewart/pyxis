package llmdhost

import (
	"github.com/johnewart/pyxis/internal/mcpserver"
	"github.com/johnewart/pyxis/internal/mcpstdio"
	"github.com/johnewart/pyxis/internal/serve"
)

// SessionEventsWSPath is the path the session-events WebSocket is served on by
// the handler from Host.BrowserSurface.
//
// It is matched by path SUFFIX, so a dev-server proxy prefix (for example
// /host-connect/ws/session-events) routes here too. A host that logs or
// proxies its routes needs the real value rather than a hand-copied string that
// can drift.
const SessionEventsWSPath = serve.SessionEventsWSPath

// MCPLogModule is the log-module label llmd's MCP HTTP surface logs under, so a
// host's startup banner for that listener matches the lines llmd itself emits.
const MCPLogModule = mcpserver.LogModule

// DebugEnabled reports whether llmd's debug switches are on (LLMD_DEBUG and
// friends). A host uses it to decide whether to bind its own diagnostics
// surface; llmd reads those variables for its internal behaviour regardless.
func DebugEnabled() bool { return serve.DebugEnabled() }

// RunMCPStdioBridge runs llmd's MCP stdio bridge in the current process and
// blocks until it finishes. args are the bridge's own arguments, i.e. os.Args
// with the program name and subcommand removed.
//
// The bridge is a stdio<->HTTP shim an agent CLI spawns to reach llmd's MCP
// endpoint. It is a whole mode of the process rather than something a Host
// hosts, which is why it is a package function and needs no Config: it talks to
// an ALREADY RUNNING llmd over HTTP and opens no database of its own.
func RunMCPStdioBridge(args []string) error { return mcpstdio.Run(args) }
