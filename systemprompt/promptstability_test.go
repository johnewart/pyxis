package systemprompt

import (
	"strings"
	"testing"
)

// The composed system prompt is the prompt-cache prefix AND the basis of llmd's
// stateful-session fingerprint. Any nondeterminism flips the fingerprint and
// silently drops conversation memory on --resume. These tests assert determinism
// by rendering twice, and assert named non-empty sections so they cannot pass on
// an empty prompt.

const (
	markerRole    = "SYNTHETIC-ROLE-MARKER"
	markerToolUse = "SYNTHETIC-TOOLUSE-MARKER"

	// Header lines of llmd's own provider-isolation bodies (see sections.go).
	markerCursorIsolation = "LLMD + Cursor"
	markerClaudeIsolation = "LLMD + Claude Code"
)

func stabilitySections() []Section {
	return []Section{
		{ID: "agent.role", Content: markerRole + " you are helpful."},
		{ID: "conversation.toolUse", Content: markerToolUse + " use tools carefully."},
		{ID: "workspace.isolation", Content: WorkspaceIsolationSection},
	}
}

func countOccurrences(hay, needle string) int { return strings.Count(hay, needle) }

func TestPromptStability_RenderIsDeterministic(t *testing.T) {
	a := RenderSystemPrompt(stabilitySections(), nil)
	b := RenderSystemPrompt(stabilitySections(), nil)

	if a.PromptBody != b.PromptBody {
		t.Fatalf("prompt body not byte-identical across renders:\n%q\n%q", a.PromptBody, b.PromptBody)
	}
	if a.Fingerprint.Combined != b.Fingerprint.Combined {
		t.Fatalf("combined fingerprint drifted: %s vs %s", a.Fingerprint.Combined, b.Fingerprint.Combined)
	}
	if strings.TrimSpace(a.PromptBody) == "" {
		t.Fatal("prompt body is empty — the test would pass on nothing")
	}

	// Each named section must appear exactly once in the logical body.
	for _, marker := range []string{markerRole, markerToolUse, markerCursorIsolation} {
		if n := countOccurrences(a.LogicalBody, marker); n != 1 {
			t.Fatalf("section marker %q appears %d times in logical body, want exactly 1", marker, n)
		}
	}

	// With no overlay every section is prompt-delivered.
	for _, marker := range []string{markerRole, markerToolUse, markerCursorIsolation} {
		if !strings.Contains(a.PromptBody, marker) {
			t.Fatalf("expected %q in prompt body with nil overlay", marker)
		}
	}
}

func TestPromptStability_ProviderIsolationRouting(t *testing.T) {
	base := []Section{
		{ID: "agent.role", Content: markerRole + " you are helpful."},
		{ID: "conversation.toolUse", Content: markerToolUse + " use tools carefully."},
	}

	// Cursor: isolation body lands in the MDC file, not the prompt body.
	cursorSections := EnsureProviderIsolationSection(base, "cursor", "cursor_cli")
	cursor := RenderSystemPrompt(cursorSections, OverlayForProvider("cursor", "cursor_cli"))
	if !strings.Contains(cursor.MDCFiles[WorkspaceMDC], markerCursorIsolation) {
		t.Fatalf("cursor isolation missing from %s: %q", WorkspaceMDC, cursor.MDCFiles[WorkspaceMDC])
	}
	if strings.Contains(cursor.PromptBody, markerCursorIsolation) {
		t.Fatal("cursor isolation must not appear in prompt body")
	}
	if strings.TrimSpace(cursor.PromptBody) == "" {
		t.Fatal("cursor prompt body is empty")
	}

	// Claude: isolation body lands in the Claude append surface, not the prompt body.
	claudeSections := EnsureProviderIsolationSection(base, "claude_v2", "claude_code_v2")
	claude := RenderSystemPrompt(claudeSections, OverlayForProvider("claude_v2", "claude_code_v2"))
	if !strings.Contains(claude.ClaudeAppend, markerClaudeIsolation) {
		t.Fatalf("claude isolation missing from ClaudeAppend: %q", claude.ClaudeAppend)
	}
	if strings.Contains(claude.PromptBody, markerClaudeIsolation) {
		t.Fatal("claude isolation must not appear in prompt body")
	}
	if strings.TrimSpace(claude.PromptBody) == "" {
		t.Fatal("claude prompt body is empty")
	}

	if cursor.Fingerprint.Combined == claude.Fingerprint.Combined {
		t.Fatal("cursor and claude combined fingerprints must differ")
	}
}
