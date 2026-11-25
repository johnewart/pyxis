package toolseam

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHandlersRegisterLookupDispatch(t *testing.T) {
	h := NewHandlers()
	called := false
	err := h.Register("echo", func(ctx context.Context, args json.RawMessage, inv Invocation) (Result, error) {
		called = true
		return Result{Content: string(args)}, nil
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	fn, ok := h.Lookup("echo")
	if !ok || fn == nil {
		t.Fatalf("Lookup did not find registered handler")
	}

	inv := Invocation{ArgumentsJSON: `{"a":1}`}
	content, success, errMsg := RunInlineBuiltin(context.Background(), "echo", h, inv)
	if !success || errMsg != "" {
		t.Fatalf("RunInlineBuiltin failed: success=%v errMsg=%q", success, errMsg)
	}
	if !called {
		t.Fatalf("handler was not invoked")
	}
	if content != `{"a":1}` {
		t.Fatalf("unexpected content: %q", content)
	}

	if names := h.Names(); len(names) != 1 || names[0] != "echo" {
		t.Fatalf("unexpected Names(): %v", names)
	}

	if !h.Unregister("echo") {
		t.Fatalf("Unregister reported false for a registered handler")
	}
	if _, ok := h.Lookup("echo"); ok {
		t.Fatalf("handler still found after Unregister")
	}
}

func TestRunInlineBuiltinUnknownTool(t *testing.T) {
	h := NewHandlers()
	_, success, errMsg := RunInlineBuiltin(context.Background(), "missing", h, Invocation{})
	if success || errMsg == "" {
		t.Fatalf("expected failure for unregistered tool, got success=%v errMsg=%q", success, errMsg)
	}
}
