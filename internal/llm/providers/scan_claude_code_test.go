package providers

import (
	"strings"
	"testing"
)

func TestScanClaudeCodeIncludesEffortThinkingVariants(t *testing.T) {
	models, err := scanClaudeCode("prov1", "")
	if err != nil {
		t.Fatalf("scanClaudeCode error: %v", err)
	}
	ids := make(map[string]string, len(models))
	for _, m := range models {
		if m.ProviderID != "prov1" {
			t.Fatalf("model %q has provider %q, want prov1", m.ModelID, m.ProviderID)
		}
		if _, dup := ids[m.ModelID]; dup {
			t.Fatalf("duplicate catalog model id %q", m.ModelID)
		}
		ids[m.ModelID] = m.Display
	}

	// Bare alias still present.
	if _, ok := ids["opus"]; !ok {
		t.Fatalf("expected bare \"opus\" alias in catalog: %v", ids)
	}

	// Effort/thinking variants requested by product: low (thinking), medium,
	// medium (thinking).
	if disp, ok := ids["opus-low-enabled"]; !ok || !strings.Contains(disp, "Low") || !strings.Contains(disp, "Thinking") {
		t.Fatalf("expected opus-low-enabled with Low+Thinking display, got %q ok=%v", disp, ok)
	}
	if disp, ok := ids["opus-medium-disabled"]; !ok || !strings.Contains(disp, "Medium") || strings.Contains(disp, "Thinking") {
		t.Fatalf("expected opus-medium-disabled with Medium, no Thinking, got %q ok=%v", disp, ok)
	}
	if disp, ok := ids["opus-medium-enabled"]; !ok || !strings.Contains(disp, "Medium") || !strings.Contains(disp, "Thinking") {
		t.Fatalf("expected opus-medium-enabled with Medium+Thinking display, got %q ok=%v", disp, ok)
	}
}

func TestScanClaudeCodeDefaultModelFirstAndDeduped(t *testing.T) {
	models, err := scanClaudeCode("prov1", "opus")
	if err != nil {
		t.Fatalf("scanClaudeCode error: %v", err)
	}
	if len(models) == 0 || models[0].ModelID != "opus" {
		t.Fatalf("expected default model \"opus\" first, got %+v", models[0])
	}
	count := 0
	for _, m := range models {
		if m.ModelID == "opus" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected \"opus\" deduped to a single entry, got %d", count)
	}
}
