package claudecodeclillm

import (
	"slices"
	"testing"
)

func TestClaudeCLIEnvFrom_StripsAmbientCredentials(t *testing.T) {
	ambient := []string{
		"PATH=/bin",
		"ANTHROPIC_API_KEY=sk-ambient",
		"CLAUDE_CODE_OAUTH_TOKEN=ambient-token",
	}
	got := claudeCLIEnvFrom(ambient, "")
	if !slices.Equal(got, []string{"PATH=/bin"}) {
		t.Fatalf("expected only PATH to survive, got %v", got)
	}
}

func TestClaudeCLIEnvFrom_SetsProviderToken(t *testing.T) {
	ambient := []string{"PATH=/bin", "CLAUDE_CODE_OAUTH_TOKEN=ambient-token"}
	got := claudeCLIEnvFrom(ambient, "  provider-token  ")
	want := []string{"PATH=/bin", "CLAUDE_CODE_OAUTH_TOKEN=provider-token"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
