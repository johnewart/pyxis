package toolexec

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/tenantscope"
)

func TestRun_httpWebhook_success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"n":1}`))
	}))
	t.Cleanup(ts.Close)

	def := &llmv1.ToolDefinition{
		Name: "t",
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: ts.URL},
		},
	}
	content, ok, errMsg := RunWithHandlers(context.Background(), def, Invocation{
		ToolCallID: "id1", Name: "t", ArgumentsJSON: `{}`, AgentID: "a", SessionID: "s",
	}, NewHandlers())
	if !ok || errMsg != "" {
		t.Fatalf("ok=%v errMsg=%q content=%q", ok, errMsg, content)
	}
	if content != `{"n":1}` {
		t.Fatalf("content=%q", content)
	}
}

type capturedWebhook struct {
	body []byte
	sig  string
}

func runSignedWebhook(t *testing.T, inv Invocation) capturedWebhook {
	t.Helper()
	var got capturedWebhook
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		got = capturedWebhook{body: b, sig: r.Header.Get("X-Llmd-Signature")}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(ts.Close)
	secret := "s3cret"
	def := &llmv1.ToolDefinition{
		Name: "t",
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: ts.URL, HmacSecret: &secret},
		},
	}
	content, ok, errMsg := RunWithHandlers(context.Background(), def, inv, NewHandlers())
	if !ok || errMsg != "" {
		t.Fatalf("ok=%v errMsg=%q content=%q", ok, errMsg, content)
	}
	return got
}

func hmacHex(body []byte) string {
	m := hmac.New(sha256.New, []byte("s3cret"))
	_, _ = m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func TestRun_httpWebhook_payloadFieldsAndSignature(t *testing.T) {
	inv := Invocation{
		ToolCallID: "id1", Name: "t", ArgumentsJSON: `{"a":1}`, AgentID: "ag", SessionID: "sess",
		Scope:       tenantscope.Scope{TenantID: "ten", ScopeID: "scp", PrincipalID: "prin"},
		ToolContext: map[string]string{"k": "v1"},
	}
	got := runSignedWebhook(t, inv)
	var m map[string]any
	if err := json.Unmarshal(got.body, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"tool_call_id": "id1", "name": "t", "arguments_json": `{"a":1}`, "agent_id": "ag",
		"session_id": "sess", "idempotency_key": "id1", "tenant_id": "ten", "scope_id": "scp", "principal_id": "prin",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s=%v want %q", k, m[k], v)
		}
	}
	tc, _ := m["tool_context"].(map[string]any)
	if tc["k"] != "v1" {
		t.Errorf("tool_context=%v", m["tool_context"])
	}
	if got.sig != hmacHex(got.body) {
		t.Errorf("signature %q does not match body", got.sig)
	}

	inv.ToolContext = map[string]string{"k": "v2"}
	got2 := runSignedWebhook(t, inv)
	if got2.sig == got.sig {
		t.Error("signature unchanged after tool_context changed")
	}
	if got2.sig != hmacHex(got2.body) {
		t.Error("second signature does not match body")
	}
}

func TestRun_httpWebhook_principalAbsentWhenEmpty(t *testing.T) {
	got := runSignedWebhook(t, Invocation{ToolCallID: "id1", Name: "t", Scope: tenantscope.Scope{TenantID: "ten"}})
	var m map[string]any
	if err := json.Unmarshal(got.body, &m); err != nil {
		t.Fatal(err)
	}
	if _, present := m["principal_id"]; present {
		t.Errorf("principal_id present: %s", got.body)
	}
	if m["tenant_id"] != "ten" {
		t.Errorf("tenant_id=%v", m["tenant_id"])
	}
}

func TestRun_httpWebhook_non2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	t.Cleanup(ts.Close)

	def := &llmv1.ToolDefinition{
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: ts.URL},
		},
	}
	_, ok, errMsg := RunWithHandlers(context.Background(), def, Invocation{ToolCallID: "x", Name: "t"}, NewHandlers())
	if ok || errMsg == "" {
		t.Fatalf("expected failure, ok=%v errMsg=%q", ok, errMsg)
	}
}

func TestRun_readOnlyRejectsMutation(t *testing.T) {
	def := &llmv1.ToolDefinition{
		Name:           "write_file",
		Classification: llmv1.ToolClassification_TOOL_CLASSIFICATION_MUTATION,
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: "http://example.invalid"},
		},
	}
	content, ok, errMsg := RunWithHandlers(context.Background(), def, Invocation{
		ToolCallID: "id1",
		Name:       "write_file",
		ReadOnly:   true,
	}, NewHandlers())
	if ok || errMsg != ReadOnlyMutationError || content != ReadOnlyMutationError {
		t.Fatalf("ok=%v errMsg=%q content=%q", ok, errMsg, content)
	}
}

// TestRunWithHandlers_NonChatCallerInvocation asserts RunWithHandlers is
// reachable with an Invocation synthesized for a non-chat caller (e.g. the
// gRPC InvokeTool RPC): no SessionID/AgentID, and ReadOnly left unset. An
// empty SessionID must not panic and must not be treated as an implicit
// read-only turn.
func TestRunWithHandlers_NonChatCallerInvocation(t *testing.T) {
	def := &llmv1.ToolDefinition{
		Name:           "write_file",
		Classification: llmv1.ToolClassification_TOOL_CLASSIFICATION_MUTATION,
		Execution: &llmv1.ToolDefinition_InlineBuiltin{
			InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{},
		},
	}
	handlers := NewHandlers()
	if err := handlers.Register("write_file", func(_ context.Context, _ json.RawMessage, _ Invocation) (Result, error) {
		return Result{Content: "ok"}, nil
	}); err != nil {
		t.Fatal(err)
	}

	inv := Invocation{
		ToolCallID:    "invoke-tool-synthesized",
		Name:          "write_file",
		ArgumentsJSON: `{}`,
		// AgentID and SessionID intentionally empty: a non-chat caller has
		// no turn to attribute them to.
	}
	content, ok, errMsg := RunWithHandlers(context.Background(), def, inv, handlers)
	if !ok || errMsg != "" || content != "ok" {
		t.Fatalf("ok=%v errMsg=%q content=%q", ok, errMsg, content)
	}
}

func TestEffectiveMaxToolRounds(t *testing.T) {
	if n := EffectiveMaxToolRounds(nil); n != DefaultMaxToolRounds {
		t.Fatalf("got %d", n)
	}
	z := int32(0)
	if n := EffectiveMaxToolRounds(&llmv1.CallOptions{MaxToolRounds: &z}); n != DefaultMaxToolRounds {
		t.Fatalf("zero: got %d", n)
	}
	v := int32(3)
	if n := EffectiveMaxToolRounds(&llmv1.CallOptions{MaxToolRounds: &v}); n != 3 {
		t.Fatalf("got %d", n)
	}
	huge := int32(999)
	if n := EffectiveMaxToolRounds(&llmv1.CallOptions{MaxToolRounds: &huge}); n != MaxToolRoundsCap {
		t.Fatalf("cap: got %d", n)
	}
}
