package policyhook

import (
	"context"
	"errors"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func spanAttr(s sdktrace.ReadOnlySpan, key string) string {
	for _, kv := range s.Attributes() {
		if kv.Key == attribute.Key(key) {
			return kv.Value.AsString()
		}
	}
	return ""
}

func TestGuardEmitsPolicySpans(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	h := Funcs{
		CheckLLMCallFn: func(context.Context, LLMCallRequest) (Verdict, error) {
			return Verdict{Decision: DecisionDeny}, nil
		},
		CheckToolCallFn: func(context.Context, ToolCallRequest) (Verdict, error) {
			return Verdict{}, errors.New("down")
		},
		FilterToolsFn: func(context.Context, FilterToolsRequest) (FilterResult, error) {
			return FilterResult{Decision: DecisionAllow, AllowedToolNames: []string{"a"}}, nil
		},
	}
	g := NewGuard(h, FailClosed, nil, tp.Tracer("test"))
	ctx := context.Background()
	if _, err := g.CheckLLMCall(ctx, LLMCallRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CheckToolCall(ctx, ToolCallRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.FilterTools(ctx, FilterToolsRequest{Tools: []*llmv1.ToolDefinition{{Name: "a"}}}); err != nil {
		t.Fatal(err)
	}

	want := map[string][2]string{
		"policy.check_llm_call":  {"deny", "ok"},
		"policy.check_tool_call": {"deny", "error"},
		"policy.filter_tools":    {"allow", "ok"},
	}
	ended := rec.Ended()
	if len(ended) != len(want) {
		t.Fatalf("got %d spans, want %d", len(ended), len(want))
	}
	for _, s := range ended {
		w, ok := want[s.Name()]
		if !ok {
			t.Fatalf("unexpected span %q", s.Name())
		}
		if got := spanAttr(s, "policy.decision"); got != w[0] {
			t.Errorf("%s decision = %q, want %q", s.Name(), got, w[0])
		}
		if got := spanAttr(s, "policy.outcome"); got != w[1] {
			t.Errorf("%s outcome = %q, want %q", s.Name(), got, w[1])
		}
	}
}
