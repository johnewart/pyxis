package claudecodeclillm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSummarizeClaudeCLIStdoutLineForDebug_controlResponse(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype": "success",
			"response": map[string]any{
				"commands": []any{map[string]any{"name": "loop"}, map[string]any{"name": "clear"}},
				"agents":   []any{map[string]any{"name": "Explore"}},
				"models":   []any{map[string]any{"value": "haiku"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := summarizeClaudeCLIStdoutLineForDebug(string(raw))
	if strings.Contains(got, "loop") || strings.Contains(got, "Explore") {
		t.Fatalf("expected summarized control_response, got %q", got)
	}
	if !strings.Contains(got, `"commands":2`) || !strings.Contains(got, `"agents":1`) || !strings.Contains(got, `"models":1`) {
		t.Fatalf("expected counts in summary, got %q", got)
	}
}

// system/init is no longer summarized for DEBUG: it is emitted at INFO by
// logKeyStdoutFrame with full tool names. Assert it routes there instead.
func TestLogKeyStdoutFrame_handlesInitAndResult(t *testing.T) {
	t.Parallel()
	log := newCLILog("test")
	initLine, err := json.Marshal(map[string]any{
		"type": "system", "subtype": "init", "session_id": "sess-1",
		"tools": []any{"Read"}, "mcp_servers": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !log.logKeyStdoutFrame(string(initLine)) {
		t.Fatal("expected system/init to be handled as a key frame")
	}
	resultLine, err := json.Marshal(map[string]any{"type": "result", "subtype": "success"})
	if err != nil {
		t.Fatal(err)
	}
	if !log.logKeyStdoutFrame(string(resultLine)) {
		t.Fatal("expected result to be handled as a key frame")
	}
	if log.logKeyStdoutFrame(`{"type":"assistant"}`) {
		t.Fatal("expected assistant frame to fall through to debug summary")
	}
	if log.logKeyStdoutFrame(`not json`) {
		t.Fatal("expected non-JSON to fall through")
	}
}

func TestClaudeInitToolNames_sortedAndTruncated(t *testing.T) {
	t.Parallel()
	if got := claudeInitToolNames([]any{"Write", "Read", "Bash"}); got != "Bash,Read,Write" {
		t.Fatalf("expected sorted names, got %q", got)
	}
	many := make([]any, 0, initToolNameLogLimit+5)
	for i := 0; i < initToolNameLogLimit+5; i++ {
		many = append(many, "tool")
	}
	got := claudeInitToolNames(many)
	if !strings.Contains(got, "…+5 more") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	if got := claudeInitToolNames(nil); got != "" {
		t.Fatalf("expected empty string for nil tools, got %q", got)
	}
}

func TestClaudeInitMCPServers_nameAndStatus(t *testing.T) {
	t.Parallel()
	got := claudeInitMCPServers([]any{
		map[string]any{"name": "llmd", "status": "connected"},
		map[string]any{"name": "other", "status": "failed"},
	})
	if got != "llmd:connected,other:failed" {
		t.Fatalf("expected name:status pairs, got %q", got)
	}
	if got := claudeInitMCPServers([]any{map[string]any{"name": "x"}}); got != "x:unknown" {
		t.Fatalf("expected unknown status fallback, got %q", got)
	}
}

// The argv log must never leak prompt text, but must keep the flags that decide
// the tool inventory verbatim -- that diff is the whole point of the log line.
func TestRedactClaudeCLIArgs_redactsPromptsKeepsToolFlags(t *testing.T) {
	t.Parallel()
	args := []string{
		"--system-prompt", "SECRET SYSTEM TEXT",
		"--append-system-prompt", "SECRET APPENDED",
		"--settings", `{"thinking":{"type":"disabled"}}`,
		"--tools", "WaitForMcpServers",
		"--strict-mcp-config",
		"--setting-sources", "",
		"--allowed-tools", "mcp__llmd__write_code",
	}
	got := strings.Join(redactClaudeCLIArgs(args), " ")
	for _, leak := range []string{"SECRET SYSTEM TEXT", "SECRET APPENDED", "thinking"} {
		if strings.Contains(got, leak) {
			t.Fatalf("expected %q redacted, got %q", leak, got)
		}
	}
	if !strings.Contains(got, "<18 chars redacted>") {
		t.Fatalf("expected redaction length marker, got %q", got)
	}
	for _, keep := range []string{"--tools", "WaitForMcpServers", "--strict-mcp-config", "--setting-sources", "mcp__llmd__write_code"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("expected %q kept verbatim, got %q", keep, got)
		}
	}
}

// A redacted flag in final position must not panic or swallow a missing value.
func TestRedactClaudeCLIArgs_trailingFlagWithoutValue(t *testing.T) {
	t.Parallel()
	got := strings.Join(redactClaudeCLIArgs([]string{"--model", "sonnet", "--system-prompt"}), " ")
	if !strings.Contains(got, "--system-prompt") || strings.Contains(got, "redacted") {
		t.Fatalf("expected bare trailing flag, got %q", got)
	}
}

func TestFormatClaudeTokenUsage(t *testing.T) {
	t.Parallel()
	if got := formatClaudeTokenUsage(nil); got != "usage=none" {
		t.Fatalf("expected usage=none, got %q", got)
	}
	raw := map[string]any{
		"usage": map[string]any{
			"input_tokens":                float64(10),
			"output_tokens":               float64(20),
			"cache_read_input_tokens":     float64(30),
			"cache_creation_input_tokens": float64(40),
		},
		"total_cost_usd": float64(0.1234),
	}
	got := formatClaudeTokenUsage(tokenUsageFromClaudeResultMap(raw))
	for _, want := range []string{"in=10", "out=20", "cache_read=30", "cache_write=40", "cost_usd=0.1234"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestNewCLILog_spawnIDIsUnique(t *testing.T) {
	t.Parallel()
	a := newCLILog("test")
	if len(a.spawn) != 8 {
		t.Fatalf("expected 8-char spawn id, got %q", a.spawn)
	}
	if a.module != "test" {
		t.Fatalf("expected module preserved, got %q", a.module)
	}
	// Distinct spawns must not collide, or the grep join key is worthless.
	seen := map[string]bool{a.spawn: true}
	for i := 0; i < 100; i++ {
		s := newCLILog("test").spawn
		if seen[s] {
			t.Fatalf("duplicate spawn id %q", s)
		}
		seen[s] = true
	}
}
