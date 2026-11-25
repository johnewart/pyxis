package claudecodeclillm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/johnewart/pyxis/llmdlog"
)

// Why the CLI runs from a neutral directory
//
// The Claude Code CLI always tells the model its working directory (an
// environment block no flag removes, even with --system-prompt). Without an
// explicit Config.Cwd the CLI would inherit llmd's own cwd, often the user's
// repo. Agent runs never need it: the CLI's built-in file tools are off and
// llmd's MCP tools resolve the workspace server-side. So when Config.Cwd is
// unset, the CLI runs from an empty temp directory that names nothing.
//
// The directory is created right before each spawn and removed right after it.
// For a session the PATH must stay the same across turns: the CLI keys stored
// sessions by cwd, and --resume only finds a session from the same path. Only
// the path string matters, so deleting and recreating the directory is fine.

const (
	neutralCwdSessionPrefix = "llmd-session-"
	neutralCwdOneShotPrefix = "llmd-oneshot-"
)

type llmdSessionCtxKey struct{}

// WithLlmdSessionID attaches the llmd session id; it names the session's
// neutral working directory (<tmp>/llmd-session-<id>).
func WithLlmdSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, llmdSessionCtxKey{}, strings.TrimSpace(sessionID))
}

func llmdSessionIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(llmdSessionCtxKey{}).(string)
	return strings.TrimSpace(v)
}

// createNeutralCwd creates the working directory for one spawn:
//   - with an llmd session: <tmp>/llmd-session-<id>, a fixed path so --resume works;
//   - without one (one-shot completions, which never resume): a unique
//     <tmp>/llmd-oneshot-*, so concurrent one-shots never remove each other's cwd.
func createNeutralCwd(ctx context.Context) (string, error) {
	if id := sanitizeNeutralCwdID(llmdSessionIDFromContext(ctx)); id != "" {
		dir := filepath.Join(os.TempDir(), neutralCwdSessionPrefix+id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("claude-code-cli: create neutral cwd %s: %w", dir, err)
		}
		return dir, nil
	}
	dir, err := os.MkdirTemp("", neutralCwdOneShotPrefix)
	if err != nil {
		return "", fmt.Errorf("claude-code-cli: create one-shot cwd: %w", err)
	}
	return dir, nil
}

// isNeutralCwd reports whether dir is one createNeutralCwd made. Checked before
// removal so an operator-configured Cwd can never be deleted.
func isNeutralCwd(dir string) bool {
	if dir == "" || filepath.Dir(dir) != filepath.Clean(os.TempDir()) {
		return false
	}
	base := filepath.Base(dir)
	return strings.HasPrefix(base, neutralCwdSessionPrefix) || strings.HasPrefix(base, neutralCwdOneShotPrefix)
}

// releaseNeutralCwd removes the spawn's neutral working directory once the CLI
// process has exited. A no-op for operator-configured directories.
func releaseNeutralCwd(cmd *exec.Cmd) {
	if cmd == nil || !isNeutralCwd(cmd.Dir) {
		return
	}
	if err := os.RemoveAll(cmd.Dir); err != nil {
		llmdlog.Stdout("claude-code-cli", llmdlog.LevelWarn, "remove neutral cwd %s: %v", cmd.Dir, err)
	}
}

// sanitizeNeutralCwdID keeps [A-Za-z0-9_-] so a session id can never escape the
// temp dir or smuggle path separators.
func sanitizeNeutralCwdID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}
