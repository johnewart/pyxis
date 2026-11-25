package store

import (
	"reflect"
	"testing"
)

func TestDisabledToolsJSON_RoundTrip(t *testing.T) {
	t.Parallel()
	in := []string{"search", "read_code", "run_command"}
	s, err := MarshalDisabledToolsJSON(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if s == "" {
		t.Fatalf("Marshal returned empty string for non-empty list")
	}
	out, err := UnmarshalDisabledToolsJSON(s)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := []string{"read_code", "run_command", "search"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("round-trip mismatch: got %v, want %v", out, want)
	}
}

func TestDisabledToolsJSON_EmptyAndBlankYieldEmpty(t *testing.T) {
	t.Parallel()
	for _, in := range [][]string{nil, {}, {"", "   ", "\t"}} {
		s, err := MarshalDisabledToolsJSON(in)
		if err != nil {
			t.Fatalf("Marshal(%v): %v", in, err)
		}
		if s != "" {
			t.Fatalf("Marshal(%v) = %q, want empty string", in, s)
		}
	}
	for _, in := range []string{"", "   ", "\n\t"} {
		out, err := UnmarshalDisabledToolsJSON(in)
		if err != nil {
			t.Fatalf("Unmarshal(%q): %v", in, err)
		}
		if out != nil {
			t.Fatalf("Unmarshal(%q) = %v, want nil", in, out)
		}
	}
	// An explicit empty array also normalises to nil.
	out, err := UnmarshalDisabledToolsJSON("[]")
	if err != nil {
		t.Fatalf("Unmarshal([]): %v", err)
	}
	if out != nil {
		t.Fatalf("Unmarshal([]) = %v, want nil", out)
	}
}

func TestDisabledToolsJSON_NormalisesDedupSort(t *testing.T) {
	t.Parallel()
	s, err := MarshalDisabledToolsJSON([]string{" zeta ", "alpha", "", "alpha", "beta ", "  "})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if s != `["alpha","beta","zeta"]` {
		t.Fatalf("Marshal produced %q, want canonical sorted/deduped array", s)
	}
	out, err := UnmarshalDisabledToolsJSON(`["b", " a ", "b", "", "c"]`)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("Unmarshal normalisation: got %v, want %v", out, want)
	}
}

func TestDisabledToolsJSON_RejectsMalformed(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"{", `{"a":1}`, `["unterminated`, `[1,2]`, "not json"} {
		if _, err := UnmarshalDisabledToolsJSON(in); err == nil {
			t.Fatalf("Unmarshal(%q) returned nil error, want parse error", in)
		}
	}
}
