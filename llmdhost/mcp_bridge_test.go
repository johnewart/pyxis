package llmdhost

import (
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/johnewart/pyxis/mcpbridge"
)

// An embedded Host serves its own MCP bridge: StartMCP on a caller-owned
// listener, published to the CLI providers via mcpbridge once
// InstallProcessDefaults has run (in either order), stopped by Close.
func TestStartMCPPublishesBridgeAndCloseStopsIt(t *testing.T) {
	t.Setenv("LLMD_MCP_HTTP_BASE_URL", "")
	t.Setenv("LLMD_MCP_HTTP_ADDR", "")
	t.Cleanup(func() { mcpbridge.SetDefault(nil) })

	dir := t.TempDir()
	h, err := New(Config{
		DatabaseDSN:         filepath.Join(dir, "llmd.sqlite"),
		KnowledgeSQLitePath: filepath.Join(dir, "knowledge.sqlite"),
		RuntimeIdleEvict:    -1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	// Installed BEFORE the listener exists: providers must still find it later.
	h.InstallProcessDefaults()
	if got := mcpbridge.BaseURL(); got != "" {
		t.Fatalf("before StartMCP: mcpbridge.BaseURL() = %q, want empty", got)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := h.StartMCP(lis); err != nil {
		t.Fatalf("StartMCP: %v", err)
	}
	if err := h.StartMCP(lis); err == nil {
		t.Fatal("second StartMCP: want error")
	}

	base := mcpbridge.BaseURL()
	if base == "" || base != h.MCPBaseURL() {
		t.Fatalf("mcpbridge.BaseURL() = %q, Host.MCPBaseURL() = %q; want equal and non-empty", base, h.MCPBaseURL())
	}
	resp, err := http.Get(base + "/mcp/health")
	if err != nil {
		t.Fatalf("GET /mcp/health: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /mcp/health: status %d", resp.StatusCode)
	}

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := h.MCPBaseURL(); got != "" {
		t.Fatalf("after Close: MCPBaseURL() = %q, want empty", got)
	}
	if _, err := http.Get(base + "/mcp/health"); err == nil {
		t.Fatal("after Close: MCP bridge still answering")
	}
}
