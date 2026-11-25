package policyhook

import (
	"strings"
	"testing"
)

func envWith(v string) func(string) string {
	return func(k string) string {
		if k == "LLMD_DEFAULT_POLICY_MODE" {
			return v
		}
		return ""
	}
}

func TestParseFailMode(t *testing.T) {
	tests := []struct {
		in   string
		want FailMode
	}{
		{"", FailClosed},
		{"closed", FailClosed},
		{"open", FailOpen},
		{"OPEN", FailOpen},
		{" Closed ", FailClosed},
	}
	for _, tc := range tests {
		got, err := ParseFailMode(tc.in)
		if err != nil {
			t.Fatalf("ParseFailMode(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseFailMode(%q) = %v want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseFailModeInvalid(t *testing.T) {
	_, err := ParseFailMode("maybe")
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "LLMD_DEFAULT_POLICY_MODE") {
		t.Fatalf("error %q does not name LLMD_DEFAULT_POLICY_MODE", err)
	}
	if _, err := FailModeFromEnv(envWith("bogus")); err == nil || !strings.Contains(err.Error(), "LLMD_DEFAULT_POLICY_MODE") {
		t.Fatalf("FailModeFromEnv invalid: err = %v", err)
	}
}

func TestFailModeFromEnv(t *testing.T) {
	for in, want := range map[string]FailMode{"": FailClosed, "closed": FailClosed, "open": FailOpen} {
		got, err := FailModeFromEnv(envWith(in))
		if err != nil || got != want {
			t.Fatalf("FailModeFromEnv(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}
