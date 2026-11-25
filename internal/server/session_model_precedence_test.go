package server

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
)

// The session's model (set by the model selector via UpdateSessionLlm) must
// win over the provider's default_model. Previously the provider default was
// checked first, so switching the selector to haiku still ran fable.
func TestEffectiveCallOptions_SessionModelBeatsProviderDefault(t *testing.T) {
	reg, err := providers.NewRegistryFromEntries("cc", map[string]*providers.Entry{
		"cc": {ID: "cc", Kind: providerkind.OpenAI, Backend: &captureStreamBackend{}, DefaultModel: "fable"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr := testAgentManagerWithCreate(t, &llmv1.EnsureSessionRequest{
		SessionId:     "sess_model",
		LlmProviderId: strPtr("cc"),
		DefaultModel:  strPtr("haiku"),
	})
	s := &LlmServer{Providers: providers.NewComposite(reg, nil), SessionRuntimes: mgr}
	ctx := testScope.IncomingContext(context.Background())

	if got := s.effectiveCallOptions(ctx, "sess_model", nil).GetModel(); got != "haiku" {
		t.Fatalf("model = %q, want session model haiku", got)
	}
	// An explicit per-call model still wins over everything.
	if got := s.effectiveCallOptions(ctx, "sess_model", &llmv1.CallOptions{Model: "opus"}).GetModel(); got != "opus" {
		t.Fatalf("model = %q, want explicit opus", got)
	}
}

// A session with no bound provider keeps the old behavior: the provider
// default is used (guards against a stale cross-provider session model).
func TestEffectiveCallOptions_UnboundSessionUsesProviderDefault(t *testing.T) {
	reg, err := providers.NewRegistryFromEntries("cc", map[string]*providers.Entry{
		"cc": {ID: "cc", Kind: providerkind.OpenAI, Backend: &captureStreamBackend{}, DefaultModel: "haiku"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr := testAgentManagerWithCreate(t, &llmv1.EnsureSessionRequest{SessionId: "sess_unbound"})
	s := &LlmServer{Providers: providers.NewComposite(reg, nil), SessionRuntimes: mgr}
	ctx := testScope.IncomingContext(context.Background())
	if got := s.effectiveCallOptions(ctx, "sess_unbound", nil).GetModel(); got != "haiku" {
		t.Fatalf("model = %q, want provider default haiku", got)
	}
}
