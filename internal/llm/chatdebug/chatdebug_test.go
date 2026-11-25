package chatdebug

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/backend"
	"github.com/johnewart/pyxis/llmdlog"
)

type stubBackend struct {
	reply  *llmv1.AssistantReply
	frames []*llmv1.ConversationStreamFrame
}

func (s *stubBackend) Chat(context.Context, []*llmv1.Message, *llmv1.CallOptions, []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	return s.reply, nil
}

func (s *stubBackend) Generate(context.Context, string, string, *llmv1.CallOptions, []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	return s.reply, nil
}

func (s *stubBackend) ChatStream(_ context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	for _, f := range s.frames {
		if err := emit(f); err != nil {
			return err
		}
	}
	return nil
}

// readRecords drains the NDJSON chat file into parsed records.
func readRecords(t *testing.T, path string) []llmdlog.ChatRecord {
	t.Helper()
	llmdlog.CloseChatDebugFile()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read chat file: %v", err)
	}
	var out []llmdlog.ChatRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec llmdlog.ChatRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line is not valid JSON (%v): %q", err, line)
		}
		out = append(out, rec)
	}
	return out
}

func enableChatFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llmd-chat.log")
	t.Setenv(llmdlog.EnvDebugChat, "")
	t.Setenv(llmdlog.EnvDebugChatFile, path)
	llmdlog.CloseChatDebugFile()
	t.Cleanup(llmdlog.CloseChatDebugFile)
	return path
}

// The whole point: the file must contain the prompt we sent, verbatim.
func TestChatStreamRecordsPromptAndReply(t *testing.T) {
	path := enableChatFile(t)

	stub := &stubBackend{frames: []*llmv1.ConversationStreamFrame{
		{Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
			AssistantTextDelta: &llmv1.EvAssistantTextDelta{Delta: "par"},
		}},
		{Frame: &llmv1.ConversationStreamFrame_StreamComplete{
			StreamComplete: &llmv1.AssistantReply{
				Content:      "Paris.",
				FinishReason: "end_turn",
				ToolCalls: []*llmv1.ToolCall{
					{Id: "call-1", Name: "read_code", ArgumentsJson: `{"path":"main.go"}`},
				},
			},
		}},
	}}

	be := Wrap(stub, Meta{ProviderID: "anthropic", AgentID: "agent-1"})
	if be == stub {
		t.Fatalf("Wrap returned the bare backend even though chat debugging is on")
	}

	messages := []*llmv1.Message{
		{Role: "system", Content: "You are a geography tutor."},
		{Role: "user", Content: "Capital of France?"},
	}
	opt := &llmv1.CallOptions{Model: "claude-x", MaxTokens: 1024}
	tools := func() []*llmv1.ToolDefinition {
		return []*llmv1.ToolDefinition{
			{Name: "read_code", Description: "read a file", ParametersSchemaJson: `{"type":"object"}`},
		}
	}

	if err := be.ChatStream(context.Background(), messages, opt, tools, func(*llmv1.ConversationStreamFrame) error { return nil }); err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	recs := readRecords(t, path)
	if len(recs) != 2 {
		t.Fatalf("expected request + response records, got %d: %+v", len(recs), recs)
	}

	req := recs[0]
	if req.Event != "chat_request" {
		t.Fatalf("first record event = %q, want chat_request", req.Event)
	}
	if req.Fields["model"] != "claude-x" || req.Fields["provider_id"] != "anthropic" {
		t.Fatalf("request lost provider/model: %+v", req.Fields)
	}
	raw, err := json.Marshal(req.Fields["messages"])
	if err != nil {
		t.Fatal(err)
	}
	// The system prompt and the user turn must both be present, verbatim:
	// a truncated prompt cannot answer "what did we actually send?".
	for _, want := range []string{"You are a geography tutor.", "Capital of France?", "system", "user"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("recorded messages missing %q: %s", want, raw)
		}
	}
	toolsRaw, err := json.Marshal(req.Fields["tools"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(toolsRaw), "read_code") || !strings.Contains(string(toolsRaw), `{\"type\":\"object\"}`) {
		t.Fatalf("recorded tool surface lost name or schema: %s", toolsRaw)
	}

	resp := recs[1]
	if resp.Event != "chat_response" {
		t.Fatalf("second record event = %q, want chat_response", resp.Event)
	}
	if resp.Fields["content"] != "Paris." {
		t.Fatalf("reply content = %v, want %q", resp.Fields["content"], "Paris.")
	}
	if resp.Fields["finish_reason"] != "end_turn" {
		t.Fatalf("finish_reason = %v", resp.Fields["finish_reason"])
	}
	callsRaw, err := json.Marshal(resp.Fields["tool_calls"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(callsRaw), "read_code") || !strings.Contains(string(callsRaw), "main.go") {
		t.Fatalf("recorded tool calls lost name or arguments: %s", callsRaw)
	}
}

// Attachment content is spliced into the outbound message at send-time, so it
// is part of the prompt; payload_json is client-only and must never appear.
func TestRecordsProviderFacingAttachmentContentOnly(t *testing.T) {
	path := enableChatFile(t)
	be := Wrap(&stubBackend{reply: &llmv1.AssistantReply{Content: "ok"}}, Meta{ProviderID: "openai"})

	_, err := be.Chat(context.Background(), []*llmv1.Message{{
		Role:    "user",
		Content: "see attached",
		Attachments: []*llmv1.MessageAttachment{
			{Id: "a1", Kind: "ask_card", PayloadJson: `{"secret":"ui-only"}`, Content: "The user answered: yes"},
			{Id: "a2", Kind: "ask_card", PayloadJson: `{"secret":"display-only"}`},
		},
	}}, &llmv1.CallOptions{Model: "gpt-x"}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	recs := readRecords(t, path)
	raw, err := json.Marshal(recs[0].Fields["messages"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "The user answered: yes") {
		t.Fatalf("provider-facing attachment content missing: %s", raw)
	}
	if strings.Contains(string(raw), "ui-only") || strings.Contains(string(raw), "display-only") {
		t.Fatalf("client-only payload_json leaked into the chat record: %s", raw)
	}
}

func TestWrapIsNoOpWhenChatDebugOff(t *testing.T) {
	t.Setenv(llmdlog.EnvDebugChat, "")
	t.Setenv(llmdlog.EnvDebugChatFile, "")
	stub := &stubBackend{}
	var be backend.Backend = stub
	if got := Wrap(be, Meta{}); got != be {
		t.Fatalf("Wrap must return the backend unchanged when chat debugging is off")
	}
}
