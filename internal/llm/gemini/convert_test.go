//go:build !integration

package geminillm

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/genai"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func contentJSON(c *genai.Content) string {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestProtoMessagesToGemini_systemAndUser(t *testing.T) {
	system, contents, err := ProtoMessagesToGemini([]*llmv1.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if system == nil || len(system.Parts) != 1 || system.Parts[0].Text != "You are helpful." {
		t.Fatalf("system: %+v", system)
	}
	if len(contents) != 1 || contents[0].Role != string(genai.RoleUser) {
		t.Fatalf("contents: %+v", contents)
	}
	if !strings.Contains(contentJSON(contents[0]), "Hi") {
		t.Fatalf("user content missing Hi")
	}
}

func TestProtoMessagesToGemini_multiTurn(t *testing.T) {
	_, contents, err := ProtoMessagesToGemini([]*llmv1.Message{
		{Role: "user", Content: "First"},
		{Role: "assistant", Content: "Second"},
		{Role: "user", Content: "Third"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 3 {
		t.Fatalf("len=%d", len(contents))
	}
	if contents[0].Role != string(genai.RoleUser) {
		t.Fatalf("role0=%q", contents[0].Role)
	}
	if contents[1].Role != string(genai.RoleModel) {
		t.Fatalf("role1=%q", contents[1].Role)
	}
	if contents[2].Role != string(genai.RoleUser) {
		t.Fatalf("role2=%q", contents[2].Role)
	}
}

func TestProtoMessagesToGemini_emptyToolContentStillPairs(t *testing.T) {
	_, contents, err := ProtoMessagesToGemini([]*llmv1.Message{
		{Role: "user", Content: "run tool"},
		{
			Role:    "assistant",
			Content: "Calling tool.",
			ToolCalls: []*llmv1.ToolCall{
				{Id: "tc1", Name: "echo", ArgumentsJson: `{"message":"x"}`},
			},
		},
		{Role: "tool", ToolCallId: "tc1", Content: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 3 {
		t.Fatalf("len=%d", len(contents))
	}
	j := contentJSON(contents[2])
	if !strings.Contains(j, "functionResponse") {
		t.Fatalf("missing functionResponse: %s", j)
	}
}

func TestProtoMessagesToGemini_assistantWithToolsAndResults(t *testing.T) {
	_, contents, err := ProtoMessagesToGemini([]*llmv1.Message{
		{Role: "user", Content: "run tool"},
		{
			Role:    "assistant",
			Content: "Calling tool.",
			ToolCalls: []*llmv1.ToolCall{
				{Id: "tc1", Name: "echo", ArgumentsJson: `{"message":"x"}`},
			},
		},
		{Role: "tool", ToolCallId: "tc1", Content: `{"ok":true}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 3 {
		t.Fatalf("len=%d", len(contents))
	}
	if !strings.Contains(contentJSON(contents[1]), "functionCall") {
		t.Fatal("expected functionCall on assistant turn")
	}
	if !strings.Contains(contentJSON(contents[2]), "functionResponse") {
		t.Fatal("expected functionResponse on tool result turn")
	}
}

func TestProtoMessagesToGemini_repairsMultipleStaleToolUseTurns(t *testing.T) {
	_, contents, err := ProtoMessagesToGemini([]*llmv1.Message{
		{Role: "user", Content: "first", Id: "u1"},
		{Role: "assistant", ToolCalls: []*llmv1.ToolCall{{Id: "toolu_A", Name: "add_numbers", ArgumentsJson: `{"a":1,"b":2}`}}, Id: "a1"},
		{Role: "user", Content: "second", Id: "u2"},
		{Role: "assistant", ToolCalls: []*llmv1.ToolCall{{Id: "toolu_B", Name: "add_numbers", ArgumentsJson: `{"a":3,"b":4}`}}, Id: "a2"},
		{Role: "user", Content: "third", Id: "u3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 7 {
		t.Fatalf("len(contents)=%d want 7", len(contents))
	}
	if !strings.Contains(contentJSON(contents[2]), "toolu_A") {
		t.Fatal("missing repair for toolu_A")
	}
	if !strings.Contains(contentJSON(contents[4]), "toolu_B") {
		t.Fatal("missing repair for toolu_B")
	}
}

func TestProtoMessagesToGemini_injectsMissingToolResult(t *testing.T) {
	_, contents, err := ProtoMessagesToGemini([]*llmv1.Message{
		{Role: "user", Content: "x"},
		{
			Role:      "assistant",
			ToolCalls: []*llmv1.ToolCall{{Id: "only", Name: "echo", ArgumentsJson: `{}`}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 3 {
		t.Fatalf("len=%d", len(contents))
	}
	if !strings.Contains(contentJSON(contents[2]), "Tool result was not provided") {
		t.Fatal("expected injected stub")
	}
}

func TestProtoMessagesToGemini_errors(t *testing.T) {
	_, contents, err := ProtoMessagesToGemini([]*llmv1.Message{
		{Role: "assistant", Content: ""},
	})
	if err != nil || len(contents) != 0 {
		t.Fatalf("err=%v contents=%d", err, len(contents))
	}

	_, _, err = ProtoMessagesToGemini([]*llmv1.Message{
		{Role: "user", Content: "hi"},
		{
			Role:      "assistant",
			ToolCalls: []*llmv1.ToolCall{{Id: "a", Name: "b", ArgumentsJson: `{`}},
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid arguments_json")
	}
}

func TestProtoMessagesToGemini_skipsOrphanTool(t *testing.T) {
	_, contents, err := ProtoMessagesToGemini([]*llmv1.Message{
		{Role: "tool", ToolCallId: "x", Content: "orphan"},
		{Role: "user", Content: "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 1 {
		t.Fatalf("len=%d", len(contents))
	}
}

func TestToolResultResponseFromBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want map[string]any
	}{
		{name: "empty", in: "", want: map[string]any{}},
		{name: "json object", in: `{"ok":true}`, want: map[string]any{"ok": true}},
		{name: "plain text", in: "Session title updated to: Fix bug", want: map[string]any{"result": "Session title updated to: Fix bug"}},
		{name: "json array", in: `[1,2]`, want: map[string]any{"result": []any{float64(1), float64(2)}}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := toolResultResponseFromBody(tc.in)
			gb, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			wb, err := json.Marshal(tc.want)
			if err != nil {
				t.Fatal(err)
			}
			if string(gb) != string(wb) {
				t.Fatalf("got=%s want=%s", gb, wb)
			}
		})
	}
}

func TestProtoMessagesToGemini_plainTextToolResult(t *testing.T) {
	plain := "Session title updated to: Fix Gemini tool result JSON error"
	_, contents, err := ProtoMessagesToGemini([]*llmv1.Message{
		{Role: "user", Content: "rename session"},
		{
			Role:      "assistant",
			ToolCalls: []*llmv1.ToolCall{{Id: "tc1", Name: "update_session_title", ArgumentsJson: `{"title":"Fix Gemini tool result JSON error"}`}},
		},
		{Role: "tool", ToolCallId: "tc1", Content: plain},
	})
	if err != nil {
		t.Fatal(err)
	}
	j := contentJSON(contents[2])
	if !strings.Contains(j, plain) {
		t.Fatalf("missing plain tool result in functionResponse: %s", j)
	}
}

func TestProtoMessagesToGemini_onlySystemYieldsNoConversationContents(t *testing.T) {
	_, contents, err := ProtoMessagesToGemini([]*llmv1.Message{
		{Role: "system", Content: "only system"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("len=%d", len(contents))
	}
}
