package cursorclillm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultCursorWorkspace_perSessionStableAndRemoved(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("LLMD_CURSOR_CLI_WORKSPACE", "")
	ctxA := WithLlmdSessionID(context.Background(), "sess-a")
	ctxB := WithLlmdSessionID(context.Background(), "sess-b")

	wsA, cleanA, err := defaultCursorWorkspace(ctxA)
	if err != nil {
		t.Fatal(err)
	}
	wsB, cleanB, err := defaultCursorWorkspace(ctxB)
	if err != nil {
		t.Fatal(err)
	}
	// The bug: one shared workspace let concurrent sessions overwrite each other's rules.
	if wsA == wsB {
		t.Fatalf("sessions share a workspace: %q", wsA)
	}
	if filepath.Base(wsA) != "llmd-cursor-session-sess-a" {
		t.Fatalf("unexpected session workspace %q", wsA)
	}
	if err := os.WriteFile(filepath.Join(wsA, "rules.mdc"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanA()
	cleanB()
	for _, ws := range []string{wsA, wsB} {
		if _, err := os.Stat(ws); !os.IsNotExist(err) {
			t.Fatalf("workspace %s not removed after run: %v", ws, err)
		}
	}
	// Next turn: same path for the same session (Cursor may tie --resume to it).
	wsA2, cleanA2, err := defaultCursorWorkspace(ctxA)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanA2()
	if wsA2 != wsA {
		t.Fatalf("session workspace path changed across turns: %q vs %q", wsA, wsA2)
	}
}

func TestDefaultCursorWorkspace_oneShotUnique(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("LLMD_CURSOR_CLI_WORKSPACE", "")
	a, cleanA, err := defaultCursorWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, cleanB, err := defaultCursorWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !strings.HasPrefix(filepath.Base(a), "llmd-cursor-oneshot-") {
		t.Fatalf("one-shot workspaces must be unique: %q %q", a, b)
	}
	cleanA()
	cleanB()
	if entries, _ := os.ReadDir(os.TempDir()); len(entries) != 0 {
		t.Fatalf("temp dir not cleaned: %v", entries)
	}
}

func TestDefaultCursorWorkspace_operatorOverrideNeverRemoved(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	op := filepath.Join(t.TempDir(), "operator-ws")
	t.Setenv("LLMD_CURSOR_CLI_WORKSPACE", op)
	ws, cleanup, err := defaultCursorWorkspace(WithLlmdSessionID(context.Background(), "s"))
	if err != nil {
		t.Fatal(err)
	}
	if ws != op {
		t.Fatalf("override ignored: got %q want %q", ws, op)
	}
	cleanup()
	removeCursorWorkspace(op) // also refuses directly
	if _, err := os.Stat(op); err != nil {
		t.Fatalf("operator workspace removed: %v", err)
	}
}

func TestDefaultCursorWorkspace_hostileSessionID(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("LLMD_CURSOR_CLI_WORKSPACE", "")
	ws, cleanup, err := defaultCursorWorkspace(WithLlmdSessionID(context.Background(), "../../etc/x"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if filepath.Dir(ws) != filepath.Clean(os.TempDir()) {
		t.Fatalf("session id escaped temp dir: %q", ws)
	}
}
