package claudecodeclillm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func initLineWithTools(t *testing.T, tools []string, servers []any) []byte {
	t.Helper()
	payload := map[string]any{
		"type":        "system",
		"subtype":     "init",
		"session_id":  "sess-inventory",
		"mcp_servers": servers,
	}
	if tools != nil {
		payload["tools"] = tools
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseClaudeInitFrame_missingToolsIsEmptyNotUnparsed(t *testing.T) {
	t.Parallel()
	// A tools-less init is precisely the failure the gate catches; it must parse as an
	// init frame with zero tools, not be dismissed as "not an init frame".
	frame, ok := parseClaudeInitFrame(initLineWithTools(t, nil, nil))
	if !ok {
		t.Fatal("system/init without tools must still parse as an init frame")
	}
	if len(frame.toolNames) != 0 {
		t.Fatalf("tools: got %v want empty", frame.toolNames)
	}
}

func TestParseClaudeInitFrame_ignoresNonInit(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		`{"type":"system","subtype":"status"}`,
		`{"type":"result","subtype":"success"}`,
		`not json`,
	} {
		if _, ok := parseClaudeInitFrame([]byte(line)); ok {
			t.Fatalf("line must not parse as init: %s", line)
		}
	}
}

func TestClaudeInventoryUsable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		tools    []string
		deferred bool
		want     bool
	}{
		{"mcp tools present", []string{"WaitForMcpServers", "mcp__llmd__write_code"}, false, true},
		{"only builtins is the bug", []string{"WaitForMcpServers"}, false, false},
		{"empty inventory", nil, false, false},
		{"deferred needs ToolSearch", []string{"WaitForMcpServers", "ToolSearch"}, true, true},
		{"deferred without ToolSearch is unreachable", []string{"WaitForMcpServers"}, true, false},
		{"native tools do not count", []string{"Bash", "Read"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeInventoryUsable(tc.tools, tc.deferred); got != tc.want {
				t.Fatalf("claudeInventoryUsable(%v, %t) = %t want %t", tc.tools, tc.deferred, got, tc.want)
			}
		})
	}
}

func TestVerifyClaudeMCPInventory_emptyInventoryFails(t *testing.T) {
	t.Parallel()
	ch := make(chan streamJSONLine, 1)
	ch <- streamJSONLine{line: initLineWithTools(t, []string{"WaitForMcpServers"},
		[]any{map[string]any{"name": "llmd", "status": "failed"}})}
	close(ch)

	err := verifyClaudeMCPInventory(context.Background(), newStreamJSONLineSource(ch),
		func() string { return "" }, make(chan struct{}), false, newCLILog("test"))
	if !errors.Is(err, errClaudeMCPInventoryEmpty) {
		t.Fatalf("got %v want errClaudeMCPInventoryEmpty", err)
	}
	// The error must carry the diagnosis, not just a failure.
	if !strings.Contains(err.Error(), "llmd:failed") {
		t.Fatalf("error should name the failed MCP server, got: %v", err)
	}
}

func TestVerifyClaudeMCPInventory_goodInventoryPassesAndPreservesStream(t *testing.T) {
	t.Parallel()
	ch := make(chan streamJSONLine, 3)
	ch <- streamJSONLine{line: []byte(`{"type":"system","subtype":"status","status":"requesting"}`)}
	ch <- streamJSONLine{line: initLineWithTools(t, []string{"WaitForMcpServers", "mcp__llmd__write_code"}, nil)}
	ch <- streamJSONLine{line: []byte(`{"type":"result","subtype":"success"}`)}
	close(ch)

	src := newStreamJSONLineSource(ch)
	var captured string
	ctx := WithBackendSessionSink(context.Background(), func(id string) { captured = id })
	if err := verifyClaudeMCPInventory(ctx, src, func() string { return "" },
		make(chan struct{}), false, newCLILog("test")); err != nil {
		t.Fatalf("verifyClaudeMCPInventory: %v", err)
	}
	if captured != "sess-inventory" {
		t.Fatalf("session_id sink: got %q want sess-inventory", captured)
	}
	// Lines the gate walked past must still reach the stream consumer.
	sl, err := src.read(context.Background())
	if err != nil {
		t.Fatalf("read buffered line: %v", err)
	}
	if !strings.Contains(string(sl.line), "requesting") {
		t.Fatalf("expected buffered status line, got %s", sl.line)
	}
	sl, err = src.read(context.Background())
	if err != nil {
		t.Fatalf("read result line: %v", err)
	}
	if !strings.Contains(string(sl.line), "result") {
		t.Fatalf("expected result line, got %s", sl.line)
	}
}

func TestVerifyClaudeMCPInventory_deferredSchemasAcceptToolSearch(t *testing.T) {
	t.Parallel()
	ch := make(chan streamJSONLine, 1)
	ch <- streamJSONLine{line: initLineWithTools(t, []string{"WaitForMcpServers", "ToolSearch"}, nil)}
	close(ch)

	// Token Saver defers MCP schemas, so mcp__llmd__* legitimately absent at init.
	if err := verifyClaudeMCPInventory(context.Background(), newStreamJSONLineSource(ch),
		func() string { return "" }, make(chan struct{}), true, newCLILog("test")); err != nil {
		t.Fatalf("deferred schemas with ToolSearch must pass: %v", err)
	}
}

func TestVerifyClaudeMCPInventory_streamEndsBeforeInit(t *testing.T) {
	t.Parallel()
	ch := make(chan streamJSONLine)
	close(ch)

	err := verifyClaudeMCPInventory(context.Background(), newStreamJSONLineSource(ch),
		func() string { return "boom" }, make(chan struct{}), false, newCLILog("test"))
	if err == nil {
		t.Fatal("stdout closing before system/init must surface an error")
	}
	if errors.Is(err, errClaudeMCPInventoryEmpty) {
		t.Fatalf("closed stdout is not an empty-inventory verdict: %v", err)
	}
}
