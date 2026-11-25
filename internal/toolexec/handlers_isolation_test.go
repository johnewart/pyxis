package toolexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestHandlers_NoProcessWideLeakage proves registration is per-Handlers-set,
// not process-wide: two independently constructed *Handlers never share
// state. This is the structural guarantee s2-phase-1 threads through exec
// and the server — two independently constructed LlmServers must never
// observe each other's registrations.
func TestHandlers_NoProcessWideLeakage(t *testing.T) {
	first := NewHandlers()
	second := NewHandlers()

	if err := first.Register("only_in_first", func(_ context.Context, _ json.RawMessage, _ Invocation) (Result, error) {
		return Result{Content: `{"ok":true}`}, nil
	}); err != nil {
		t.Fatalf("register into first: %v", err)
	}

	def := inlineBuiltinDef("only_in_first")
	inv := Invocation{ToolCallID: "c1", Name: "only_in_first"}

	// (a) dispatch through the set it was registered into succeeds.
	content, ok, errMsg := RunWithHandlers(context.Background(), def, inv, first)
	if !ok || errMsg != "" {
		t.Fatalf("dispatch through first: ok=%v errMsg=%q content=%q", ok, errMsg, content)
	}
	if content != `{"ok":true}` {
		t.Fatalf("unexpected content from first: %q", content)
	}

	// (b) dispatch through the independent second set fails: registration did
	// not leak across the two Handlers instances.
	content2, ok2, errMsg2 := RunWithHandlers(context.Background(), def, inv, second)
	if ok2 {
		t.Fatalf("expected dispatch through second (unregistered) set to fail, got content=%q", content2)
	}
	const wantSubstr = "no inline builtin handler registered"
	if !strings.Contains(errMsg2, wantSubstr) {
		t.Fatalf("errMsg=%q does not contain %q", errMsg2, wantSubstr)
	}
	if !strings.Contains(errMsg2, "only_in_first") {
		t.Fatalf("errMsg=%q should name the tool", errMsg2)
	}
}
