package toolexec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/toolseam"
)

func runWebhookBody(t *testing.T, contentType, body string) (content string, ok bool, errMsg string, tc *toolseam.TurnControl) {
	t.Helper()
	return runWebhookStatus(t, http.StatusOK, contentType, body)
}

func runWebhookStatus(t *testing.T, status int, contentType, body string) (content string, ok bool, errMsg string, tc *toolseam.TurnControl) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	def := &llmv1.ToolDefinition{
		Name: "t",
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: ts.URL},
		},
	}
	tc = &toolseam.TurnControl{}
	ctx := toolseam.WithTurnControl(context.Background(), tc)
	content, ok, errMsg = RunWithHandlers(ctx, def, Invocation{ToolCallID: "id1", Name: "t", ArgumentsJSON: `{}`}, NewHandlers())
	return content, ok, errMsg, tc
}

func TestWebhookTyped_EndsTurn(t *testing.T) {
	content, ok, errMsg, tc := runWebhookBody(t, "application/json; charset=utf-8", `{"content":"done","ends_turn":true}`)
	if !ok || errMsg != "" || content != "done" {
		t.Fatalf("ok=%v errMsg=%q content=%q", ok, errMsg, content)
	}
	if !tc.EndRequested() {
		t.Fatal("expected end-turn request")
	}
	if r := tc.Requests(); r[0].ToolCallID != "id1" || r[0].ToolName != "t" {
		t.Fatalf("request=%+v", r[0])
	}
}

func TestWebhookTyped_IsErrorNeverEndsTurn(t *testing.T) {
	content, ok, errMsg, tc := runWebhookBody(t, "application/json", `{"content":"boom","is_error":true,"ends_turn":true}`)
	if ok || content != "boom" || errMsg == "" {
		t.Fatalf("ok=%v errMsg=%q content=%q", ok, errMsg, content)
	}
	if tc.EndRequested() {
		t.Fatal("failed result must not end the turn")
	}
}

func TestWebhookTyped_PlainTextUnchanged(t *testing.T) {
	body := `{"content":"x","ends_turn":true}`
	content, ok, _, tc := runWebhookBody(t, "text/plain", body)
	if !ok || content != body || tc.EndRequested() {
		t.Fatalf("ok=%v content=%q ends=%v", ok, content, tc.EndRequested())
	}
}

func TestWebhookTyped_RealPlainTextUnchanged(t *testing.T) {
	for _, ct := range []string{"text/plain", ""} {
		body := "hello world, not json"
		content, ok, errMsg, tc := runWebhookBody(t, ct, body)
		if !ok || errMsg != "" || content != body || tc.EndRequested() {
			t.Fatalf("ct=%q ok=%v errMsg=%q content=%q", ct, ok, errMsg, content)
		}
	}
}

func TestWebhookTyped_Non2xxKeepsOldBehavior(t *testing.T) {
	_, ok, errMsg, tc := runWebhookStatus(t, http.StatusInternalServerError, "application/json", `{"content":"x","ends_turn":true}`)
	if ok || errMsg != "HTTP 500" {
		t.Fatalf("ok=%v errMsg=%q", ok, errMsg)
	}
	if tc.EndRequested() {
		t.Fatal("non-2xx must not end the turn")
	}
}

func TestWebhookTyped_UnrelatedJSONUnchanged(t *testing.T) {
	for _, body := range []string{
		`{"n":1}`,
		`{"content":"x","extra":1}`,
		`{"is_error":true}`,
		`{"content":"x"} trailing`,
		`[1]`,
	} {
		content, ok, errMsg, tc := runWebhookBody(t, "application/json", body)
		if !ok || errMsg != "" || content != body || tc.EndRequested() {
			t.Fatalf("body=%q ok=%v errMsg=%q content=%q", body, ok, errMsg, content)
		}
	}
}
