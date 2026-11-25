// Package mcpbridge answers one question for llmd's CLI providers (claude-code,
// cursor-cli): where is llmd's MCP HTTP endpoint (POST /mcp)? Those providers
// spawn an external CLI that reaches llmd's tools over MCP, so without this URL
// a turn that needs tools cannot run.
//
// Resolution order:
//  1. the resolver installed with SetDefault (llmdhost.Host.InstallProcessDefaults
//     installs the Host's own listener, see Host.StartMCP);
//  2. LLMD_MCP_HTTP_BASE_URL;
//  3. LLMD_MCP_HTTP_ADDR, turned into a URL.
//
// It is deliberately independent of cursorws: the Cursor workspace adapter is
// one CONSUMER of this URL (it writes it into .cursor/mcp.json), not its source.
package mcpbridge

import (
	"net"
	"os"
	"strings"
	"sync/atomic"
)

var resolver atomic.Pointer[func() string]

// SetDefault installs the process-wide resolver consulted first by BaseURL.
// It is a function rather than a URL so the publisher may start its listener
// before or after installing it. nil removes it.
func SetDefault(f func() string) {
	if f == nil {
		resolver.Store(nil)
		return
	}
	resolver.Store(&f)
}

// BaseURL returns llmd's MCP HTTP base URL (no trailing slash, no /mcp), or ""
// when no bridge is available.
func BaseURL() string {
	if f := resolver.Load(); f != nil {
		if u := strings.TrimSpace((*f)()); u != "" {
			return strings.TrimSuffix(u, "/")
		}
	}
	if u := strings.TrimSpace(os.Getenv("LLMD_MCP_HTTP_BASE_URL")); u != "" {
		return strings.TrimSuffix(u, "/")
	}
	return BaseURLFromListenAddr(os.Getenv("LLMD_MCP_HTTP_ADDR"))
}

// BaseURLFromListenAddr turns a listen address (":8765", "127.0.0.1:8765",
// "0.0.0.0:8765", "[::]:8765" or a full URL) into a URL a local client can
// dial. Wildcard hosts become 127.0.0.1: the CLIs reaching it run on this
// machine, and a wildcard is a bind address, not a destination.
func BaseURLFromListenAddr(addr string) string {
	a := strings.TrimSuffix(strings.TrimSpace(addr), "/")
	if a == "" {
		return ""
	}
	if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") {
		return a
	}
	host, port, err := net.SplitHostPort(a)
	if err != nil {
		// Not host:port; pass through as before rather than guessing.
		return "http://" + a
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
