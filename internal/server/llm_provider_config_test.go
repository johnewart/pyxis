package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/store"
)

func newTestLlmServerWithDB(t *testing.T) *LlmServer {
	t.Helper()
	db := store.NewTestSQLite(t)
	repo := store.NewLlmProviderRepo(db)
	return &LlmServer{Providers: providers.NewComposite(nil, repo)}
}

func TestLlmServer_GetLlmProviderConfig_RoundTripsRegisteredAnthropic(t *testing.T) {
	ctx := testScope.IncomingContext(context.Background())
	s := newTestLlmServerWithDB(t)

	if _, err := s.RegisterLlmProvider(ctx, &llmv1.RegisterLlmProviderRequest{
		ProviderId: "my-anthropic",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_ANTHROPIC,
		Config: &llmv1.RegisterLlmProviderRequest_Anthropic{Anthropic: &llmv1.AnthropicLlmConfig{
			ApiKey:       "sk-ant-xyz",
			BaseUrl:      strPtrTest("https://example.test"),
			DefaultModel: strPtrTest("claude-x"),
		}},
	}); err != nil {
		t.Fatalf("RegisterLlmProvider: %v", err)
	}

	resp, err := s.GetLlmProviderConfig(ctx, &llmv1.GetLlmProviderConfigRequest{ProviderId: "my-anthropic"})
	if err != nil {
		t.Fatalf("GetLlmProviderConfig: %v", err)
	}
	if resp.GetType() != llmv1.LlmProviderType_LLM_PROVIDER_TYPE_ANTHROPIC {
		t.Fatalf("type = %v, want ANTHROPIC", resp.GetType())
	}
	a := resp.GetAnthropic()
	if a == nil {
		t.Fatalf("expected anthropic config in response, got %+v", resp)
	}
	if a.GetApiKey() != "sk-ant-xyz" {
		t.Errorf("api_key = %q, want sk-ant-xyz", a.GetApiKey())
	}
	if a.GetBaseUrl() != "https://example.test" {
		t.Errorf("base_url = %q, want https://example.test", a.GetBaseUrl())
	}
	if a.GetDefaultModel() != "claude-x" {
		t.Errorf("default_model = %q, want claude-x", a.GetDefaultModel())
	}
}

func TestLlmServer_GetLlmProviderConfig_UnknownProviderIsNotFound(t *testing.T) {
	s := newTestLlmServerWithDB(t)
	_, err := s.GetLlmProviderConfig(testScope.IncomingContext(context.Background()), &llmv1.GetLlmProviderConfigRequest{ProviderId: "nope"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestLlmServer_UpdateLlmProviderConfig_UnknownProviderIsNotFound(t *testing.T) {
	s := newTestLlmServerWithDB(t)
	_, err := s.UpdateLlmProviderConfig(testScope.IncomingContext(context.Background()), &llmv1.UpdateLlmProviderConfigRequest{
		ProviderId: "nope",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENAI,
		Config:     &llmv1.UpdateLlmProviderConfigRequest_Openai{Openai: &llmv1.OpenAiLlmConfig{ApiKey: "sk-test"}},
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestLlmServer_UpdateLlmProviderConfig_ReplacesExistingConfig(t *testing.T) {
	ctx := testScope.IncomingContext(context.Background())
	s := newTestLlmServerWithDB(t)

	if _, err := s.RegisterLlmProvider(ctx, &llmv1.RegisterLlmProviderRequest{
		ProviderId: "my-openai",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENAI,
		Config: &llmv1.RegisterLlmProviderRequest_Openai{Openai: &llmv1.OpenAiLlmConfig{
			ApiKey:  "sk-old",
			BaseUrl: strPtrTest("https://old.example.test"),
		}},
	}); err != nil {
		t.Fatalf("RegisterLlmProvider: %v", err)
	}

	if _, err := s.UpdateLlmProviderConfig(ctx, &llmv1.UpdateLlmProviderConfigRequest{
		ProviderId: "my-openai",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENAI,
		Config: &llmv1.UpdateLlmProviderConfigRequest_Openai{Openai: &llmv1.OpenAiLlmConfig{
			ApiKey:  "sk-new",
			BaseUrl: strPtrTest("https://new.example.test"),
		}},
	}); err != nil {
		t.Fatalf("UpdateLlmProviderConfig: %v", err)
	}

	resp, err := s.GetLlmProviderConfig(ctx, &llmv1.GetLlmProviderConfigRequest{ProviderId: "my-openai"})
	if err != nil {
		t.Fatalf("GetLlmProviderConfig: %v", err)
	}
	o := resp.GetOpenai()
	if o == nil || o.GetApiKey() != "sk-new" || o.GetBaseUrl() != "https://new.example.test" {
		t.Fatalf("unexpected openai config after update: %+v", o)
	}
}

func strPtrTest(s string) *string { return &s }
