package policyhook

import (
	"context"
	"errors"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestNoopAllowsAll(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []FailMode{FailClosed, FailOpen} {
		g := NewGuard(Noop{}, mode, nil, nil)
		v, err := g.CheckLLMCall(ctx, LLMCallRequest{})
		if err != nil || v.Decision != DecisionAllow {
			t.Fatalf("mode %v CheckLLMCall = %+v, %v", mode, v, err)
		}
		tv, err := g.CheckToolCall(ctx, ToolCallRequest{Name: "x"})
		if err != nil || tv.Decision != DecisionAllow {
			t.Fatalf("mode %v CheckToolCall = %+v, %v", mode, tv, err)
		}
		fr, err := g.FilterTools(ctx, FilterToolsRequest{Tools: []*llmv1.ToolDefinition{{Name: "a"}, {Name: "b"}}})
		if err != nil || len(fr.AllowedToolNames) != 2 {
			t.Fatalf("mode %v FilterTools = %+v, %v", mode, fr, err)
		}
	}
}

func TestGuardFailClosed(t *testing.T) {
	f := &fakeServer{decision: llmv1.Decision_DECISION_ALLOW, delay: 2 * time.Second}
	c := NewGRPCClient(dialFake(t, f), 50*time.Millisecond)
	g := NewGuard(c, FailClosed, nil, nil)
	v, err := g.CheckLLMCall(context.Background(), LLMCallRequest{})
	if err != nil {
		t.Fatalf("guard must convert hook failure into a decision, got err %v", err)
	}
	if v.Decision != DecisionDeny {
		t.Fatalf("decision = %v, want DENY", v.Decision)
	}

	// A plain erroring Funcs hook is also denied.
	boom := Funcs{CheckLLMCallFn: func(context.Context, LLMCallRequest) (Verdict, error) {
		return Verdict{}, errors.New("boom")
	}}
	v, err = NewGuard(boom, FailClosed, nil, nil).CheckLLMCall(context.Background(), LLMCallRequest{})
	if err != nil || v.Decision != DecisionDeny {
		t.Fatalf("erroring hook = %+v, %v", v, err)
	}
}

func TestGuardFailOpen(t *testing.T) {
	f := &fakeServer{decision: llmv1.Decision_DECISION_DENY, delay: 2 * time.Second}
	c := NewGRPCClient(dialFake(t, f), 50*time.Millisecond)
	g := NewGuard(c, FailOpen, nil, nil)
	v, err := g.CheckLLMCall(context.Background(), LLMCallRequest{})
	if err != nil || v.Decision != DecisionAllow {
		t.Fatalf("open mode timeout = %+v, %v; want ALLOW", v, err)
	}
	tv, err := g.CheckToolCall(context.Background(), ToolCallRequest{Name: "x"})
	if err != nil || tv.Decision != DecisionAllow {
		t.Fatalf("open mode tool call = %+v, %v", tv, err)
	}
}

func TestGuardExplicitDenyNotOverriddenByOpen(t *testing.T) {
	h := Funcs{CheckLLMCallFn: func(context.Context, LLMCallRequest) (Verdict, error) {
		return Verdict{Decision: DecisionDeny, Reason: "no"}, nil
	}}
	v, err := NewGuard(h, FailOpen, nil, nil).CheckLLMCall(context.Background(), LLMCallRequest{})
	if err != nil || v.Decision != DecisionDeny {
		t.Fatalf("explicit deny = %+v, %v", v, err)
	}
}
