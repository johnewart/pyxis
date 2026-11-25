package toolargs

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestForDisplay_PassesThroughSmallArguments(t *testing.T) {
	const args = `{"path":"foo.go","start_line":15,"end_line":200}`

	got, shortened := ForDisplay(args)

	if got != args {
		t.Errorf("got %q, want the input unchanged", got)
	}
	if shortened {
		t.Error("shortened = true for a small payload")
	}
}

func TestForDisplay_EmptyAndBlankYieldNothing(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t"} {
		got, shortened := ForDisplay(in)
		if got != "" || shortened {
			t.Errorf("ForDisplay(%q) = (%q, %v), want (\"\", false)", in, got, shortened)
		}
	}
}

// The regression that motivated per-value elision: Go marshals object keys in
// sorted order, so "content" precedes "path". A prefix clip would keep 2KiB of
// file body and drop the filename — the one field a label is built from.
func TestForDisplay_KeepsIdentifyingFieldsWhenBulkValueIsHuge(t *testing.T) {
	args, err := json.Marshal(map[string]any{
		"content":    strings.Repeat("x", 64*1024),
		"path":       "big.go",
		"start_line": 15,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, shortened := ForDisplay(string(args))

	if !shortened {
		t.Fatal("shortened = false for a 64KiB payload")
	}
	if len(got) > MaxDisplayBytes {
		t.Errorf("len = %d, want <= %d", len(got), MaxDisplayBytes)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(got), &fields); err != nil {
		t.Fatalf("elided output is not valid JSON: %v (%q)", err, got)
	}
	if fields["path"] != "big.go" {
		t.Errorf("path = %v, want big.go (it must survive elision)", fields["path"])
	}
	if fields["start_line"] != float64(15) {
		t.Errorf("start_line = %v, want 15", fields["start_line"])
	}
	// The bulk field is still present as a key, just shortened.
	content, ok := fields["content"].(string)
	if !ok {
		t.Fatalf("content = %T, want a shortened string", fields["content"])
	}
	if len(content) > MaxValueBytes+8 {
		t.Errorf("content is %d bytes, want elided to ~%d", len(content), MaxValueBytes)
	}
	if !strings.HasSuffix(content, "…") {
		t.Error("elided string is not marked with an ellipsis")
	}
}

// Elision must survive a round trip as JSON: the whole point is that a
// consumer can still parse it and read the surviving fields.
func TestForDisplay_ElidedOutputRemainsValidJSON(t *testing.T) {
	args, err := json.Marshal(map[string]any{
		"path":   "x.go",
		"blob":   strings.Repeat("y", 8*1024),
		"nested": map[string]any{"deep": strings.Repeat("z", 8*1024)},
		"list":   []string{strings.Repeat("w", 4*1024)},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, shortened := ForDisplay(string(args))

	if !shortened {
		t.Fatal("shortened = false")
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(got), &fields); err != nil {
		t.Fatalf("not valid JSON: %v (%q)", err, got)
	}
	if fields["path"] != "x.go" {
		t.Errorf("path = %v, want x.go", fields["path"])
	}
	// Non-string bulk values become a byte-count stand-in rather than a
	// meaningless fragment.
	for _, key := range []string{"nested", "list"} {
		s, ok := fields[key].(string)
		if !ok {
			t.Errorf("%s = %T, want an elision marker string", key, fields[key])
			continue
		}
		if !strings.Contains(s, "elided") {
			t.Errorf("%s = %q, want an elision marker", key, s)
		}
	}
}

func TestForDisplay_ExactlyAtLimitIsNotShortened(t *testing.T) {
	args := `{"k":"` + strings.Repeat("a", MaxDisplayBytes-10) + `"}`
	if len(args) > MaxDisplayBytes {
		t.Fatalf("test fixture is %d bytes, over the %d limit", len(args), MaxDisplayBytes)
	}

	got, shortened := ForDisplay(args)

	if shortened {
		t.Error("shortened = true at/below the limit")
	}
	if got != args {
		t.Error("payload at/below the limit was modified")
	}
}

// A very wide object can bust the budget on keys alone, after every value is
// already elided.
func TestForDisplay_DropsFieldsWhenEvenElidedObjectIsTooBig(t *testing.T) {
	wide := map[string]any{}
	for i := 0; i < 500; i++ {
		wide[fmt.Sprintf("field_%03d", i)] = strings.Repeat("v", 64)
	}
	args, err := json.Marshal(wide)
	if err != nil {
		t.Fatal(err)
	}

	got, shortened := ForDisplay(string(args))

	if !shortened {
		t.Fatal("shortened = false for a very wide object")
	}
	if len(got) > MaxDisplayBytes {
		t.Errorf("len = %d, want <= %d", len(got), MaxDisplayBytes)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(got), &fields); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
}

// Non-object payloads have no structure worth preserving and fall back to a
// prefix clip — which must still never end mid-rune, or the proto string field
// fails to marshal and the whole frame is lost.
func TestForDisplay_NonObjectFallsBackToClip(t *testing.T) {
	for name, args := range map[string]string{
		"array":     `["` + strings.Repeat("é", MaxDisplayBytes) + `"]`,
		"bare":      strings.Repeat("é", MaxDisplayBytes),
		"malformed": `{"path": "` + strings.Repeat("é", MaxDisplayBytes),
	} {
		t.Run(name, func(t *testing.T) {
			got, shortened := ForDisplay(args)

			if !shortened {
				t.Fatal("shortened = false for an oversized payload")
			}
			if len(got) > MaxDisplayBytes {
				t.Errorf("len = %d, want <= %d", len(got), MaxDisplayBytes)
			}
			if !utf8.ValidString(got) {
				t.Error("clipped output is not valid UTF-8")
			}
		})
	}
}

func TestForDisplay_ElidedStringValueStaysValidUTF8(t *testing.T) {
	args, err := json.Marshal(map[string]any{
		"path": "ok.go",
		"text": strings.Repeat("🙂", 4*1024),
	})
	if err != nil {
		t.Fatal(err)
	}

	got, shortened := ForDisplay(string(args))

	if !shortened {
		t.Fatal("shortened = false")
	}
	if !utf8.ValidString(got) {
		t.Error("output is not valid UTF-8")
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(got), &fields); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if fields["path"] != "ok.go" {
		t.Errorf("path = %v, want ok.go", fields["path"])
	}
}
