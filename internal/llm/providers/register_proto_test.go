package providers

import (
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// TestBuildProviderConfigFromRequest_onlyMatchingKindSet pins that the resulting
// store.LlmProviderConfig has exactly one kind-specific field set — there is no blob to leak
// unused backend keys into (typed columns, not a document), so the only thing to check is that
// building from one kind's oneof branch never touches any other kind's field.
func TestBuildProviderConfigFromRequest_onlyMatchingKindSet(t *testing.T) {
	model := "gemini-3.5-flash"
	req := &llmv1.RegisterLlmProviderRequest{
		ProviderId: "gemini",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_GEMINI,
		Config: &llmv1.RegisterLlmProviderRequest_Gemini{
			Gemini: &llmv1.GeminiLlmConfig{
				ApiKey:       "secret",
				DefaultModel: &model,
			},
		},
	}
	cfg, err := BuildProviderConfigFromRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Gemini == nil {
		t.Fatalf("expected Gemini set: %+v", cfg)
	}
	if cfg.Anthropic != nil || cfg.OpenAI != nil || cfg.Ollama != nil || cfg.CursorCLI != nil ||
		cfg.ClaudeCode != nil || cfg.Bedrock != nil {
		t.Fatalf("expected only Gemini set: %+v", cfg)
	}
	if cfg.Gemini.APIKey != "secret" || cfg.Gemini.DefaultModel != model {
		t.Fatalf("gemini config = %+v", cfg.Gemini)
	}
}

func TestOpenRouter_registerUpdateAndRedactedRead(t *testing.T) {
	model := "openai/gpt-4o"
	fallbacks := false
	opts := &llmv1.OpenRouterCallOptions{
		Sort:           llmv1.OpenRouterCallOptions_SORT_PRICE,
		Only:           []string{"anthropic"},
		AllowFallbacks: &fallbacks,
	}
	or := &llmv1.OpenRouterLlmConfig{ApiKey: "or-secret", DefaultModel: &model, DefaultCallOptions: opts}
	reg := &llmv1.RegisterLlmProviderRequest{
		ProviderId: "or",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENROUTER,
		Config:     &llmv1.RegisterLlmProviderRequest_Openrouter{Openrouter: or},
	}
	cfg, err := BuildProviderConfigFromRequest(reg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OpenRouter == nil || cfg.Anthropic != nil || cfg.OpenAI != nil || cfg.Gemini != nil {
		t.Fatalf("expected only OpenRouter set: %+v", cfg)
	}
	if cfg.OpenRouter.APIKey != "or-secret" || cfg.OpenRouter.DefaultModel != model {
		t.Fatalf("row = %+v", cfg.OpenRouter)
	}

	upd, err := BuildProviderConfigFromUpdateRequest(&llmv1.UpdateLlmProviderConfigRequest{
		ProviderId: "or",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENROUTER,
		Config:     &llmv1.UpdateLlmProviderConfigRequest_Openrouter{Openrouter: or},
	})
	if err != nil {
		t.Fatal(err)
	}
	if upd.OpenRouter == nil || upd.OpenRouter.Sort != cfg.OpenRouter.Sort || len(upd.OpenRouter.ProviderOnly) != len(cfg.OpenRouter.ProviderOnly) {
		t.Fatalf("update row = %+v", upd.OpenRouter)
	}

	resp, err := BuildGetLlmProviderConfigResponse(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetType() != llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENROUTER {
		t.Fatalf("type = %v", resp.GetType())
	}
	got := resp.GetOpenrouter()
	if got == nil {
		t.Fatal("missing openrouter block")
	}
	if got.GetApiKey() != "" {
		t.Fatal("api_key must be redacted on read")
	}
	if got.GetDefaultModel() != model {
		t.Fatalf("default_model = %q", got.GetDefaultModel())
	}
	go2 := got.GetDefaultCallOptions()
	if go2.GetSort() != llmv1.OpenRouterCallOptions_SORT_PRICE || len(go2.GetOnly()) != 1 || go2.AllowFallbacks == nil || go2.GetAllowFallbacks() {
		t.Fatalf("default_call_options = %+v", go2)
	}
}

func TestOpenRouter_typeMismatch(t *testing.T) {
	_, err := BuildProviderConfigFromRequest(&llmv1.RegisterLlmProviderRequest{
		ProviderId: "or",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_GEMINI,
		Config:     &llmv1.RegisterLlmProviderRequest_Openrouter{Openrouter: &llmv1.OpenRouterLlmConfig{ApiKey: "k"}},
	})
	if err == nil {
		t.Fatal("expected type mismatch error")
	}
}

const testRoleARN = "arn:aws:iam::123456789012:role/llmd-test"

func bedrockRegisterReq(b *llmv1.BedrockLlmConfig) *llmv1.RegisterLlmProviderRequest {
	return &llmv1.RegisterLlmProviderRequest{
		ProviderId: "bedrock",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_BEDROCK,
		Config:     &llmv1.RegisterLlmProviderRequest_Bedrock{Bedrock: b},
	}
}

func strp(s string) *string { return &s }

func TestBedrockAssumeRole_badARN(t *testing.T) {
	b := &llmv1.BedrockLlmConfig{Region: "us-east-1", AssumeRoleArn: strp("not-an-arn")}
	if _, err := BuildProviderConfigFromRequest(bedrockRegisterReq(b)); err == nil {
		t.Fatal("register: expected error for bad ARN")
	}
	upd := &llmv1.UpdateLlmProviderConfigRequest{
		ProviderId: "bedrock",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_BEDROCK,
		Config:     &llmv1.UpdateLlmProviderConfigRequest_Bedrock{Bedrock: b},
	}
	if _, err := BuildProviderConfigFromUpdateRequest(upd); err == nil {
		t.Fatal("update: expected error for bad ARN")
	}
}

func TestBedrockAssumeRole_badSessionName(t *testing.T) {
	b := &llmv1.BedrockLlmConfig{Region: "us-east-1", AssumeRoleArn: strp(testRoleARN), RoleSessionName: strp("bad name!")}
	if _, err := BuildProviderConfigFromRequest(bedrockRegisterReq(b)); err == nil {
		t.Fatal("register: expected error for bad session name")
	}
	upd := &llmv1.UpdateLlmProviderConfigRequest{
		ProviderId: "bedrock",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_BEDROCK,
		Config:     &llmv1.UpdateLlmProviderConfigRequest_Bedrock{Bedrock: b},
	}
	if _, err := BuildProviderConfigFromUpdateRequest(upd); err == nil {
		t.Fatal("update: expected error for bad session name")
	}
}

func TestBedrockAssumeRole_roundTripRedactsExternalID(t *testing.T) {
	b := &llmv1.BedrockLlmConfig{
		Region:          "us-east-1",
		AssumeRoleArn:   strp(testRoleARN),
		RoleSessionName: strp("my-session"),
		ExternalId:      strp("super-secret"),
	}
	cfg, err := BuildProviderConfigFromRequest(bedrockRegisterReq(b))
	if err != nil {
		t.Fatal(err)
	}
	row := cfg.Bedrock
	if row.AssumeRoleARN != testRoleARN || row.RoleSessionName != "my-session" || row.ExternalID != "super-secret" {
		t.Fatalf("row = %+v", row)
	}
	resp, err := BuildGetLlmProviderConfigResponse(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := resp.GetBedrock()
	if got.GetAssumeRoleArn() != testRoleARN || got.GetRoleSessionName() != "my-session" {
		t.Fatalf("response = %+v", got)
	}
	if got.ExternalId != nil {
		t.Fatal("external_id must be redacted on read")
	}
}

func TestBuildProviderConfigFromRequest_cursorOnlyCursorSet(t *testing.T) {
	model := "auto"
	req := &llmv1.RegisterLlmProviderRequest{
		ProviderId: "cursor",
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CURSOR_CLI,
		Config: &llmv1.RegisterLlmProviderRequest_CursorCli{
			CursorCli: &llmv1.CursorCliLlmConfig{
				DefaultModel: &model,
			},
		},
	}
	cfg, err := BuildProviderConfigFromRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CursorCLI == nil {
		t.Fatalf("expected CursorCLI set: %+v", cfg)
	}
	if cfg.Anthropic != nil || cfg.OpenAI != nil || cfg.Ollama != nil || cfg.ClaudeCode != nil ||
		cfg.Bedrock != nil || cfg.Gemini != nil {
		t.Fatalf("expected only CursorCLI set: %+v", cfg)
	}
	if cfg.CursorCLI.DefaultModel != model {
		t.Fatalf("cursor_cli config = %+v", cfg.CursorCLI)
	}
}
