package anthropicllm

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// Minimal Messages API JSON bodies (fixtures for httptest).
const (
	anthropicMessageJSONTextOnly = `{
  "id": "msg_test",
  "type": "message",
  "role": "assistant",
  "model": "claude-haiku-4-5",
  "content": [{"type": "text", "text": "Hello"}],
  "stop_reason": "end_turn",
  "stop_sequence": "",
  "usage": {"input_tokens": 1, "output_tokens": 2}
}`

	anthropicMessageJSONToolUse = `{
  "id": "msg_tool",
  "type": "message",
  "role": "assistant",
  "model": "claude-haiku-4-5",
  "content": [
    {"type": "tool_use", "id": "tu_1", "name": "echo", "input": {"message": "x"}}
  ],
  "stop_reason": "tool_use",
  "stop_sequence": "",
  "usage": {"input_tokens": 3, "output_tokens": 4}
}`
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{
		inner: anthropic.NewClient(
			option.WithAPIKey("sk-test"),
			option.WithBaseURL(srv.URL),
		),
		defaultModel:   "claude-haiku-4-5",
		defaultMaxToks: 256,
	}
}

func TestClient_Chat_happyPath(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, anthropicMessageJSONTextOnly)
	})

	reply, err := c.Chat(context.Background(), []*llmv1.Message{
		{Role: "user", Content: "Hi"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply.GetContent() != "Hello" {
		t.Fatalf("content=%q", reply.GetContent())
	}
	if reply.GetFinishReason() != "end_turn" {
		t.Fatalf("finish=%q", reply.GetFinishReason())
	}
}

func TestClient_Chat_requestBodyIncludesSystemAndCallOptions(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, anthropicMessageJSONTextOnly)
	})

	temp := float32(0.2)
	_, err := c.Chat(context.Background(), []*llmv1.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "Hi"},
	}, &llmv1.CallOptions{
		Model:       "override-model",
		MaxTokens:   99,
		Temperature: &temp,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["model"] != "override-model" {
		t.Fatalf("model=%v", got["model"])
	}
	if int64(got["max_tokens"].(float64)) != 99 {
		t.Fatalf("max_tokens=%v", got["max_tokens"])
	}
	if math.Abs(got["temperature"].(float64)-0.2) > 1e-5 {
		t.Fatalf("temperature=%v", got["temperature"])
	}
	sys, ok := got["system"].([]any)
	if !ok || len(sys) != 1 {
		t.Fatalf("system=%v", got["system"])
	}
}

func TestClient_Chat_noParamsError(t *testing.T) {
	c := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("server should not be called")
	})
	_, err := c.Chat(context.Background(), []*llmv1.Message{
		{Role: "system", Content: "only"},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClient_Generate_happyPath(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		msgs, _ := req["messages"].([]any)
		if len(msgs) != 1 {
			t.Fatalf("messages len %d", len(msgs))
		}
		sys, _ := req["system"].([]any)
		if len(sys) != 1 {
			t.Fatalf("system len %d", len(sys))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, anthropicMessageJSONTextOnly)
	})

	reply, err := c.Generate(context.Background(), "You are nice.", "Say hi", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply.GetContent() != "Hello" {
		t.Fatalf("content=%q", reply.GetContent())
	}
}

func TestClient_Generate_emptyPrompt(t *testing.T) {
	c := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	})
	_, err := c.Generate(context.Background(), "sys", "   ", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClient_Chat_toolUseReply(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, anthropicMessageJSONToolUse)
	})

	reply, err := c.Chat(context.Background(), []*llmv1.Message{
		{Role: "user", Content: "use tool"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.GetToolCalls()) != 1 {
		t.Fatalf("tool calls: %v", reply.GetToolCalls())
	}
	tc := reply.GetToolCalls()[0]
	if tc.GetName() != "echo" || tc.GetId() != "tu_1" {
		t.Fatalf("tool=%+v", tc)
	}
}
