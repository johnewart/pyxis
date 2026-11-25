package openaillm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	openai "github.com/sashabaranov/go-openai"
)

func TestIsOfficialOpenAI(t *testing.T) {
	cases := map[string]bool{
		"":                              true,
		"https://api.openai.com/v1":     true,
		"https://API.openai.com/v1/":    true,
		"http://localhost:8080/v1":      false,
		"https://openrouter.ai/api/v1":  false,
		"https://api.openai.com.evil/x": false,
	}
	for in, want := range cases {
		if got := isOfficialOpenAI(in); got != want {
			t.Errorf("isOfficialOpenAI(%q) = %v, want %v", in, got, want)
		}
	}
}

func plainRequest(t *testing.T, c *Client, opt *llmv1.CallOptions) openai.ChatCompletionRequest {
	t.Helper()
	req, err := c.chatRequest(
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
		false, opt, nil,
	)
	if err != nil {
		t.Fatalf("chatRequest: %v", err)
	}
	return req
}

func TestChatRequest_OfficialAPIUsesMaxCompletionTokens(t *testing.T) {
	req := plainRequest(t, New("k", "", "gpt-5", 1024), nil)
	if req.MaxCompletionTokens != 1024 || req.MaxTokens != 0 {
		t.Fatalf("got max_tokens=%d max_completion_tokens=%d, want 0/1024", req.MaxTokens, req.MaxCompletionTokens)
	}
}

func TestChatRequest_CompatibleServerUsesMaxTokens(t *testing.T) {
	req := plainRequest(t, New("k", "http://localhost:8080/v1", "qwen", 1024), nil)
	if req.MaxTokens != 1024 || req.MaxCompletionTokens != 0 {
		t.Fatalf("got max_tokens=%d max_completion_tokens=%d, want 1024/0", req.MaxTokens, req.MaxCompletionTokens)
	}
}

func TestRejectedParams(t *testing.T) {
	param := "max_tokens"
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"message", &openai.APIError{HTTPStatusCode: 400, Message: "Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead."}, paramMaxTokens},
		{"value", &openai.APIError{HTTPStatusCode: 400, Message: "Unsupported value: 'temperature' does not support 0.2 with this model."}, paramTemperature},
		{"code+param", &openai.APIError{HTTPStatusCode: 400, Code: "unsupported_parameter", Param: &param, Message: "nope"}, paramMaxTokens},
		{"other 400", &openai.APIError{HTTPStatusCode: 400, Message: "context length exceeded"}, ""},
		{"not 400", &openai.APIError{HTTPStatusCode: 500, Message: "Unsupported parameter: 'max_tokens'"}, ""},
		{"local o-series check", openai.ErrReasoningModelMaxTokensDeprecated, paramMaxTokens},
	}
	for _, tc := range cases {
		got := rejectedParams(tc.err)
		if tc.want == "" {
			if len(got) != 0 {
				t.Errorf("%s: got %v, want none", tc.name, got)
			}
			continue
		}
		if len(got) == 0 || got[0] != tc.want {
			t.Errorf("%s: got %v, want %q", tc.name, got, tc.want)
		}
	}
}

// fakeServer answers chat completions, rejecting any parameter in reject with
// OpenAI's 400 wording. It records every request body it saw.
type fakeServer struct {
	mu     sync.Mutex
	reject map[string]string // param -> "parameter" | "value"
	bodies []map[string]any
}

func (f *fakeServer) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	f.mu.Unlock()
	for p, kind := range f.reject {
		if _, sent := body[p]; !sent {
			continue
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		msg := "Unsupported parameter: '" + p + "' is not supported with this model."
		code := "unsupported_parameter"
		if kind == "value" {
			msg = "Unsupported value: '" + p + "' does not support this value with this model."
			code = "unsupported_value"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
			"message": msg, "type": "invalid_request_error", "param": p, "code": code,
		}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "r1", "object": "chat.completion", "model": body["model"],
		"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": "ok"}}},
	})
}

func (f *fakeServer) sent() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.bodies...)
}

func TestChat_LearnsRejectedParamsAndStopsSendingThem(t *testing.T) {
	f := &fakeServer{reject: map[string]string{paramMaxTokens: "parameter", paramTemperature: "value"}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	c := New("k", srv.URL+"/v1", "reasoner", 512)
	temp := float32(0.2)
	opt := &llmv1.CallOptions{Temperature: &temp}
	msgs := []*llmv1.Message{{Role: "user", Content: "hi"}}

	if _, err := c.Chat(context.Background(), msgs, opt, nil); err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	// max_tokens rejected, then temperature rejected, then success.
	if n := len(f.sent()); n != 3 {
		t.Fatalf("first call sent %d requests, want 3", n)
	}
	last := f.sent()[2]
	if _, ok := last[paramMaxTokens]; ok {
		t.Fatalf("retry still sent max_tokens: %v", last)
	}
	if got, _ := last[paramMaxCompletionTokens].(float64); got != 512 {
		t.Fatalf("retry max_completion_tokens = %v, want 512", last[paramMaxCompletionTokens])
	}
	if _, ok := last[paramTemperature]; ok {
		t.Fatalf("retry still sent temperature: %v", last)
	}

	// The client remembers: the next call goes out right the first time.
	if _, err := c.Chat(context.Background(), msgs, opt, nil); err != nil {
		t.Fatalf("second Chat: %v", err)
	}
	if n := len(f.sent()); n != 4 {
		t.Fatalf("second call sent %d more requests, want 1", n-3)
	}
}

func TestChat_UnfixableRejectionIsReturned(t *testing.T) {
	// Rejects both cap fields: after switching once there is nothing left to try.
	f := &fakeServer{reject: map[string]string{paramMaxTokens: "parameter", paramMaxCompletionTokens: "parameter"}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	c := New("k", srv.URL+"/v1", "stubborn", 512)
	_, err := c.Chat(context.Background(), []*llmv1.Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err == nil {
		t.Fatal("expected the server's error")
	}
	if n := len(f.sent()); n != 2 {
		t.Fatalf("sent %d requests, want 2", n)
	}
}
