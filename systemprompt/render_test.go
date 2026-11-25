package systemprompt

import (
	"strings"
	"testing"
)

func TestRenderSystemPrompt_CursorOverlayOmitsMDCFromPrompt(t *testing.T) {
	// conversation.toolUse is a client-owned shared body delivered as a structured
	// section; use a synthetic marker so this test exercises overlay routing, not llmd content.
	sections := []Section{
		{ID: "agent.role", Content: "You are helpful."},
		{ID: "conversation.toolUse", Content: "SYNTHETIC-TOOLUSE-MARKER body"},
		{ID: "workspace.isolation", Content: WorkspaceIsolationSection},
	}
	got := RenderSystemPrompt(sections, CursorOverlay)
	if strings.Contains(got.PromptBody, "SYNTHETIC-TOOLUSE-MARKER") {
		t.Fatal("prompt body must not contain MDC-routed toolUse section")
	}
	if strings.Contains(got.PromptBody, markerCursorIsolation) {
		t.Fatal("prompt body must not contain MDC-routed isolation section")
	}
	if got.MDCFiles[WorkspaceMDC] == "" {
		t.Fatal("expected consolidated MDC file")
	}
	if got.Fingerprint.Prompt == got.Fingerprint.Combined {
		t.Fatal("combined fingerprint should differ when MDC content exists")
	}
	if got.Fingerprint.MDC == hashText("") {
		t.Fatal("expected non-empty MDC fingerprint")
	}
}

func TestDefaultCursorWorkspaceMDCBody_HasFrontMatter(t *testing.T) {
	body := DefaultCursorWorkspaceMDCBody()
	if !strings.HasPrefix(body, "---\nalwaysApply: true") {
		t.Fatalf("missing MDC front matter: %q", body[:min(40, len(body))])
	}
	// Fallback body is provider-specific only; shared toolUse content is not owned by llmd.
	if strings.Contains(body, "TOOLS AND WORKSPACE (NON-NEGOTIABLE)") {
		t.Fatal("fallback MDC must not contain client-owned shared toolUse content")
	}
	if !strings.Contains(body, markerCursorIsolation) {
		t.Fatal("missing provider-specific isolation content")
	}
}

func TestRenderSystemPrompt_ClaudeOverlayOmitsAppendFromPrompt(t *testing.T) {
	// conversation.toolUse is a client-owned shared body; use a synthetic marker.
	sections := []Section{
		{ID: "agent.role", Content: "You are helpful."},
		{ID: "conversation.toolUse", Content: "SYNTHETIC-TOOLUSE-MARKER body"},
		{ID: "workspace.isolation", Content: ClaudeIsolationSection},
	}
	got := RenderSystemPrompt(sections, ClaudeOverlay)
	if strings.Contains(got.PromptBody, "SYNTHETIC-TOOLUSE-MARKER") {
		t.Fatal("prompt body must not contain Claude-append toolUse section")
	}
	if strings.Contains(got.PromptBody, markerClaudeIsolation) {
		t.Fatal("prompt body must not contain Claude-append isolation section")
	}
	if got.ClaudeAppend == "" {
		t.Fatal("expected Claude append body")
	}
	if !strings.Contains(got.ClaudeAppend, "SYNTHETIC-TOOLUSE-MARKER") {
		t.Fatal("Claude append must include toolUse")
	}
	if got.Fingerprint.ClaudeAppend == hashText("") {
		t.Fatal("expected non-empty Claude append fingerprint")
	}
}

func TestEnsureProviderIsolationSection(t *testing.T) {
	// Claude: client-sent isolation content is overridden with llmd's owned body.
	claude := EnsureProviderIsolationSection(
		[]Section{{ID: "workspace.isolation", Content: "client-authored junk"}},
		"claude_v2", "claude_code_v2",
	)
	if got := claude[0].Content; got != ClaudeIsolationSection {
		t.Fatalf("claude isolation not overridden with owned body: %q", got)
	}

	// Cursor: inserted when the client sends none.
	cursor := EnsureProviderIsolationSection(nil, "cursor", "cursor_cli")
	if len(cursor) != 1 || cursor[0].Content != WorkspaceIsolationSection {
		t.Fatalf("cursor isolation not inserted: %+v", cursor)
	}

	// Non-isolation provider: any client-sent isolation section is dropped.
	openai := EnsureProviderIsolationSection(
		[]Section{{ID: "agent.role", Content: "role"}, {ID: "workspace.isolation", Content: "leak"}},
		"openai", "openai",
	)
	if len(openai) != 1 || openai[0].ID != "agent.role" {
		t.Fatalf("expected isolation dropped for non-isolation provider: %+v", openai)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
