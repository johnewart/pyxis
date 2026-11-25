package llmdhost

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/backend"
	"github.com/johnewart/pyxis/internal/policyhook"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
)

// countingBackend counts calls that reach the provider.
type countingBackend struct {
	backend.Backend
	calls atomic.Int32
}

func (b *countingBackend) Chat(context.Context, []*llmv1.Message, *llmv1.CallOptions, []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	b.calls.Add(1)
	return &llmv1.AssistantReply{Content: "pong"}, nil
}

func newPolicyTestHost(t *testing.T, hooks policyhook.Hooks) *Host {
	t.Helper()
	dir := t.TempDir()
	h, err := New(Config{
		DatabaseDSN:         filepath.Join(dir, "llmd.sqlite"),
		KnowledgeSQLitePath: filepath.Join(dir, "knowledge.sqlite"),
		RuntimeIdleEvict:    -1,
		PolicyHooks:         hooks,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func TestHostPassesPolicyGuardToServer(t *testing.T) {
	h := newPolicyTestHost(t, policyhook.Funcs{CheckLLMCallFn: func(context.Context, policyhook.LLMCallRequest) (policyhook.Verdict, error) {
		return policyhook.Verdict{Decision: policyhook.DecisionDeny, Reason: "no"}, nil
	}})
	if h.llm.PolicyHooks == nil {
		t.Fatal("LlmServer.PolicyHooks is nil with a hook configured")
	}
	if g, ok := h.llm.PolicyHooks.(*policyhook.Guard); !ok || g != h.Hooks() {
		t.Fatalf("LlmServer.PolicyHooks = %T, want the host guard", h.llm.PolicyHooks)
	}

	sc := serverutil.Scope{TenantID: "test_tenant"}
	ctx := sc.IncomingContext(context.Background())
	model := "claude-x"
	if _, err := h.llm.RegisterLlmProvider(ctx, &llmv1.RegisterLlmProviderRequest{
		ProviderId: "claude",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_ANTHROPIC,
		Config: &llmv1.RegisterLlmProviderRequest_Anthropic{Anthropic: &llmv1.AnthropicLlmConfig{
			ApiKey:       "sk-ant-test",
			DefaultModel: &model,
		}},
	}); err != nil {
		t.Fatalf("RegisterLlmProvider: %v", err)
	}
	if err := store.NewLlmProviderModelRepo(h.db).Replace(context.Background(), sc, "claude",
		[]store.ProviderModel{{ModelKey: "claude-x", Label: "X"}}, time.Now().UTC()); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	e, ok := h.llm.Providers.Get(ctx, sc, "claude")
	if !ok || e == nil {
		t.Fatal("provider claude not resolvable")
	}
	be := &countingBackend{Backend: e.Backend}
	e.Backend = be

	_, err := h.llm.Complete(ctx, &llmv1.CompleteRequest{
		ProviderId: "claude",
		Messages:   []*llmv1.Message{{Role: "user", Content: "ping", Id: "u1"}},
	})
	if c := status.Code(err); c != codes.PermissionDenied {
		t.Fatalf("Complete code = %v (%v), want PermissionDenied", c, err)
	}
	if n := be.calls.Load(); n != 0 {
		t.Fatalf("provider backend called %d times, want 0", n)
	}
}

func TestHostNoHookLeavesServerPolicyNil(t *testing.T) {
	h := newPolicyTestHost(t, nil)
	if h.llm.PolicyHooks != nil {
		t.Fatal("PolicyHooks should stay nil when no hook is configured")
	}
}
