//go:build !integration

package geminillm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/genai"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

const geminiStreamSSE = "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hel\"}]}}],\"responseId\":\"stream1\"}\n\ndata: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"lo\"}]}}],\"responseId\":\"stream1\",\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":2}}\n\n"

const geminiStreamToolSSE = "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"id\":\"fc_stream\",\"name\":\"echo\",\"args\":{\"message\":\"y\"}},\"thoughtSignature\":\"c3RyZWFt\"}]}}],\"responseId\":\"stream_tool\"}\n\n"

func TestClient_ChatStream_textFrames(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":streamGenerateContent") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiStreamSSE)
	})

	var (
		sawStarted bool
		sawText    strings.Builder
		sawDone    bool
		complete   *llmv1.AssistantReply
	)
	err := c.ChatStream(context.Background(), []*llmv1.Message{
		{Role: "user", Content: "Hi"},
	}, nil, nil, func(f *llmv1.ConversationStreamFrame) error {
		switch {
		case f.GetAssistantTurnStarted() != nil:
			sawStarted = true
		case f.GetAssistantTextDelta() != nil:
			sawText.WriteString(f.GetAssistantTextDelta().GetDelta())
		case f.GetAssistantTurnFinished() != nil:
			sawDone = true
		case f.GetStreamComplete() != nil:
			complete = f.GetStreamComplete()
			sawDone = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawStarted {
		t.Fatal("missing AssistantTurnStarted")
	}
	if !sawDone {
		t.Fatal("missing turn finished / stream complete")
	}
	if got := sawText.String(); got != "Hello" {
		t.Fatalf("stream text=%q", got)
	}
	if complete == nil || complete.GetContent() != "Hello" {
		t.Fatalf("complete=%v", complete)
	}
}

func TestClient_ChatStream_toolFrames(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":streamGenerateContent") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiStreamToolSSE)
	})

	var (
		sawToolStart   bool
		sawToolFinish  bool
		sawToolInReply bool
	)
	err := c.ChatStream(context.Background(), []*llmv1.Message{
		{Role: "user", Content: "tool"},
	}, nil, nil, func(f *llmv1.ConversationStreamFrame) error {
		if f.GetToolCallStreamStarted() != nil {
			sawToolStart = true
		}
		if f.GetToolCallStreamFinished() != nil {
			sawToolFinish = true
		}
		if sc := f.GetStreamComplete(); sc != nil && len(sc.GetToolCalls()) == 1 {
			if len(sc.GetToolCalls()[0].GetThoughtSignature()) == 0 {
				t.Fatal("stream complete missing thought_signature")
			}
			sawToolInReply = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawToolStart {
		t.Fatal("missing ToolCallStreamStarted")
	}
	if !sawToolFinish {
		t.Fatal("missing ToolCallStreamFinished")
	}
	if !sawToolInReply {
		t.Fatal("stream complete missing tool call")
	}
}

func TestAssistantReplyFromResponse_fixture(t *testing.T) {
	var resp genai.GenerateContentResponse
	if err := json.Unmarshal([]byte(geminiGenerateJSONToolUse), &resp); err != nil {
		t.Fatal(err)
	}
	reply, err := assistantReplyFromResponse(context.Background(), &resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.GetToolCalls()) != 1 {
		t.Fatalf("calls=%v", reply.GetToolCalls())
	}
	if len(reply.GetToolCalls()[0].GetThoughtSignature()) == 0 {
		t.Fatal("expected thought_signature on tool call")
	}
}
