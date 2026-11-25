package server

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/systemprompt"
	"google.golang.org/grpc/metadata"
)

func TestApplyStructuredSystemPrompt_FromProto(t *testing.T) {
	req := &llmv1.ChatStreamRequest{
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "agent.role", Content: "You are helpful."},
			{Id: "conversation.toolUse", Content: "TOOLS AND WORKSPACE"},
			{Id: "workspace.isolation", Content: "LLMD + Cursor"},
		},
	}
	in := []*llmv1.Message{{Role: "system", Content: "", Id: "system"}}

	ctxOut, applied := applyStructuredSystemPrompt(context.Background(), req, in, "cursor", providerkind.CursorCLI, nil)
	if applied.Fingerprint == "" {
		t.Fatal("expected combined fingerprint when sections present")
	}
	if len(applied.MDCFiles) == 0 {
		t.Fatal("expected MDC files for cursor overlay")
	}
	if strings.Contains(applied.Messages[0].GetContent(), "TOOLS AND WORKSPACE") {
		t.Fatal("prompt body must omit MDC-routed toolUse")
	}
	if got := systemprompt.RenderedMDCFilesFromContext(ctxOut); len(got) == 0 {
		t.Fatal("expected MDC files on context")
	}
}

func TestApplyStructuredSystemPrompt_FromMetadata(t *testing.T) {
	wire := `[{"id":"agent.role","content":"You are helpful."},{"id":"conversation.toolUse","content":"TOOLS AND WORKSPACE"},{"id":"workspace.isolation","content":"LLMD + Cursor"}]`
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		metadataSystemPromptSectionsKey, wire,
	))
	req := &llmv1.ChatStreamRequest{}
	in := []*llmv1.Message{{Role: "system", Content: "", Id: "system"}}

	ctxOut, applied := applyStructuredSystemPrompt(ctx, req, in, "cursor", providerkind.CursorCLI, nil)
	if applied.Fingerprint == "" {
		t.Fatal("expected combined fingerprint when sections present")
	}
	if len(applied.MDCFiles) == 0 {
		t.Fatal("expected MDC files for cursor overlay")
	}
	if strings.Contains(applied.Messages[0].GetContent(), "TOOLS AND WORKSPACE") {
		t.Fatal("prompt body must omit MDC-routed toolUse")
	}
	if got := systemprompt.RenderedMDCFilesFromContext(ctxOut); len(got) == 0 {
		t.Fatal("expected MDC files on context")
	}
}

func TestApplyStructuredSystemPrompt_FromMetadataBase64(t *testing.T) {
	wire := `[{"id":"agent.role","content":"You are helpful.\n\nWith newlines."},{"id":"conversation.toolUse","content":"TOOLS AND WORKSPACE"},{"id":"workspace.isolation","content":"LLMD + Cursor"}]`
	encoded := "b64:" + base64.StdEncoding.EncodeToString([]byte(wire))
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		metadataSystemPromptSectionsKey, encoded,
	))
	req := &llmv1.ChatStreamRequest{}
	in := []*llmv1.Message{{Role: "system", Content: "", Id: "system"}}

	_, applied := applyStructuredSystemPrompt(ctx, req, in, "cursor", providerkind.CursorCLI, nil)
	if applied.Fingerprint == "" {
		t.Fatal("expected combined fingerprint when base64 metadata sections present")
	}
	if !strings.Contains(applied.Messages[0].GetContent(), "You are helpful.") {
		t.Fatalf("expected multiline section in body, got %q", applied.Messages[0].GetContent())
	}
}

func TestApplyStructuredSystemPrompt_ProtoPreferredOverMetadata(t *testing.T) {
	req := &llmv1.ChatStreamRequest{
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "agent.role", Content: "from proto"},
		},
	}
	wire := `[{"id":"agent.role","content":"from metadata"}]`
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		metadataSystemPromptSectionsKey, wire,
	))
	in := []*llmv1.Message{{Role: "system", Content: "", Id: "system"}}

	_, applied := applyStructuredSystemPrompt(ctx, req, in, "openai", providerkind.OpenAI, nil)
	if !strings.Contains(applied.Messages[0].GetContent(), "from proto") {
		t.Fatalf("expected proto sections to win, got %q", applied.Messages[0].GetContent())
	}
	if strings.Contains(applied.Messages[0].GetContent(), "from metadata") {
		t.Fatal("metadata sections must not be used when proto sections are set")
	}
}

func TestApplyStructuredSystemPrompt_ClaudeOverlayAppend(t *testing.T) {
	req := &llmv1.ChatStreamRequest{
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "agent.role", Content: "You are helpful."},
			{Id: "conversation.toolUse", Content: "TOOLS AND WORKSPACE"},
			{Id: "workspace.isolation", Content: "LLMD + Claude Code"},
		},
	}
	in := []*llmv1.Message{{Role: "system", Content: "", Id: "system"}}

	ctxOut, applied := applyStructuredSystemPrompt(context.Background(), req, in, "claude_v2", providerkind.ClaudeCodeV2, nil)
	if strings.Contains(applied.Messages[0].GetContent(), "TOOLS AND WORKSPACE") {
		t.Fatal("prompt body must omit Claude-append toolUse")
	}
	if got := systemprompt.RenderedClaudeAppendFromContext(ctxOut); !strings.Contains(got, "TOOLS AND WORKSPACE") {
		t.Fatalf("expected Claude append on context, got %q", got)
	}
}

func TestApplyStructuredSystemPrompt_NoSectionsPassthrough(t *testing.T) {
	ctx := context.Background()
	in := []*llmv1.Message{{Role: "system", Content: "keep", Id: "system"}}
	_, applied := applyStructuredSystemPrompt(ctx, &llmv1.ChatStreamRequest{}, in, "cursor", providerkind.CursorCLI, nil)
	if applied.Fingerprint != "" {
		t.Fatal("expected empty fingerprint without sections")
	}
	if applied.Messages[0].GetContent() != "keep" {
		t.Fatalf("expected passthrough, got %q", applied.Messages[0].GetContent())
	}
}

// TestApplyStructuredSystemPrompt_SessionDefaultUsedWhenRequestOmitsSections pins
// the actual fix: a ChatStreamRequest carrying no system_prompt_sections of its
// own (the common case once a caller sets the default once via EnsureSession)
// still gets the session's persisted default rendered, instead of falling all
// the way back to legacy_flat_prompt passthrough.
func TestApplyStructuredSystemPrompt_SessionDefaultUsedWhenRequestOmitsSections(t *testing.T) {
	sessionDefault := []systemprompt.Section{
		{ID: "agent.role", Content: "Session-level default role."},
	}
	in := []*llmv1.Message{{Role: "system", Content: "", Id: "system"}}

	_, applied := applyStructuredSystemPrompt(context.Background(), &llmv1.ChatStreamRequest{}, in, "openai", providerkind.OpenAI, sessionDefault)
	if applied.Fingerprint == "" {
		t.Fatal("expected combined fingerprint from session default")
	}
	if !strings.Contains(applied.Messages[0].GetContent(), "Session-level default role.") {
		t.Fatalf("expected session default rendered into prompt body, got %q", applied.Messages[0].GetContent())
	}
}

// TestApplyStructuredSystemPrompt_RequestSectionsOverrideSessionDefault pins the
// override priority: an explicit per-turn ChatStreamRequest.system_prompt_sections
// still wins over the session's persisted default for that turn.
func TestApplyStructuredSystemPrompt_RequestSectionsOverrideSessionDefault(t *testing.T) {
	sessionDefault := []systemprompt.Section{
		{ID: "agent.role", Content: "Session-level default role."},
	}
	req := &llmv1.ChatStreamRequest{
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "agent.role", Content: "Per-turn override role."},
		},
	}
	in := []*llmv1.Message{{Role: "system", Content: "", Id: "system"}}

	_, applied := applyStructuredSystemPrompt(context.Background(), req, in, "openai", providerkind.OpenAI, sessionDefault)
	if !strings.Contains(applied.Messages[0].GetContent(), "Per-turn override role.") {
		t.Fatalf("expected per-turn sections to win, got %q", applied.Messages[0].GetContent())
	}
	if strings.Contains(applied.Messages[0].GetContent(), "Session-level default role.") {
		t.Fatal("session default must not be used when the request supplies its own sections")
	}
}

// captureStdout captures llmd's process-log records emitted while fn runs.
//
// It installs an llmdlog sink rather than swapping os.Stdout: swapping the
// global *os.File races with any goroutine still logging (e.g. a stream
// handler from an earlier test whose server Stop did not wait for it). Records
// from such stragglers may land in the capture too; callers only assert on
// substrings, so that is harmless.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	var (
		mu  sync.Mutex
		buf strings.Builder
	)
	llmdlog.SetSink(func(rec llmdlog.Record) {
		mu.Lock()
		defer mu.Unlock()
		buf.WriteString(rec.Message)
		buf.WriteByte('\n')
	})
	defer llmdlog.SetSink(nil)
	fn()
	mu.Lock()
	defer mu.Unlock()
	return buf.String()
}

func TestHasChatMarkdownSection(t *testing.T) {
	if hasChatMarkdownSection(nil) {
		t.Fatal("expected false for nil sections")
	}
	if hasChatMarkdownSection([]systemprompt.Section{{ID: "agent.identity", Content: "x"}}) {
		t.Fatal("expected false without provider.chatMarkdown")
	}
	if !hasChatMarkdownSection([]systemprompt.Section{
		{ID: "agent.identity", Content: "x"},
		{ID: "provider.chatMarkdown", Content: "Use GFM"},
	}) {
		t.Fatal("expected true when provider.chatMarkdown present")
	}
}

func TestApplyStructuredSystemPrompt_DebugLogStructuredDelivery(t *testing.T) {
	t.Setenv("LLMD_DEBUG_CHAT", "1")
	req := &llmv1.ChatStreamRequest{
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "agent.identity", Content: "Acme Agent"},
			{Id: "provider.chatMarkdown", Content: "Use GFM"},
		},
	}
	in := []*llmv1.Message{{Role: "system", Content: "", Id: "system"}}
	out := captureStdout(t, func() {
		applyStructuredSystemPrompt(context.Background(), req, in, "local-llamacpp", providerkind.Ollama, nil)
	})
	if !strings.Contains(out, "ChatStream system prompt delivery") {
		t.Fatalf("expected delivery log line, got %q", out)
	}
	if !strings.Contains(out, "delivery=structured_sections") {
		t.Fatalf("expected structured delivery log, got %q", out)
	}
	if !strings.Contains(out, "provider.chatMarkdown") {
		t.Fatalf("expected chat markdown in section_ids, got %q", out)
	}
	if !strings.Contains(out, "has_chat_markdown=true") {
		t.Fatalf("expected has_chat_markdown=true, got %q", out)
	}
	if !strings.Contains(out, "ChatStream system prompt section") {
		t.Fatalf("expected per-section debug log, got %q", out)
	}
	if !strings.Contains(out, "provider.chatMarkdown") || !strings.Contains(out, "Use GFM") {
		t.Fatalf("expected section content in debug log, got %q", out)
	}
	if !strings.Contains(out, "ChatStream system prompt rendered") {
		t.Fatalf("expected rendered prompt debug log, got %q", out)
	}
	if !strings.Contains(out, "Acme Agent") || !strings.Contains(out, "prompt_body") {
		t.Fatalf("expected assembled prompt body in debug log, got %q", out)
	}
}

func TestApplyStructuredSystemPrompt_DebugLogLegacyDelivery(t *testing.T) {
	t.Setenv("LLMD_DEBUG_CHAT", "1")
	in := []*llmv1.Message{{Role: "system", Content: "keep", Id: "system"}}
	out := captureStdout(t, func() {
		applyStructuredSystemPrompt(context.Background(), &llmv1.ChatStreamRequest{}, in, "local-llamacpp", providerkind.Ollama, nil)
	})
	if !strings.Contains(out, "ChatStream system prompt delivery") {
		t.Fatalf("expected delivery log line, got %q", out)
	}
	if !strings.Contains(out, "delivery=legacy_flat_prompt") {
		t.Fatalf("expected legacy delivery log, got %q", out)
	}
	if !strings.Contains(out, "has_chat_markdown=false") {
		t.Fatalf("expected has_chat_markdown=false, got %q", out)
	}
	if !strings.Contains(out, "ChatStream system prompt rendered") || !strings.Contains(out, "keep") {
		t.Fatalf("expected legacy prompt body in debug log, got %q", out)
	}
}
