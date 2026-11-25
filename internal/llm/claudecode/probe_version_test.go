package claudecodeclillm

import (
	"strings"
	"testing"
)

// The floor exists because a CLI below it fails to bring up llmd's MCP tools. These
// cases pin the comparison and, importantly, the fail-open behaviour: an unreadable
// version must never block a spawn.
func TestClaudeCLIVersionTooOld(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    bool
	}{
		{"below floor", "2.1.197", true},
		{"well below floor", "1.9.0", true},
		{"at floor", MinClaudeCLIVersion, false},
		{"above floor", "2.1.246", false},
		{"v prefix below floor", "v2.1.197", true},
		{"empty is unknown, not old", "", false},
		{"unparseable is unknown, not old", "Claude Code (dev build)", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeCLIVersionTooOld(tc.version); got != tc.want {
				t.Fatalf("claudeCLIVersionTooOld(%q) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}

// The message has to name both versions: the whole point is that the operator can
// act on it without reading llmd's source.
func TestClaudeVersionTooOldErrorNamesVersions(t *testing.T) {
	msg := claudeVersionTooOldError("2.1.197")
	if !strings.Contains(msg, "2.1.197") {
		t.Errorf("message must name the installed version: %q", msg)
	}
	if !strings.Contains(msg, MinClaudeCLIVersion) {
		t.Errorf("message must name the minimum version: %q", msg)
	}
}

// A too-old or version-pinned CLI must be named in the inventory-gate error, not just
// in the probe: the gate is where the failure actually surfaces during a turn.
func TestDescribeBinaryReportsBinAndVersion(t *testing.T) {
	l := cliLog{module: "test", spawn: "abcd1234", bin: "/opt/claude", version: "2.1.197"}
	got := l.describeBinary()
	if !strings.Contains(got, "/opt/claude") || !strings.Contains(got, "2.1.197") {
		t.Fatalf("describeBinary() = %q, want bin and version", got)
	}

	unknown := cliLog{module: "test", spawn: "abcd1234"}.describeBinary()
	if !strings.Contains(unknown, "unknown") {
		t.Fatalf("describeBinary() with no binary = %q, want %q marker", unknown, "unknown")
	}
}
