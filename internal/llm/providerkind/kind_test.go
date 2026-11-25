package providerkind

import "testing"

// TestParseAcceptsOnlyCanonicalIDs pins the one-id rule: Parse accepts exactly
// the ids String() produces, and nothing else.
//
// This replaces a test that pinned the OPPOSITE contract -- it asserted Parse
// accepted "claude_v2", "cursor", "claude_code", "claude-code-v2",
// "claude_cli_v2" and "cursor-cli" as aliases, because @pyxis-llm/core's ProviderKind
// had invented its own spellings and llmd had been taught to tolerate them. The
// aliases are gone: the SDK emits the canonical id, a kind is carried as this
// enum rather than as a string, and anything else is garbage.
func TestParseAcceptsOnlyCanonicalIDs(t *testing.T) {
	for _, k := range All() {
		id := k.String()
		if id == "" {
			t.Fatalf("kind %d from All() has no id", int(k))
		}
		got, ok := Parse(id)
		if !ok {
			t.Errorf("Parse(%q) rejected its own String() output", id)
			continue
		}
		if got != k {
			t.Errorf("Parse(%q) = %v, want %v", id, got, k)
		}
	}
}

func TestOpenRouterKind(t *testing.T) {
	if OpenRouter.String() != "openrouter" {
		t.Errorf("String() = %q", OpenRouter.String())
	}
	if k, ok := Parse("openrouter"); !ok || k != OpenRouter {
		t.Errorf("Parse(openrouter) = %v, %v", k, ok)
	}
	found := false
	for _, k := range All() {
		if k == OpenRouter {
			found = true
		}
	}
	if !found {
		t.Error("All() does not contain OpenRouter")
	}
	if !OpenRouter.Valid() {
		t.Error("OpenRouter.Valid() = false")
	}
	if OpenRouter.DisplayName() != "OpenRouter" {
		t.Errorf("DisplayName() = %q", OpenRouter.DisplayName())
	}
	if !OpenRouter.RequiresAPIKey() {
		t.Error("RequiresAPIKey() = false, want true")
	}
}

func TestParseRejectsEverythingElse(t *testing.T) {
	// The first six are the spellings llmd used to accept. They must now fail:
	// keeping them working is what let two vocabularies coexist.
	for _, in := range []string{
		"claude_v2",
		"claude-v2",
		"claude_code",
		"claude-code",
		"claude-code-v2",
		"claude_cli_v2",
		"cursor",
		"cursor-cli",
		// Never-valid input, including the casing/whitespace Parse used to absorb.
		"",
		" ",
		"claude",
		"claude_v3",
		"cursor_agent",
		"openai_v2",
		"OpenAI",
		"CLAUDE_CODE_V2",
		" cursor_cli",
		"cursor_cli ",
	} {
		if k, ok := Parse(in); ok {
			t.Errorf("Parse(%q) = %v, want rejection", in, k)
		}
	}
}

// TestUnspecifiedIsNotAKind guards the failure mode that made a bad provider
// name look like a missing field: the zero value must never pass as real.
func TestUnspecifiedIsNotAKind(t *testing.T) {
	if Unspecified.Valid() {
		t.Error("Unspecified.Valid() = true, want false")
	}
	if id := Unspecified.String(); id != "" {
		t.Errorf("Unspecified.String() = %q, want \"\"", id)
	}
	if _, ok := Parse(Unspecified.String()); ok {
		t.Error("Parse(Unspecified.String()) accepted the zero value")
	}
	for _, k := range All() {
		if k == Unspecified {
			t.Error("All() contains Unspecified")
		}
		if !k.Valid() {
			t.Errorf("All() contains invalid kind %d", int(k))
		}
	}
}
