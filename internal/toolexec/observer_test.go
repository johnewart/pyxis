package toolexec

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/toolseam"
)

type observed struct {
	tool    string
	origin  toolseam.Origin
	binding toolseam.Binding
	result  toolseam.InvocationResult
}

type recordingObserver struct {
	mu  sync.Mutex
	got []observed
}

func (r *recordingObserver) Begin(tool string, origin toolseam.Origin, binding toolseam.Binding) func(toolseam.InvocationResult) {
	return func(res toolseam.InvocationResult) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.got = append(r.got, observed{tool, origin, binding, res})
	}
}

func (r *recordingObserver) only(t *testing.T) observed {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.got) != 1 {
		t.Fatalf("observer saw %d dispatches, want 1", len(r.got))
	}
	return r.got[0]
}

func inlineDef(name string) *llmv1.ToolDefinition {
	return &llmv1.ToolDefinition{
		Name:      name,
		Execution: &llmv1.ToolDefinition_InlineBuiltin{InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{}},
	}
}

func TestRunWithHandlers_reportsToObserver(t *testing.T) {
	cases := []struct {
		name        string
		def         *llmv1.ToolDefinition
		ctx         func() context.Context
		handler     toolseam.Handler
		wantTool    string
		wantBinding toolseam.Binding
		wantOutcome toolseam.Outcome
	}{
		{
			name: "success", def: inlineDef("echo"),
			handler: func(context.Context, json.RawMessage, toolseam.Invocation) (toolseam.Result, error) {
				return toolseam.Result{Content: "hello"}, nil
			},
			wantTool: "echo", wantBinding: toolseam.BindingInline, wantOutcome: toolseam.OutcomeOK,
		},
		{
			name: "canceled context", def: inlineDef("echo"),
			ctx: func() context.Context { c, cancel := context.WithCancel(context.Background()); cancel(); return c },
			handler: func(ctx context.Context, _ json.RawMessage, _ toolseam.Invocation) (toolseam.Result, error) {
				return toolseam.Result{}, ctx.Err()
			},
			wantTool: "echo", wantBinding: toolseam.BindingInline, wantOutcome: toolseam.OutcomeCanceled,
		},
		{
			name: "unregistered tool", def: nil,
			wantTool: "", wantBinding: toolseam.BindingNone, wantOutcome: toolseam.OutcomeNotRegistered,
		},
		{
			name: "no execution binding", def: &llmv1.ToolDefinition{Name: "bare"},
			wantTool: "bare", wantBinding: toolseam.BindingNone, wantOutcome: toolseam.OutcomeNotConfigured,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := toolseam.NewHandlers()
			if c.handler != nil {
				if err := h.Register("echo", c.handler); err != nil {
					t.Fatalf("register: %v", err)
				}
			}
			obs := &recordingObserver{}
			h.SetObserver(obs)
			ctx := context.Background()
			if c.ctx != nil {
				ctx = c.ctx()
			}
			content, _, _ := RunWithHandlers(ctx, c.def, Invocation{Name: "model-made-up-name", Origin: toolseam.OriginMCP}, h)

			got := obs.only(t)
			if got.tool != c.wantTool || got.binding != c.wantBinding || got.result.Outcome != c.wantOutcome {
				t.Fatalf("observed (tool=%q binding=%q outcome=%q), want (%q, %q, %q)",
					got.tool, got.binding, got.result.Outcome, c.wantTool, c.wantBinding, c.wantOutcome)
			}
			if got.origin != toolseam.OriginMCP {
				t.Errorf("origin = %v, want mcp", got.origin)
			}
			if got.result.ResultBytes != len(content) {
				t.Errorf("ResultBytes = %d, want %d", got.result.ResultBytes, len(content))
			}
		})
	}
}

// Without an observer, dispatch behaves exactly as before.
func TestRunWithHandlers_noObserver(t *testing.T) {
	h := toolseam.NewHandlers()
	if err := h.Register("echo", func(context.Context, json.RawMessage, toolseam.Invocation) (toolseam.Result, error) {
		return toolseam.Result{Content: "hi"}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if content, ok, _ := RunWithHandlers(context.Background(), inlineDef("echo"), Invocation{}, h); !ok || content != "hi" {
		t.Fatalf("RunWithHandlers = (%q, %v), want (\"hi\", true)", content, ok)
	}
}
