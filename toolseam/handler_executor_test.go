package toolseam

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

type stubExecutor struct{ id int }

func (s *stubExecutor) Call(context.Context, *llmv1.ToolDefinition, Invocation) (string, bool, string) {
	return "", true, ""
}

func TestHandlersExecutorRoundTrip(t *testing.T) {
	h := NewHandlers()
	if h.Executor() != nil {
		t.Fatal("fresh Handlers should have nil executor")
	}
	s := &stubExecutor{id: 1}
	h.SetExecutor(s)
	if got := h.Executor(); got != ExecutorDispatcher(s) {
		t.Fatalf("Executor() = %v, want %v", got, s)
	}
	var nilH *Handlers
	if nilH.Executor() != nil {
		t.Fatal("nil Handlers should return nil executor")
	}
	if OriginUnspecified != 0 || OriginMCP.String() != "mcp" {
		t.Fatal("unexpected Origin values")
	}
}
