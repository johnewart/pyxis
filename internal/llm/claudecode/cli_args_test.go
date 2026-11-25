package claudecodeclillm

import (
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/protobuf/proto"
)

func TestPickClaudeModelPrefersBaseModel(t *testing.T) {
	opt := &llmv1.CallOptions{
		Model: "profile-sonnet-low",
		ClaudeCode: &llmv1.ClaudeCodeCallOptions{
			BaseModel: proto.String("claude-sonnet-4-5-20250929"),
		},
	}
	got := pickClaudeModel(opt, "default-model")
	if got != "claude-sonnet-4-5-20250929" {
		t.Fatalf("pickClaudeModel = %q, want base model", got)
	}
}

// A stale base_model (e.g. fable) must not override the session/default model
// the user actually picked.
func TestPickClaudeModelSessionModelBeatsStaleBaseModel(t *testing.T) {
	opt := &llmv1.CallOptions{
		Model:      "opus-high-enabled",
		ClaudeCode: &llmv1.ClaudeCodeCallOptions{BaseModel: proto.String("fable")},
	}
	if got := pickClaudeModel(opt, ""); got != "opus" {
		t.Fatalf("pickClaudeModel = %q, want opus", got)
	}
}

// The session selector may send a canonical "provider/model" id. Picking haiku
// there must yield haiku even when a stale fable base_model rides along.
func TestPickClaudeModelCanonicalIDBeatsStaleBaseModel(t *testing.T) {
	for _, model := range []string{"claude-code/haiku", "my-provider/haiku-low-disabled", "haiku"} {
		opt := &llmv1.CallOptions{
			Model:      model,
			ClaudeCode: &llmv1.ClaudeCodeCallOptions{BaseModel: proto.String("fable")},
		}
		if got := pickClaudeModel(opt, "fable"); got != "haiku" {
			t.Errorf("pickClaudeModel(%q) = %q, want haiku", model, got)
		}
	}
}

// Anything unrecognized resolves to haiku — never passed through, never
// "let the CLI decide".
func TestPickClaudeModelUnrecognizedFallsToHaiku(t *testing.T) {
	for _, tc := range []struct {
		name     string
		opt      *llmv1.CallOptions
		fallback string
	}{
		{"garbage model", &llmv1.CallOptions{Model: "gpt-4o"}, "opus"},
		{"garbage base model", &llmv1.CallOptions{ClaudeCode: &llmv1.ClaudeCodeCallOptions{BaseModel: proto.String("bogus")}}, "opus"},
		{"garbage provider default", nil, "not-a-model"},
	} {
		if got := pickClaudeModel(tc.opt, tc.fallback); got != claudeSafeDefaultModel {
			t.Errorf("%s: pickClaudeModel = %q, want %q", tc.name, got, claudeSafeDefaultModel)
		}
	}
}

// Defaults must never be fable and never empty: fall back to haiku.
func TestPickClaudeModelNeverDefaultsToFable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		opt      *llmv1.CallOptions
		fallback string
	}{
		{"nil opts, no default", nil, ""},
		{"empty opts, no default", &llmv1.CallOptions{}, ""},
		{"provider default fable", &llmv1.CallOptions{}, "fable"},
		{"provider default pinned fable", nil, "claude-fable-5"},
	} {
		if got := pickClaudeModel(tc.opt, tc.fallback); got != claudeSafeDefaultModel {
			t.Errorf("%s: pickClaudeModel = %q, want %q", tc.name, got, claudeSafeDefaultModel)
		}
	}
	if got := pickClaudeModel(nil, "opus"); got != "opus" {
		t.Errorf("non-fable provider default = %q, want opus", got)
	}
}

func TestPickClaudeModelInfersFromProfileSlug(t *testing.T) {
	opt := &llmv1.CallOptions{Model: "sonnet-low-default-usd0_50"}
	got := pickClaudeModel(opt, "default-model")
	if got != "sonnet" {
		t.Fatalf("pickClaudeModel = %q, want sonnet from profile slug", got)
	}
}

// Fable profile slugs must resolve to a base model like every other tier. When
// "fable" was missing from claudeBaseModelPrefixes, inference returned "" and the
// turn silently fell back to the provider default — the user asked for Fable and
// got Sonnet, with nothing in the logs to say so.
func TestPickClaudeModelInfersFableFromProfileSlug(t *testing.T) {
	for _, tc := range []struct {
		slug string
		want string
	}{
		{"fable-high-enabled", "fable"},
		{"fable", "fable"},
		{"claude-fable-5-low-default", "claude-fable-5"},
		{"claude-fable-5", "claude-fable-5"},
	} {
		opt := &llmv1.CallOptions{Model: tc.slug}
		if got := pickClaudeModel(opt, "default-model"); got != tc.want {
			t.Errorf("pickClaudeModel(%q) = %q, want %q", tc.slug, got, tc.want)
		}
	}
}

func TestPickClaudeEffortInfersFromProfileSlug(t *testing.T) {
	for _, tc := range []struct {
		slug string
		want string
	}{
		{"opus-low-enabled", "low"},
		{"opus-medium-disabled", "medium"},
		{"opus", ""},
		{"unknown-slug", ""},
	} {
		opt := &llmv1.CallOptions{Model: tc.slug}
		if got := pickClaudeEffort(opt, ""); got != tc.want {
			t.Errorf("pickClaudeEffort(%q) = %q, want %q", tc.slug, got, tc.want)
		}
	}
}

func TestPickClaudeThinkingTypeInfersFromProfileSlug(t *testing.T) {
	for _, tc := range []struct {
		slug string
		want string
	}{
		{"opus-low-enabled", "enabled"},
		{"opus-medium-disabled", "disabled"},
		{"opus", ""},
	} {
		opt := &llmv1.CallOptions{Model: tc.slug}
		if got := pickClaudeThinkingType(opt, ""); got != tc.want {
			t.Errorf("pickClaudeThinkingType(%q) = %q, want %q", tc.slug, got, tc.want)
		}
	}
}

func TestPickClaudeEffortExplicitCallOptionsWinOverProfileSlug(t *testing.T) {
	opt := &llmv1.CallOptions{
		Model:      "opus-low-enabled",
		ClaudeCode: &llmv1.ClaudeCodeCallOptions{Effort: proto.String("max")},
	}
	if got := pickClaudeEffort(opt, ""); got != "max" {
		t.Fatalf("pickClaudeEffort = %q, want explicit override to win", got)
	}
}

// Strict MCP config defaults to on. A nil pointer (provider never configured it,
// or an older row with NULL in the DB) must still produce --strict-mcp-config;
// only an explicit false may drop it, otherwise the checkbox cannot be turned off.
func TestStrictMcpConfigDefaultsOn(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  *bool
		want bool
	}{
		{"unset defaults to strict", nil, true},
		{"explicit true is strict", proto.Bool(true), true},
		{"explicit false opts out", proto.Bool(false), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := appendClaudeProviderCLIArgs(nil, Config{StrictMcpConfig: tc.val}, nil)
			if got := claudeCLIHasArg(args, "--strict-mcp-config"); got != tc.want {
				t.Fatalf("--strict-mcp-config present = %v, want %v (args: %v)", got, tc.want, args)
			}
		})
	}
}

func TestPickClaudeEffortFromCallOptions(t *testing.T) {
	opt := &llmv1.CallOptions{
		ClaudeCode: &llmv1.ClaudeCodeCallOptions{Effort: proto.String("xhigh")},
	}
	got := pickClaudeEffort(opt, "low")
	if got != "xhigh" {
		t.Fatalf("pickClaudeEffort = %q", got)
	}
}

func TestPickClaudeThinkingTypeFromCallOptions(t *testing.T) {
	opt := &llmv1.CallOptions{
		ClaudeCode: &llmv1.ClaudeCodeCallOptions{ThinkingType: proto.String("adaptive")},
	}
	got := pickClaudeThinkingType(opt, "disabled")
	if got != "adaptive" {
		t.Fatalf("pickClaudeThinkingType = %q", got)
	}
}

func TestPickClaudeThinkingTypeRejectsDefaultPlaceholder(t *testing.T) {
	opt := &llmv1.CallOptions{
		ClaudeCode: &llmv1.ClaudeCodeCallOptions{ThinkingType: proto.String("default")},
	}
	got := pickClaudeThinkingType(opt, "enabled")
	if got != "enabled" {
		t.Fatalf("pickClaudeThinkingType = %q, want provider fallback enabled", got)
	}
}

func TestAppendClaudeSettingsArgsOmitsBudgetWhenDisabled(t *testing.T) {
	budget := 8192
	opt := &llmv1.CallOptions{
		ClaudeCode: &llmv1.ClaudeCodeCallOptions{
			ThinkingType:         proto.String("disabled"),
			ThinkingBudgetTokens: proto.Int32(int32(budget)),
		},
	}
	args := appendClaudeSettingsArgs(nil, Config{}, opt)
	if len(args) != 2 || args[0] != "--settings" {
		t.Fatalf("args = %v", args)
	}
	if strings.Contains(args[1], "budget_tokens") {
		t.Fatalf("disabled thinking must not include budget_tokens: %s", args[1])
	}
}

func TestPickClaudeThinkingBudgetTokensFromCallOptions(t *testing.T) {
	budget := int32(8192)
	opt := &llmv1.CallOptions{
		ClaudeCode: &llmv1.ClaudeCodeCallOptions{ThinkingBudgetTokens: &budget},
	}
	fallback := 1024
	got := pickClaudeThinkingBudgetTokens(opt, &fallback)
	if got == nil || *got != 8192 {
		t.Fatalf("pickClaudeThinkingBudgetTokens = %v", got)
	}
}

func TestTokenUsageFromClaudeResultMap(t *testing.T) {
	raw := map[string]any{
		"type": "result",
		"usage": map[string]any{
			"input_tokens":            float64(100),
			"output_tokens":           float64(50),
			"cache_read_input_tokens": float64(10),
		},
		"total_cost_usd": 0.42,
	}
	u := tokenUsageFromClaudeResultMap(raw)
	if u == nil {
		t.Fatal("expected usage")
	}
	if u.GetInputTokens() != 100 || u.GetOutputTokens() != 50 {
		t.Fatalf("tokens: in=%d out=%d", u.GetInputTokens(), u.GetOutputTokens())
	}
	if u.GetCacheReadTokens() != 10 {
		t.Fatalf("cache read = %d", u.GetCacheReadTokens())
	}
	if u.GetTotalCostUsd() != 0.42 {
		t.Fatalf("cost = %v", u.GetTotalCostUsd())
	}
}
