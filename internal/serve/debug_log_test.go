package serve

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestDebugLogCallOptions_Disabled(t *testing.T) {
	t.Setenv("LLMD_DEBUG_CHAT", "0")
	t.Setenv("LLMD_DEBUG_CHAT_FILE", "")
	temp := float32(0.7)
	wire := &llmv1.CallOptions{Temperature: &temp}
	effective := &llmv1.CallOptions{Model: "test-model", Temperature: &temp}
	out := captureStdout(t, func() {
		debugLogCallOptions("ChatStream", "agent-1", "sess-1", wire, effective)
	})
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected no log when LLMD_DEBUG_CHAT=0, got %q", out)
	}
}

func TestDebugLogCallOptions_LogsWireAndEffective(t *testing.T) {
	t.Setenv("LLMD_DEBUG_CHAT", "1")
	t.Setenv("LLMD_DEBUG_CHAT_FILE", "")
	temp := float32(0.7)
	topP := float32(0.9)
	topK := int64(40)
	maxToolRounds := int32(8)
	readOnly := true
	wire := &llmv1.CallOptions{
		Temperature:   &temp,
		TopP:          &topP,
		TopK:          &topK,
		MaxToolRounds: &maxToolRounds,
		ReadOnly:      &readOnly,
	}
	effective := &llmv1.CallOptions{
		Model:         "local-model",
		MaxTokens:     4096,
		Temperature:   &temp,
		TopP:          &topP,
		TopK:          &topK,
		MaxToolRounds: &maxToolRounds,
		ReadOnly:      &readOnly,
	}
	out := captureStdout(t, func() {
		debugLogCallOptions("ChatStream", "agent-1", "sess-1", wire, effective)
	})
	if !strings.Contains(out, "ChatStream call options") {
		t.Fatalf("expected call options log line, got %q", out)
	}
	if !strings.Contains(out, "agent-1") {
		t.Fatalf("expected agent_id in log, got %q", out)
	}
	if !strings.Contains(out, "sess-1") {
		t.Fatalf("expected session_id in log, got %q", out)
	}
	if !strings.Contains(out, "wire=") || !strings.Contains(out, "effective=") {
		t.Fatalf("expected wire and effective JSON in log, got %q", out)
	}
	if !strings.Contains(out, "temperature") || !strings.Contains(out, "0.7") {
		t.Fatalf("expected temperature in log, got %q", out)
	}
	if !strings.Contains(out, "local-model") {
		t.Fatalf("expected effective model in log, got %q", out)
	}
}

func TestDebugLogCallOptions_NilWire(t *testing.T) {
	t.Setenv("LLMD_DEBUG_CHAT", "1")
	t.Setenv("LLMD_DEBUG_CHAT_FILE", "")
	effective := &llmv1.CallOptions{Model: "default-model"}
	out := captureStdout(t, func() {
		debugLogCallOptions("Chat", "agent-1", "", nil, effective)
	})
	if !strings.Contains(out, "\\u003cnil\\u003e") && !strings.Contains(out, "wire=<nil>") {
		t.Fatalf("expected nil wire marker, got %q", out)
	}
	if !strings.Contains(out, "default-model") {
		t.Fatalf("expected effective model in log, got %q", out)
	}
}
