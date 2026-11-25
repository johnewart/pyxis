package ollamallm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func runOllamaStream(t *testing.T, body string) (*llmv1.AssistantReply, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	var complete *llmv1.AssistantReply
	err := New(srv.URL, "test-model", 0).ChatStream(context.Background(),
		[]*llmv1.Message{{Role: "user", Content: "hi"}}, nil, nil,
		func(f *llmv1.ConversationStreamFrame) error {
			if sc := f.GetStreamComplete(); sc != nil {
				complete = sc
			}
			return nil
		})
	return complete, err
}

func TestChatStream_truncatedBodyIsAnError(t *testing.T) {
	// Two content chunks, then the connection ends: no done:true line.
	body := `{"message":{"role":"assistant","content":"Hello "},"done":false}` + "\n" +
		`{"message":{"role":"assistant","content":"wor"},"done":false}` + "\n"
	complete, err := runOllamaStream(t, body)
	if err == nil || !strings.Contains(err.Error(), "stream ended before done") {
		t.Fatalf("want truncation error, got err=%v", err)
	}
	if complete != nil {
		t.Fatalf("truncated stream must not emit StreamComplete, got %+v", complete)
	}
}

func TestChatStream_doneBodyCompletes(t *testing.T) {
	body := `{"message":{"role":"assistant","content":"Hello"},"done":false}` + "\n" +
		`{"message":{"role":"assistant","content":""},"done":true,"prompt_eval_count":3,"eval_count":1}` + "\n"
	complete, err := runOllamaStream(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if complete == nil || !strings.Contains(complete.GetContent(), "Hello") {
		t.Fatalf("expected completed reply with content, got %+v", complete)
	}
}
