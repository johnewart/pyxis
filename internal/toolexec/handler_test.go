package toolexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func inlineBuiltinDef(name string) *llmv1.ToolDefinition {
	return &llmv1.ToolDefinition{
		Name:           name,
		Classification: llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
		Execution: &llmv1.ToolDefinition_InlineBuiltin{
			InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{},
		},
	}
}

func TestRunWithHandlers_inlineBuiltin_registered(t *testing.T) {
	handlers := NewHandlers()
	var gotArgs string
	if err := handlers.Register("echo", func(_ context.Context, args json.RawMessage, inv Invocation) (Result, error) {
		gotArgs = string(args)
		return Result{Content: `{"ok":true,"call":"` + inv.ToolCallID + `"}`}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	content, ok, errMsg := RunWithHandlers(context.Background(), inlineBuiltinDef("echo"),
		Invocation{ToolCallID: "c1", Name: "echo", ArgumentsJSON: `{"n":2}`}, handlers)
	if !ok || errMsg != "" {
		t.Fatalf("ok=%v errMsg=%q content=%q", ok, errMsg, content)
	}
	if gotArgs != `{"n":2}` {
		t.Fatalf("handler saw args %q", gotArgs)
	}
	if content != `{"ok":true,"call":"c1"}` {
		t.Fatalf("content=%q", content)
	}
}

func TestRunWithHandlers_inlineBuiltin_unregistered(t *testing.T) {
	content, ok, errMsg := RunWithHandlers(context.Background(), inlineBuiltinDef("nope"),
		Invocation{ToolCallID: "c1", Name: "nope"}, NewHandlers())
	if ok {
		t.Fatalf("expected failure, content=%q", content)
	}
	if errMsg == "" || !strings.Contains(errMsg, "nope") {
		t.Fatalf("errMsg=%q", errMsg)
	}
}

func TestRunWithHandlers_inlineBuiltin_handlerError(t *testing.T) {
	handlers := NewHandlers()
	_ = handlers.Register("boom", func(context.Context, json.RawMessage, Invocation) (Result, error) {
		return Result{}, fmt.Errorf("handler exploded")
	})
	content, ok, errMsg := RunWithHandlers(context.Background(), inlineBuiltinDef("boom"),
		Invocation{ToolCallID: "c1", Name: "boom"}, handlers)
	if ok || errMsg != "handler exploded" || content != "handler exploded" {
		t.Fatalf("ok=%v errMsg=%q content=%q", ok, errMsg, content)
	}
}

func TestRunWithHandlers_inlineBuiltin_invalidArgsJSON(t *testing.T) {
	handlers := NewHandlers()
	called := false
	_ = handlers.Register("t", func(context.Context, json.RawMessage, Invocation) (Result, error) {
		called = true
		return Result{}, nil
	})
	_, ok, errMsg := RunWithHandlers(context.Background(), inlineBuiltinDef("t"),
		Invocation{ToolCallID: "c1", Name: "t", ArgumentsJSON: "{not json"}, handlers)
	if ok || errMsg == "" {
		t.Fatalf("ok=%v errMsg=%q", ok, errMsg)
	}
	if called {
		t.Fatalf("handler must not run on invalid arguments JSON")
	}
}

func TestRun_inlineBuiltin_unregisteredByDefault(t *testing.T) {
	// A fresh, empty handler set has nothing registered.
	_, ok, errMsg := RunWithHandlers(context.Background(), inlineBuiltinDef("unregistered_builtin"),
		Invocation{ToolCallID: "c1", Name: "unregistered_builtin"}, NewHandlers())
	if ok || errMsg == "" {
		t.Fatalf("ok=%v errMsg=%q", ok, errMsg)
	}
}

func TestHandlers_registerValidationAndNames(t *testing.T) {
	h := NewHandlers()
	if err := h.Register("", func(context.Context, json.RawMessage, Invocation) (Result, error) { return Result{}, nil }); err == nil {
		t.Fatal("expected empty-name error")
	}
	if err := h.Register("x", nil); err == nil {
		t.Fatal("expected nil-handler error")
	}
	_ = h.Register("b", func(context.Context, json.RawMessage, Invocation) (Result, error) { return Result{}, nil })
	_ = h.Register("a", func(context.Context, json.RawMessage, Invocation) (Result, error) { return Result{}, nil })
	names := h.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("names=%v", names)
	}
	if !h.Unregister("a") || h.Unregister("a") {
		t.Fatal("unregister semantics")
	}
	if _, ok := h.Lookup("a"); ok {
		t.Fatal("unregistered tool must be unknown")
	}
}

// TestRunWithHandlers_noProcessWideLeakage constructs two independent
// *Handlers sets via NewHandlers(), registers a tool handler in only the
// first, and asserts dispatch through the first succeeds while dispatch of
// the identical tool through the second fails — proving registration is
// per-set, not process-wide (there is no global handler set to leak into).
func TestRunWithHandlers_noProcessWideLeakage(t *testing.T) {
	handlersA := NewHandlers()
	handlersB := NewHandlers()

	if err := handlersA.Register("only_in_a", func(context.Context, json.RawMessage, Invocation) (Result, error) {
		return Result{Content: `{"ok":true}`}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	def := inlineBuiltinDef("only_in_a")
	inv := Invocation{ToolCallID: "c1", Name: "only_in_a"}

	content, ok, errMsg := RunWithHandlers(context.Background(), def, inv, handlersA)
	if !ok || errMsg != "" {
		t.Fatalf("dispatch through handlersA: ok=%v errMsg=%q content=%q", ok, errMsg, content)
	}

	content, ok, errMsg = RunWithHandlers(context.Background(), def, inv, handlersB)
	if ok {
		t.Fatalf("dispatch through handlersB unexpectedly succeeded: content=%q", content)
	}
	wantMsg := `no inline builtin handler registered for tool "only_in_a"`
	if errMsg != wantMsg {
		t.Fatalf("errMsg=%q, want %q", errMsg, wantMsg)
	}
}
