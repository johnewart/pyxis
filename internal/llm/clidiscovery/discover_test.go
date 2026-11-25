package clidiscovery

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// isolate removes the machine-wide bin directories from the search so a real
// CLI installed on the developer's or CI machine cannot leak into results and
// make the test's outcome depend on the host.
func isolate(t *testing.T) {
	t.Helper()
	prev := systemRoots
	systemRoots = nil
	t.Cleanup(func() { systemRoots = prev })
}

// writeFakeCLI creates an executable script at dir/name that prints version on
// its version flag, standing in for a real CLI install.
func writeFakeCLI(t *testing.T, dir, name, version string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI fixtures are shell scripts; not meaningful on windows")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	script := "#!/bin/sh\necho " + version + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolve_SymlinkSurvivesVersionFlip(t *testing.T) {
	isolate(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CLAUDE_CLI_PATH", "")
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	v123 := writeFakeCLI(t, filepath.Join(versions, "1.2.3"), "claude", "1.2.3")
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "claude")
	if err := os.Symlink(v123, link); err != nil {
		t.Fatal(err)
	}

	got, err := Resolve(context.Background(), KindClaude, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != link {
		t.Fatalf("got %s want %s", got.Path, link)
	}
	if got.Version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", got.Version)
	}

	// Self-update: new version written, symlink repointed, old version deleted.
	v124 := writeFakeCLI(t, filepath.Join(versions, "1.2.4"), "claude", "1.2.4")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(v124, link); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(v123)); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(got.Path).Output()
	if err != nil {
		t.Fatalf("run %s: %v", got.Path, err)
	}
	if strings.TrimSpace(string(out)) != "1.2.4" {
		t.Fatalf("output = %q, want 1.2.4", out)
	}
}

func TestResolve_SymlinkedOverrideReturnedUnchanged(t *testing.T) {
	isolate(t)
	target := writeFakeCLI(t, t.TempDir(), "claude", "3.0.0")
	link := filepath.Join(t.TempDir(), "claude-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(context.Background(), KindClaude, link)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != link {
		t.Fatalf("got %s want %s", got.Path, link)
	}
	if got.Source != SourceOverride {
		t.Fatalf("source = %s, want %s", got.Source, SourceOverride)
	}
}

func TestResolve_OverrideWins(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	want := writeFakeCLI(t, dir, "claude", "9.9.9")
	// A different install on PATH that must NOT be chosen over the override.
	other := t.TempDir()
	writeFakeCLI(t, other, "claude", "9.9.9")
	t.Setenv("PATH", other)

	got, err := Resolve(context.Background(), KindClaude, want)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != want {
		t.Fatalf("override ignored: got %s want %s", got.Path, want)
	}
	if got.Source != SourceOverride {
		t.Fatalf("source = %s, want %s", got.Source, SourceOverride)
	}
	if got.Version != "9.9.9" {
		t.Fatalf("version = %q, want 9.9.9", got.Version)
	}
}

func TestResolve_BadOverrideDoesNotSilentlyFallBack(t *testing.T) {
	isolate(t)
	// An install on PATH that Resolve could fall back to but must not: naming a
	// path is deliberate, so a bad one is an error, not a reason to substitute.
	other := t.TempDir()
	writeFakeCLI(t, other, "claude", "9.9.9")
	t.Setenv("PATH", other)

	_, err := Resolve(context.Background(), KindClaude, filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("expected error for unresolvable override, got nil")
	}
}

func TestResolve_FindsWellKnownInstallOffPath(t *testing.T) {
	// The whole point of the package: nothing on PATH, but an install in a
	// directory the CLI is known to use.
	isolate(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CLAUDE_CLI_PATH", "")
	want := writeFakeCLI(t, filepath.Join(home, ".claude", "local"), "claude", "2.1.224")

	got, err := Resolve(context.Background(), KindClaude, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != want {
		t.Fatalf("got %s want %s", got.Path, want)
	}
	if got.Source != SourceWellKnown {
		t.Fatalf("source = %s, want %s", got.Source, SourceWellKnown)
	}
}

func TestResolve_NotFoundErrorNamesWhereItLooked(t *testing.T) {
	isolate(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CLAUDE_CLI_PATH", "")

	_, err := Resolve(context.Background(), KindClaude, "")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	// A bare "not found" leaves the user nowhere; the message must be actionable.
	for _, want := range []string{"$PATH", "CLAUDE_CLI_PATH", ".claude"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestDiscover_DeduplicatesSymlinkedInstalls(t *testing.T) {
	isolate(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CLI_PATH", "")
	real := writeFakeCLI(t, filepath.Join(home, "real"), "claude", "2.1.224")

	// Same binary reachable via PATH and via a well-known root.
	linkDir := filepath.Join(home, ".claude", "local")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(linkDir, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(home, "real"))

	got, err := Discover(context.Background(), KindClaude, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 deduplicated candidate, got %d: %+v", len(got), got)
	}
	// Attributed to the highest-priority route that reached it.
	if got[0].Source != SourcePath {
		t.Fatalf("source = %s, want %s", got[0].Source, SourcePath)
	}
}

func TestDiscover_ReportsUnusableRatherThanHiding(t *testing.T) {
	isolate(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CLAUDE_CLI_PATH", "")
	writeFakeCLI(t, filepath.Join(home, ".claude", "local"), "claude", "1.0.0")

	// Enforce a floor that 1.0.0 fails.
	SetClaudeVersionFloor(func(v string) string {
		if v == "1.0.0" {
			return "too old"
		}
		return ""
	})
	t.Cleanup(func() { SetClaudeVersionFloor(nil) })

	got, err := Discover(context.Background(), KindClaude, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the too-old install to still be reported, got %+v", got)
	}
	if got[0].Usable {
		t.Fatal("below-floor binary reported usable")
	}
	if got[0].Error != "too old" {
		t.Fatalf("error = %q, want %q", got[0].Error, "too old")
	}
}

// TestDiscover_HostSmoke reports what discovery actually finds on the machine
// running the tests. It never fails -- whether a CLI is installed here is not a
// property of the code -- but it makes a real scan visible under `go test -v`,
// which is the fastest way to confirm the well-known-root list is still right
// as these CLIs change where they install.
func TestDiscover_HostSmoke(t *testing.T) {
	for _, k := range []Kind{KindClaude, KindCursor} {
		cands, err := Discover(context.Background(), k, "")
		if err != nil {
			t.Fatalf("Discover(%s): %v", k, err)
		}
		if len(cands) == 0 {
			t.Logf("%s: not installed on this host", k)
			continue
		}
		for _, c := range cands {
			t.Logf("%s: %s version=%q source=%s origin=%s usable=%v err=%s",
				k, c.Path, c.Version, c.Source, c.Origin, c.Usable, c.Error)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
