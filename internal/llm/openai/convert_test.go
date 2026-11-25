package openaillm

import (
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/sashabaranov/go-openai"
)

func TestProtoToOpenAIMessages_assistantReasoningRoundTrip(t *testing.T) {
	msgs, err := ProtoToOpenAIMessages([]*llmv1.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "answer", ThinkingContent: "step-by-step"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len=%d", len(msgs))
	}
	a := msgs[1]
	if a.Role != openai.ChatMessageRoleAssistant || a.Content != "answer" {
		t.Fatalf("assistant=%+v", a)
	}
	if a.ReasoningContent != "step-by-step" {
		t.Fatalf("ReasoningContent=%q", a.ReasoningContent)
	}
}
