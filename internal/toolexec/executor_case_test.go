package toolexec

import (
	"context"
	"sync/atomic"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/toolseam"
)

type stubExecutorDispatcher struct {
	calls   atomic.Int32
	content string
	success bool
	errMsg  string
}

var _ toolseam.ExecutorDispatcher = (*stubExecutorDispatcher)(nil)

func (s *stubExecutorDispatcher) Call(ctx context.Context, def *llmv1.ToolDefinition, inv toolseam.Invocation) (string, bool, string) {
	s.calls.Add(1)
	return s.content, s.success, s.errMsg
}

func executorDef() *llmv1.ToolDefinition {
	// Unclassified, non-core name: IsMutationTool treats it as a mutation.
	return &llmv1.ToolDefinition{
		Name:      "run_command_test",
		Execution: &llmv1.ToolDefinition_Executor{Executor: &llmv1.ToolExecutionExecutor{}},
	}
}

func TestExecutorCaseReadOnlyRefusedWithoutDispatch(t *testing.T) {
	stub := &stubExecutorDispatcher{content: "x", success: true}
	h := &Handlers{}
	h.SetExecutor(stub)
	content, ok, errMsg := RunWithHandlers(context.Background(), executorDef(), Invocation{Name: "run_command_test", ReadOnly: true}, h)
	if ok || content != ReadOnlyMutationError || errMsg != ReadOnlyMutationError {
		t.Fatalf("got (%q,%v,%q), want read-only refusal", content, ok, errMsg)
	}
	if n := stub.calls.Load(); n != 0 {
		t.Fatalf("stub called %d times, want 0", n)
	}
}

func TestExecutorCaseDispatchesVerbatim(t *testing.T) {
	stub := &stubExecutorDispatcher{content: "result-body", success: false, errMsg: "remote failure"}
	h := &Handlers{}
	h.SetExecutor(stub)
	content, ok, errMsg := RunWithHandlers(context.Background(), executorDef(), Invocation{Name: "run_command_test"}, h)
	if content != "result-body" || ok != false || errMsg != "remote failure" {
		t.Fatalf("got (%q,%v,%q), want stub triple", content, ok, errMsg)
	}
	if n := stub.calls.Load(); n != 1 {
		t.Fatalf("stub called %d times, want 1", n)
	}
}

type captureExecutorDispatcher struct{ got toolseam.Invocation }

func (c *captureExecutorDispatcher) Call(ctx context.Context, def *llmv1.ToolDefinition, inv toolseam.Invocation) (string, bool, string) {
	c.got = inv
	return "ok", true, ""
}

func TestExecutorCaseReceivesToolContext(t *testing.T) {
	cap := &captureExecutorDispatcher{}
	h := &Handlers{}
	h.SetExecutor(cap)
	inv := Invocation{Name: "run_command_test", ToolContext: map[string]string{"k": "v"}}
	if _, ok, errMsg := RunWithHandlers(context.Background(), executorDef(), inv, h); !ok {
		t.Fatalf("errMsg=%q", errMsg)
	}
	if cap.got.ToolContext["k"] != "v" {
		t.Fatalf("executor saw ToolContext=%v", cap.got.ToolContext)
	}
}

func TestExecutorCaseNotConfigured(t *testing.T) {
	for name, h := range map[string]*Handlers{"fresh": {}, "nil": nil} {
		content, ok, errMsg := RunWithHandlers(context.Background(), executorDef(), Invocation{Name: "run_command_test"}, h)
		if ok || content != ExecutorNotConfiguredError || errMsg != ExecutorNotConfiguredError {
			t.Fatalf("%s: got (%q,%v,%q), want not-configured", name, content, ok, errMsg)
		}
	}
}
