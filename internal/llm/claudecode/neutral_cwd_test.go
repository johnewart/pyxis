package claudecodeclillm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNeutralCwd_sessionPathStableAndReleased(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	ctx := WithLlmdSessionID(context.Background(), "6f1c2a9e-0b7d-4c1e-9a55-2f3e4d5c6b7a")

	a, err := createNeutralCwd(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(os.TempDir(), "llmd-session-6f1c2a9e-0b7d-4c1e-9a55-2f3e4d5c6b7a")
	if a != want {
		t.Fatalf("got %q want %q", a, want)
	}
	if fi, err := os.Stat(a); err != nil || !fi.IsDir() {
		t.Fatalf("neutral cwd not created: %v", err)
	}

	releaseNeutralCwd(&exec.Cmd{Dir: a})
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Fatalf("neutral cwd not removed after release: %v", err)
	}

	// Next turn: same path again, so --resume (keyed by cwd) still matches.
	b, err := createNeutralCwd(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("path changed across turns: %q vs %q", a, b)
	}
}

func TestNeutralCwd_oneShotUniqueAndReleased(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	a, err := createNeutralCwd(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := createNeutralCwd(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !strings.HasPrefix(filepath.Base(a), "llmd-oneshot-") {
		t.Fatalf("one-shot dirs must be unique llmd-oneshot-*: %q %q", a, b)
	}
	releaseNeutralCwd(&exec.Cmd{Dir: a})
	releaseNeutralCwd(&exec.Cmd{Dir: b})
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp dir not cleaned up: %v", entries)
	}
}

func TestNeutralCwd_hostileSessionIDStaysInTemp(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	got, err := createNeutralCwd(WithLlmdSessionID(context.Background(), "../../etc/x"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got) != filepath.Clean(os.TempDir()) || filepath.Base(got) != "llmd-session-etcx" {
		t.Fatalf("session id escaped temp dir: %q", got)
	}
}

func TestReleaseNeutralCwd_neverRemovesConfiguredCwd(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	for _, dir := range []string{
		t.TempDir(), // operator-configured directory elsewhere
		filepath.Join(os.TempDir(), "my-project"), // in tmp, but not ours
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		releaseNeutralCwd(&exec.Cmd{Dir: dir})
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("release removed a directory it does not own: %s (%v)", dir, err)
		}
	}
}
