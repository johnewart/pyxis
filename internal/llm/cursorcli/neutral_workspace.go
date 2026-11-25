package cursorclillm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnewart/pyxis/llmdlog"
)

// Why each session gets its own Cursor workspace
//
// The host's cursorws.Ensure writes .cursor/mcp.json and the session's rendered
// .mdc rules (its system prompt) into the --workspace directory on every run.
// The old default was ONE shared directory for every session, so concurrent turns
// overwrote each other's rules mid-turn: session A could run on session B's
// instructions. Now, unless the operator configures a workspace, every run gets a
// directory of its own, which is removed when the run ends.
//
// For a session the path stays the same across turns (only its contents are
// removed): Cursor may tie a --resume chat to its workspace path.

const (
	neutralWorkspaceSessionPrefix = "llmd-cursor-session-"
	neutralWorkspaceOneShotPrefix = "llmd-cursor-oneshot-"
)

type llmdSessionCtxKey struct{}

// WithLlmdSessionID attaches the llmd session id; it names the session's Cursor
// workspace (<tmp>/llmd-cursor-session-<id>).
func WithLlmdSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, llmdSessionCtxKey{}, strings.TrimSpace(sessionID))
}

func llmdSessionIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(llmdSessionCtxKey{}).(string)
	return strings.TrimSpace(v)
}

// defaultCursorWorkspace returns the --workspace for one run when cursor_cli.workspace
// is unset, plus a cleanup to call once the CLI process has exited.
//   - LLMD_CURSOR_CLI_WORKSPACE set: that directory, shared and operator-owned (never removed);
//   - with an llmd session: <tmp>/llmd-cursor-session-<id>, removed after the run;
//   - without one (one-shot): a unique <tmp>/llmd-cursor-oneshot-*, removed after the run.
func defaultCursorWorkspace(ctx context.Context) (ws string, cleanup func(), err error) {
	noop := func() {}
	if p := strings.TrimSpace(os.Getenv("LLMD_CURSOR_CLI_WORKSPACE")); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", noop, err
		}
		return abs, noop, os.MkdirAll(abs, 0o755)
	}
	if id := sanitizeWorkspaceID(llmdSessionIDFromContext(ctx)); id != "" {
		ws = filepath.Join(os.TempDir(), neutralWorkspaceSessionPrefix+id)
		if err := os.MkdirAll(ws, 0o700); err != nil {
			return "", noop, fmt.Errorf("cursor-cli: create session workspace %s: %w", ws, err)
		}
	} else {
		ws, err = os.MkdirTemp("", neutralWorkspaceOneShotPrefix)
		if err != nil {
			return "", noop, fmt.Errorf("cursor-cli: create one-shot workspace: %w", err)
		}
	}
	return ws, func() { removeCursorWorkspace(ws) }, nil
}

// removeCursorWorkspace deletes a workspace created by defaultCursorWorkspace.
// It refuses anything else, so an operator-configured directory is never removed.
func removeCursorWorkspace(ws string) {
	base := filepath.Base(ws)
	if filepath.Dir(ws) != filepath.Clean(os.TempDir()) ||
		!(strings.HasPrefix(base, neutralWorkspaceSessionPrefix) || strings.HasPrefix(base, neutralWorkspaceOneShotPrefix)) {
		return
	}
	if err := os.RemoveAll(ws); err != nil {
		llmdlog.Stdout("cursor-cli", llmdlog.LevelWarn, "cursor-cli: remove workspace %s: %v", ws, err)
	}
}

// sanitizeWorkspaceID keeps [A-Za-z0-9_-] so a session id cannot escape the temp dir.
func sanitizeWorkspaceID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
