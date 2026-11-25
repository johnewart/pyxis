package server

import (
	"context"
	"fmt"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
)

// wantDisplayNames pins the human-friendly label for every supported provider short code.
// Deliberately exhaustive (not just spot-checked) so a new Kind added to AllKinds() without a
// DisplayName case fails this test rather than shipping an empty label to the "add provider" UI.
var wantDisplayNames = map[string]string{
	"openai":         "OpenAI",
	"anthropic":      "Anthropic",
	"ollama":         "Ollama",
	"bedrock":        "Amazon Bedrock",
	"gemini":         "Google Gemini",
	"openrouter":     "OpenRouter",
	"cursor_cli":     "Cursor CLI",
	"claude_code_v2": "Claude Code",
}

func TestLlmServer_ListSupportedLlmProviderTypes(t *testing.T) {
	s := &LlmServer{}
	resp, err := s.ListSupportedLlmProviderTypes(context.Background(), &llmv1.ListSupportedLlmProviderTypesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	types := resp.GetTypes()
	if len(types) == 0 {
		t.Fatalf("expected at least one supported provider type")
	}

	seen := make(map[string]*llmv1.SupportedLlmProviderType, len(types))
	for _, ty := range types {
		if ty.GetShortCode() == "" {
			t.Fatalf("provider type %v missing short_code", ty)
		}
		if _, dup := seen[ty.GetShortCode()]; dup {
			t.Fatalf("duplicate short_code %q in supported types", ty.GetShortCode())
		}
		if ty.GetType() == llmv1.LlmProviderType_LLM_PROVIDER_TYPE_UNSPECIFIED {
			t.Fatalf("provider type %q has unspecified proto enum", ty.GetShortCode())
		}
		seen[ty.GetShortCode()] = ty
	}

	// Every short code the registry can parse must be advertised, and vice versa: what we
	// advertise must round-trip back through ParseKind to the same Kind.
	for code, wantName := range wantDisplayNames {
		ty, ok := seen[code]
		if !ok {
			t.Errorf("expected short_code %q in ListSupportedLlmProviderTypes, got %v", code, keys(seen))
			continue
		}
		if ty.GetDisplayName() != wantName {
			t.Errorf("short_code %q: display_name = %q, want %q", code, ty.GetDisplayName(), wantName)
		}
		kind, ok := providerkind.Parse(code)
		if !ok {
			t.Errorf("short_code %q does not round-trip through providerkind.Parse", code)
			continue
		}
		if kind.String() != code {
			t.Errorf("providerkind.Parse(%q).String() = %q, want %q", code, kind.String(), code)
		}
	}
	if len(seen) != len(wantDisplayNames) {
		t.Errorf("ListSupportedLlmProviderTypes returned %d types, want %d (got %v)", len(seen), len(wantDisplayNames), keys(seen))
	}

	claudeCode := seen["claude_code_v2"]
	if !claudeCode.GetIsCliBacked() {
		t.Errorf("claude_code_v2 should be CLI-backed")
	}
	if claudeCode.GetRequiresApiKey() {
		t.Errorf("claude_code_v2 should not require an API key")
	}

	anthropic := seen["anthropic"]
	if !anthropic.GetRequiresApiKey() {
		t.Errorf("anthropic should require an API key")
	}
	if anthropic.GetIsCliBacked() {
		t.Errorf("anthropic should not be CLI-backed")
	}
}

// minimalRegisterRequestFor builds the smallest valid RegisterLlmProviderRequest for a supported
// provider type's short_code, so BuildProviderConfigFromRequest can turn it into a stored config.
func minimalRegisterRequestFor(providerID string, ty *llmv1.SupportedLlmProviderType) (*llmv1.RegisterLlmProviderRequest, error) {
	req := &llmv1.RegisterLlmProviderRequest{ProviderId: providerID, Type: ty.GetType()}
	switch ty.GetShortCode() {
	case "openai":
		req.Config = &llmv1.RegisterLlmProviderRequest_Openai{Openai: &llmv1.OpenAiLlmConfig{ApiKey: "sk-test"}}
	case "anthropic":
		req.Config = &llmv1.RegisterLlmProviderRequest_Anthropic{Anthropic: &llmv1.AnthropicLlmConfig{ApiKey: "sk-ant-test"}}
	case "ollama":
		req.Config = &llmv1.RegisterLlmProviderRequest_Ollama{Ollama: &llmv1.OllamaLlmConfig{}}
	case "cursor_cli":
		req.Config = &llmv1.RegisterLlmProviderRequest_CursorCli{CursorCli: &llmv1.CursorCliLlmConfig{}}
	case "claude_code_v2":
		req.Config = &llmv1.RegisterLlmProviderRequest_ClaudeCode{ClaudeCode: &llmv1.ClaudeCodeLlmConfig{}}
	case "bedrock":
		req.Config = &llmv1.RegisterLlmProviderRequest_Bedrock{Bedrock: &llmv1.BedrockLlmConfig{Region: "us-east-1"}}
	case "gemini":
		req.Config = &llmv1.RegisterLlmProviderRequest_Gemini{Gemini: &llmv1.GeminiLlmConfig{ApiKey: "gemini-test"}}
	case "openrouter":
		req.Config = &llmv1.RegisterLlmProviderRequest_Openrouter{Openrouter: &llmv1.OpenRouterLlmConfig{ApiKey: "or-test"}}
	default:
		return nil, fmt.Errorf("no minimal config wired up for short_code %q in this test", ty.GetShortCode())
	}
	return req, nil
}

// TestLlmServer_SupportedProviderTypes_CanRegister exercises the intended "add provider" flow
// end to end at the type level: for every short_code ListSupportedLlmProviderTypes advertises,
// building a RegisterLlmProviderRequest using that short code's own proto type must produce a
// storable config of the matching kind (BuildProviderConfigFromRequest never rejects it).
func TestLlmServer_SupportedProviderTypes_CanRegister(t *testing.T) {
	s := &LlmServer{}
	resp, err := s.ListSupportedLlmProviderTypes(context.Background(), &llmv1.ListSupportedLlmProviderTypesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, ty := range resp.GetTypes() {
		ty := ty
		t.Run(ty.GetShortCode(), func(t *testing.T) {
			providerID := "test-" + ty.GetShortCode()
			req, err := minimalRegisterRequestFor(providerID, ty)
			if err != nil {
				t.Fatal(err)
			}

			cfg, err := providers.BuildProviderConfigFromRequest(req)
			if err != nil {
				t.Fatalf("BuildProviderConfigFromRequest(%s): %v", ty.GetShortCode(), err)
			}
			if cfg.ProviderID != providerID {
				t.Errorf("cfg.ProviderID = %q, want %q", cfg.ProviderID, providerID)
			}
			// cfg.Kind is the enum now, so this asserts the remaining half of the
			// one-id rule: the kind's id is exactly the advertised short_code.
			if cfg.Kind.String() != ty.GetShortCode() {
				t.Errorf("cfg.Kind.String() = %q, want short_code %q", cfg.Kind.String(), ty.GetShortCode())
			}
		})
	}
}

func keys(m map[string]*llmv1.SupportedLlmProviderType) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
