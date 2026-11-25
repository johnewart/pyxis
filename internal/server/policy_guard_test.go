package server

import (
	"context"
	"errors"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/policyhook"
)

// An erroring hook is allowed when the host's Guard is FailOpen, and denied
// for a bare Hooks value (the server wraps it fail-closed).
func TestCompletePolicyFailModeFromGuard(t *testing.T) {
	ctx := testScope.IncomingContext(context.Background())
	failing := policyhook.Funcs{CheckLLMCallFn: func(context.Context, policyhook.LLMCallRequest) (policyhook.Verdict, error) {
		return policyhook.Verdict{}, errors.New("hook down")
	}}

	s, be := newCompleteTestServer(t)
	s.PolicyHooks = policyhook.NewGuard(failing, policyhook.FailOpen, nil, nil)
	if _, err := s.Complete(ctx, &llmv1.CompleteRequest{ProviderId: "claude", Messages: userMsgs()}); err != nil {
		t.Fatalf("Complete under FailOpen: %v", err)
	}
	if be.calls != 1 {
		t.Fatalf("backend calls = %d, want 1", be.calls)
	}

	s2, be2 := newCompleteTestServer(t)
	s2.PolicyHooks = failing
	if _, err := s2.Complete(ctx, &llmv1.CompleteRequest{ProviderId: "claude", Messages: userMsgs()}); err == nil {
		t.Fatal("Complete with bare hooks should fail closed")
	}
	if be2.calls != 0 {
		t.Fatalf("backend calls = %d, want 0", be2.calls)
	}
}

func TestPolicyCheckPolicyUsesOperatorDefault(t *testing.T) {
	s, _ := newCompleteTestServer(t)
	def := &policyhook.CheckPolicy{Cadence: policyhook.CadenceFirstRound}
	s.DefaultCheckPolicy = def
	s.PolicyHooks = policyhook.Funcs{GetCheckPolicyFn: func(context.Context) (policyhook.HookPolicies, error) {
		return policyhook.HookPolicies{}, errors.New("down")
	}}
	got := s.policyCheckPolicy(context.Background(), func(h policyhook.HookPolicies) *policyhook.CheckPolicy { return h.LLMCall })
	if got != def {
		t.Fatalf("fallback = %+v, want operator default", got)
	}
}
