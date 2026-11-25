package mcpbridge

import "testing"

func TestBaseURLResolutionOrder(t *testing.T) {
	t.Cleanup(func() { SetDefault(nil) })
	t.Setenv("LLMD_MCP_HTTP_BASE_URL", "")
	t.Setenv("LLMD_MCP_HTTP_ADDR", "")
	SetDefault(nil)
	if got := BaseURL(); got != "" {
		t.Fatalf("nothing configured: BaseURL() = %q, want empty", got)
	}

	t.Setenv("LLMD_MCP_HTTP_ADDR", ":8765")
	if got := BaseURL(); got != "http://127.0.0.1:8765" {
		t.Fatalf("addr only: BaseURL() = %q", got)
	}

	t.Setenv("LLMD_MCP_HTTP_BASE_URL", "http://bridge:9000/")
	if got := BaseURL(); got != "http://bridge:9000" {
		t.Fatalf("base url wins over addr: BaseURL() = %q", got)
	}

	SetDefault(func() string { return "http://127.0.0.1:4242" })
	if got := BaseURL(); got != "http://127.0.0.1:4242" {
		t.Fatalf("resolver wins over env: BaseURL() = %q", got)
	}

	// A resolver with nothing to report (listener not started yet) falls through.
	SetDefault(func() string { return "" })
	if got := BaseURL(); got != "http://bridge:9000" {
		t.Fatalf("empty resolver falls through: BaseURL() = %q", got)
	}
}

func TestBaseURLFromListenAddr(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		":8765":             "http://127.0.0.1:8765",
		"127.0.0.1:8765":    "http://127.0.0.1:8765",
		"0.0.0.0:8765":      "http://127.0.0.1:8765",
		"[::]:8765":         "http://127.0.0.1:8765",
		"localhost:8765/":   "http://localhost:8765",
		"http://x:1/":       "http://x:1",
		"https://x.example": "https://x.example",
	}
	for in, want := range cases {
		if got := BaseURLFromListenAddr(in); got != want {
			t.Errorf("BaseURLFromListenAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
