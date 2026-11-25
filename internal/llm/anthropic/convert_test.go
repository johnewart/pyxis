package anthropicllm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func assertJSONContains(t *testing.T, p anthropic.MessageParam, sub string) {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), sub) {
		t.Fatalf("JSON %q does not contain %q", b, sub)
	}
}

func TestProtoMessagesToParams_systemAndUser(t *testing.T) {
	system, params, err := ProtoMessagesToParams([]*llmv1.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(system) != 1 || system[0].Text != "You are helpful." {
		t.Fatalf("system: %+v", system)
	}
	if len(params) != 1 {
		t.Fatalf("params len: %d", len(params))
	}
	assertJSONContains(t, params[0], `"role":"user"`)
	assertJSONContains(t, params[0], `"Hi"`)
}

func TestProtoMessagesToParams_multiTurn(t *testing.T) {
	_, params, err := ProtoMessagesToParams([]*llmv1.Message{
		{Role: "user", Content: "First"},
		{Role: "assistant", Content: "Second"},
		{Role: "user", Content: "Third"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(params) != 3 {
		t.Fatalf("len=%d", len(params))
	}
	assertJSONContains(t, params[0], `"role":"user"`)
	assertJSONContains(t, params[1], `"role":"assistant"`)
	assertJSONContains(t, params[2], `"role":"user"`)
}

func TestProtoMessagesToParams_emptyToolContentStillPairs(t *testing.T) {
	_, params, err := ProtoMessagesToParams([]*llmv1.Message{
		{Role: "user", Content: "run tool"},
		{
			Role:    "assistant",
			Content: "Calling tool.",
			ToolCalls: []*llmv1.ToolCall{
				{Id: "tc1", Name: "echo", ArgumentsJson: `{"message":"x"}`},
			},
		},
		{Role: "tool", ToolCallId: "tc1", Content: ""},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(params) != 3 {
		t.Fatalf("len=%d", len(params))
	}
	assertJSONContains(t, params[2], `"tool_result"`)
	assertJSONContains(t, params[2], `"{}"`)
	if strings.Contains(string(mustJSON(t, params[2])), "Tool result was not provided") {
		t.Fatal("unexpected injected stub for empty tool content")
	}
}

func mustJSON(t *testing.T, p anthropic.MessageParam) []byte {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProtoMessagesToParams_assistantWithToolsAndResults(t *testing.T) {
	_, params, err := ProtoMessagesToParams([]*llmv1.Message{
		{Role: "user", Content: "run tool"},
		{
			Role:    "assistant",
			Content: "Calling tool.",
			ToolCalls: []*llmv1.ToolCall{
				{Id: "tc1", Name: "echo", ArgumentsJson: `{"message":"x"}`},
			},
		},
		{Role: "tool", ToolCallId: "tc1", Content: `{"ok":true}`},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(params) != 3 {
		t.Fatalf("len=%d", len(params))
	}
	assertJSONContains(t, params[1], `"tool_use"`)
	assertJSONContains(t, params[1], `"tc1"`)
	assertJSONContains(t, params[2], `"tool_result"`)
}

func TestProtoMessagesToParams_repairsMultipleStaleToolUseTurns(t *testing.T) {
	// Simulates SQLite history with several assistant tool_use rows and no persisted tool results.
	_, params, err := ProtoMessagesToParams([]*llmv1.Message{
		{Role: "user", Content: "first", Id: "u1"},
		{Role: "assistant", ToolCalls: []*llmv1.ToolCall{{Id: "toolu_A", Name: "add_numbers", ArgumentsJson: `{"a":1,"b":2}`}}, Id: "a1"},
		{Role: "user", Content: "second", Id: "u2"},
		{Role: "assistant", ToolCalls: []*llmv1.ToolCall{{Id: "toolu_B", Name: "add_numbers", ArgumentsJson: `{"a":3,"b":4}`}}, Id: "a2"},
		{Role: "user", Content: "third", Id: "u3"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(params) != 7 {
		t.Fatalf("len(params)=%d want 7 (user, asst+tools, tool_result user, user, ...)", len(params))
	}
	assertJSONContains(t, params[2], `"tool_result"`)
	assertJSONContains(t, params[2], "toolu_A")
	assertJSONContains(t, params[4], "toolu_B")
}

func TestProtoMessagesToParams_injectsMissingToolResult(t *testing.T) {
	_, params, err := ProtoMessagesToParams([]*llmv1.Message{
		{Role: "user", Content: "x"},
		{
			Role:      "assistant",
			ToolCalls: []*llmv1.ToolCall{{Id: "only", Name: "echo", ArgumentsJson: `{}`}},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(params) != 3 {
		t.Fatalf("len=%d", len(params))
	}
	assertJSONContains(t, params[2], `"tool_result"`)
	assertJSONContains(t, params[2], "Tool result was not provided")
}

func TestProtoMessagesToParams_errors(t *testing.T) {
	// Empty assistant (no tools) is filtered out before conversion, not an error.
	_, params, err := ProtoMessagesToParams([]*llmv1.Message{
		{Role: "assistant", Content: ""},
	}, nil)
	if err != nil || len(params) != 0 {
		t.Fatalf("err=%v params=%d", err, len(params))
	}

	_, _, err = ProtoMessagesToParams([]*llmv1.Message{
		{Role: "user", Content: "hi"},
		{
			Role:      "assistant",
			ToolCalls: []*llmv1.ToolCall{{Id: "a", Name: "b", ArgumentsJson: `{`}},
		},
	}, nil)
	if err == nil {
		t.Fatal("expected error for invalid arguments_json")
	}
}

func TestProtoMessagesToParams_skipsOrphanTool(t *testing.T) {
	_, params, err := ProtoMessagesToParams([]*llmv1.Message{
		{Role: "tool", ToolCallId: "x", Content: "orphan"},
		{Role: "user", Content: "hi"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(params) != 1 {
		t.Fatalf("len=%d", len(params))
	}
}

func TestProtoMessagesToParams_systemUsesPromptCacheHint(t *testing.T) {
	opt := &llmv1.CallOptions{
		CacheOptions: &llmv1.CacheOptions{
			PromptCacheMode: llmv1.CacheOptions_PROMPT_CACHE_MODE_SHORT_TTL,
		},
	}
	system, _, err := ProtoMessagesToParams([]*llmv1.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(system) != 1 {
		t.Fatalf("len=%d", len(system))
	}
	if system[0].CacheControl.TTL != anthropic.CacheControlEphemeralTTLTTL5m {
		t.Fatalf("TTL=%v", system[0].CacheControl.TTL)
	}
}

func TestProtoMessagesToParams_onlySystemYieldsNoConversationParams(t *testing.T) {
	_, params, err := ProtoMessagesToParams([]*llmv1.Message{
		{Role: "system", Content: "only system"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(params) != 0 {
		t.Fatalf("len=%d", len(params))
	}
}
