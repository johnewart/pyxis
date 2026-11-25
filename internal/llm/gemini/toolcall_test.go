//go:build !integration

package geminillm

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/genai"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestToolCallFromFunctionCallPart_thoughtSignature(t *testing.T) {
	sig := []byte("opaque-thought-signature")
	part := &genai.Part{
		FunctionCall: &genai.FunctionCall{
			ID:   "fc_1",
			Name: "set_status",
			Args: map[string]any{"description": "working"},
		},
		ThoughtSignature: sig,
	}
	tc, err := toolCallFromFunctionCallPart(part)
	if err != nil {
		t.Fatal(err)
	}
	if tc.GetId() != "fc_1" || tc.GetName() != "set_status" {
		t.Fatalf("tc=%+v", tc)
	}
	if string(tc.GetThoughtSignature()) != string(sig) {
		t.Fatalf("thought_signature=%q want %q", tc.GetThoughtSignature(), sig)
	}
}

func TestFunctionCallPartFromToolCall_thoughtSignature(t *testing.T) {
	sig := []byte("round-trip-signature")
	part, err := functionCallPartFromToolCall(&llmv1.ToolCall{
		Id:               "fc_2",
		Name:             "read_code",
		ArgumentsJson:    `{"path":"foo.go"}`,
		ThoughtSignature: sig,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(part.ThoughtSignature) != string(sig) {
		t.Fatalf("part thoughtSignature=%q want %q", part.ThoughtSignature, sig)
	}
	if part.FunctionCall == nil || part.FunctionCall.Name != "read_code" {
		t.Fatalf("functionCall=%+v", part.FunctionCall)
	}
}

func TestToolCallsFromResponse_thoughtSignature(t *testing.T) {
	const raw = `{
  "candidates": [{
    "content": {
      "role": "model",
      "parts": [{
        "functionCall": {"id": "fc_1", "name": "echo", "args": {"message": "x"}},
        "thoughtSignature": "c2lnbmF0dXJl"
      }]
    },
    "finishReason": "STOP"
  }],
  "responseId": "resp_tool"
}`
	var resp genai.GenerateContentResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatal(err)
	}
	calls, err := toolCallsFromResponse(&resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls=%v", calls)
	}
	if len(calls[0].GetThoughtSignature()) == 0 {
		t.Fatal("expected thought_signature on tool call")
	}
}

func TestProtoMessagesToGemini_preservesThoughtSignatureOnReplay(t *testing.T) {
	sig := []byte("must-replay")
	_, contents, err := ProtoMessagesToGemini([]*llmv1.Message{
		{Role: "user", Content: "run tool"},
		{
			Role:    "assistant",
			Content: "Calling tool.",
			ToolCalls: []*llmv1.ToolCall{
				{
					Id:               "tc1",
					Name:             "set_status",
					ArgumentsJson:    `{"description":"x"}`,
					ThoughtSignature: sig,
				},
			},
		},
		{Role: "tool", ToolCallId: "tc1", Content: `{"ok":true}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) < 2 {
		t.Fatalf("len=%d", len(contents))
	}
	j := contentJSON(contents[1])
	if !strings.Contains(j, "functionCall") {
		t.Fatalf("missing functionCall: %s", j)
	}
	if !strings.Contains(j, "thoughtSignature") {
		t.Fatalf("missing thoughtSignature on replay: %s", j)
	}
}
