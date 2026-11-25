package claudecodeclillm

import (
	"encoding/json"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestMessagesToStreamUserLines_resumeOnlyUser(t *testing.T) {
	t.Parallel()
	msgs := []*llmv1.Message{
		{Role: "system", Content: "rules"},
		{Role: "assistant", Content: "prior"},
		{Role: "user", Content: "new turn"},
	}
	lines, err := messagesToStreamUserLines(msgs, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("want 1 user frame on resume, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], `"type":"user"`) {
		t.Fatalf("expected user frame, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "new turn") {
		t.Fatalf("expected latest user content, got %q", lines[0])
	}
	if strings.Contains(lines[0], "prior") {
		t.Fatalf("assistant history must not replay on resume, got %q", lines[0])
	}
}

func TestMessagesToStreamUserLines_fullResyncStructured(t *testing.T) {
	t.Parallel()
	msgs := []*llmv1.Message{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: "again"},
	}
	lines, err := messagesToStreamUserLines(msgs, "")
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one frame: several frames make the CLI run several turns and emit
	// several results, and we would return the answer to an earlier one.
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 user frame on full resync, got %d: %v", len(lines), lines)
	}
	var env struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if env.Type != "user" || env.Message.Role != "user" {
		t.Fatalf("expected a user frame, got %q/%q", env.Type, env.Message.Role)
	}
	var texts []string
	for _, b := range env.Message.Content {
		if b.Type != "text" {
			t.Fatalf("expected only text blocks, got %q", b.Type)
		}
		texts = append(texts, b.Text)
	}
	want := []string{"hello", "[Previous assistant reply]\nhi", "again"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("blocks out of order or wrong: got %q want %q", texts, want)
	}
	if strings.Contains(lines[0], "rules") {
		t.Fatalf("system content must not be replayed in the user frame: %q", lines[0])
	}
}

func TestMessagesToStreamUserLines_resumeMultipleUserTailIsOneFrame(t *testing.T) {
	t.Parallel()
	msgs := []*llmv1.Message{
		{Role: "user", Content: "first queued"},
		{Role: "user", Content: "second queued"},
	}
	lines, err := messagesToStreamUserLines(msgs, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("want 1 frame even with several new user messages, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "first queued") || !strings.Contains(lines[0], "second queued") {
		t.Fatalf("frame missing queued content: %q", lines[0])
	}
}

func TestAppendClaudeCodePresetSystemPromptArgs_defaultNoPreset(t *testing.T) {
	t.Parallel()
	args := appendClaudeCodePresetSystemPromptArgs(nil, t.Context(), Config{SystemPrompt: "cfg"}, "msg-system")
	if len(args) != 2 || args[0] != "--system-prompt" {
		t.Fatalf("expected a single full --system-prompt override, got %v", args)
	}
	if !strings.Contains(args[1], "msg-system") || !strings.Contains(args[1], "cfg") {
		t.Fatalf("expected combined content in --system-prompt, got %q", args[1])
	}
	for _, a := range args {
		if a == "--append-system-prompt" {
			t.Fatalf("default (non-preset) mode must never use --append-system-prompt: %v", args)
		}
	}
}

func TestAppendClaudeCodePresetSystemPromptArgs_defaultEmptyStillOverridesPreset(t *testing.T) {
	t.Parallel()
	args := appendClaudeCodePresetSystemPromptArgs(nil, t.Context(), Config{}, "")
	// Omitting --system-prompt (or passing "") brings back the CLI's full preset.
	if len(args) != 2 || args[0] != "--system-prompt" || args[1] != claudeMinimalSystemPrompt {
		t.Fatalf("expected --system-prompt %q when nothing is configured, got %v", claudeMinimalSystemPrompt, args)
	}
}

func TestAppendClaudeCodePresetSystemPromptArgs_optInPreset(t *testing.T) {
	t.Parallel()
	args := appendClaudeCodePresetSystemPromptArgs(nil, t.Context(), Config{SystemPrompt: "cfg", UseClaudeCodePreset: true}, "msg-system")
	if len(args) < 4 || args[0] != "--system-prompt" || args[1] != "" {
		t.Fatalf("expected empty preset system prompt, got %v", args)
	}
	foundAppend := false
	for i, a := range args {
		if a == "--append-system-prompt" && i+1 < len(args) {
			combined := args[i+1]
			if strings.Contains(combined, "msg-system") && strings.Contains(combined, "cfg") {
				foundAppend = true
			}
		}
	}
	if !foundAppend {
		t.Fatalf("expected combined append prompt in args: %v", args)
	}
}

func TestMessageToStreamText_toolResultFlattened(t *testing.T) {
	t.Parallel()
	text, ok := messageToStreamText(&llmv1.Message{
		Role:       "tool",
		ToolCallId: "toolu_123",
		Content:    "ok",
	}, false)
	if !ok {
		t.Fatal("expected tool message to render on full resync")
	}
	// Text, not a tool_result block: its tool_use is never replayed, and an
	// orphan tool_result is rejected by the API.
	if text != "[Tool result toolu_123]\nok" {
		t.Fatalf("unexpected tool text %q", text)
	}
	if _, ok := messageToStreamText(&llmv1.Message{Role: "tool", ToolCallId: "x", Content: "ok"}, true); ok {
		t.Fatal("tool results must not replay on resume")
	}
}
