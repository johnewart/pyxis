package cursorws

import "testing"

func TestNoopDefault(t *testing.T) {
	t.Setenv("LLMD_MCP_HTTP_BASE_URL", "")
	t.Setenv("LLMD_MCP_HTTP_ADDR", "")
	t.Setenv("LLMD_DEBUG", "")
	t.Setenv("LLMD_DEBUG_CHAT", "")
	t.Setenv("LLMD_DEBUG_CHAT_FILE", "")
	SetDefault(nil)
	cw := Default()
	if got := cw.ResolveHTTPBaseURL(); got != "" {
		t.Fatalf("ResolveHTTPBaseURL() = %q, want empty", got)
	}
	if cw.DiagEnabled() {
		t.Fatalf("DiagEnabled() = true, want false")
	}
	if err := cw.Ensure("/tmp/ws", "/usr/local/bin/llmd", "http://localhost:1234", nil); err != nil {
		t.Fatalf("Ensure() = %v, want nil", err)
	}
}
