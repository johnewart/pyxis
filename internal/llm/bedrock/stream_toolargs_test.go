package bedrockllm

import "testing"

func TestNormalizeStreamedToolArgumentsJSON(t *testing.T) {
	if g := normalizeStreamedToolArgumentsJSON("grep", ""); g != "{}" {
		t.Fatalf("empty: %q", g)
	}
	if g := normalizeStreamedToolArgumentsJSON("grep", `  `); g != "{}" {
		t.Fatalf("whitespace: %q", g)
	}
	if g := normalizeStreamedToolArgumentsJSON("grep", `{"pattern":"x"}`); g != `{"pattern":"x"}` {
		t.Fatalf("object: %q", g)
	}
	if g := normalizeStreamedToolArgumentsJSON("grep", `{"pattern":`); g != "{}" {
		t.Fatalf("truncated should fallback: %q", g)
	}
	if g := normalizeStreamedToolArgumentsJSON("grep", `[]`); g != "{}" {
		t.Fatalf("array should fallback: %q", g)
	}
}
