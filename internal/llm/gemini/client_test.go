//go:build !integration

package geminillm

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/genai"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

const (
	geminiGenerateJSONTextOnly = `{
  "candidates": [{
    "content": {"role": "model", "parts": [{"text": "Hello"}]},
    "finishReason": "STOP"
  }],
  "responseId": "resp_test",
  "usageMetadata": {"promptTokenCount": 1, "candidatesTokenCount": 2}
}`

	geminiGenerateJSONToolUse = `{
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
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cc := &genai.ClientConfig{
		APIKey:     "test-key",
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: srv.Client(),
		HTTPOptions: genai.HTTPOptions{
			BaseURL:    srv.URL + "/",
			APIVersion: "v1beta",
		},
	}
	inner, err := genai.NewClient(context.Background(), cc)
	if err != nil {
		t.Fatal(err)
	}
	return newClientFromGenAI(inner, DefaultModel, 256)
}

func TestClient_Chat_happyPath(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, ":generateContent") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, geminiGenerateJSONTextOnly)
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
	if reply.GetFinishReason() != "STOP" {
		t.Fatalf("finish=%q", reply.GetFinishReason())
	}
}

func TestClient_Chat_requestIncludesSystemAndCallOptions(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, geminiGenerateJSONTextOnly)
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
	genCfg, _ := got["generationConfig"].(map[string]any)
	if genCfg == nil {
		t.Fatalf("generationConfig missing: %v", got)
	}
	if int(genCfg["maxOutputTokens"].(float64)) != 99 {
		t.Fatalf("maxOutputTokens=%v", genCfg["maxOutputTokens"])
	}
	if math.Abs(genCfg["temperature"].(float64)-0.2) > 1e-5 {
		t.Fatalf("temperature=%v", genCfg["temperature"])
	}
	if got["systemInstruction"] == nil {
		t.Fatalf("systemInstruction=%v", got["systemInstruction"])
	}
}

func TestClient_Chat_noContentsError(t *testing.T) {
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
		if !strings.Contains(r.URL.Path, ":generateContent") {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		contents, _ := req["contents"].([]any)
		if len(contents) != 1 {
			t.Fatalf("contents len %d", len(contents))
		}
		if req["systemInstruction"] == nil {
			t.Fatal("expected systemInstruction")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, geminiGenerateJSONTextOnly)
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
		_, _ = io.WriteString(w, geminiGenerateJSONToolUse)
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
	if tc.GetName() != "echo" || tc.GetId() != "fc_1" {
		t.Fatalf("tool=%+v", tc)
	}
}
