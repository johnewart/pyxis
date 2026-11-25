package claudecodeclillm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/johnewart/pyxis/llmdlog"
)

// The CLI stores each conversation under <config dir>/projects/<project key>/,
// where the project key is the cwd with every non-alphanumeric character
// replaced by '-' (e.g. /tmp/llmd-session-abc -> -tmp-llmd-session-abc). Because
// llmd runs the CLI from a per-session neutral cwd (neutral_cwd.go), one llmd
// session maps to exactly one project directory, which we remove when the llmd
// session is deleted.

// usedConfigDirs records every explicit Config.ConfigDir a CLI was spawned with in
// this process ("" = the CLI default, ~/.claude). Directories used only before a
// restart are not known; their transcripts are left in place.
var usedConfigDirs sync.Map // string -> struct{}

func recordClaudeConfigDir(dir string) {
	usedConfigDirs.Store(strings.TrimSpace(dir), struct{}{})
}

// claudeProjectKey mirrors the CLI's cwd -> project directory name mapping.
func claudeProjectKey(cwd string) string {
	var b strings.Builder
	for _, r := range cwd {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// sessionTranscriptDirs lists the project directories that may hold the CLI
// transcripts for an llmd session, across the default and every recorded config dir.
func sessionTranscriptDirs(sessionID string) ([]string, error) {
	id := sanitizeNeutralCwdID(sessionID)
	if id == "" {
		return nil, nil
	}
	key := claudeProjectKey(filepath.Join(os.TempDir(), neutralCwdSessionPrefix+id))
	roots := map[string]struct{}{}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("claude-code-cli: resolve home for transcript purge: %w", err)
	}
	roots[filepath.Join(home, ".claude")] = struct{}{}
	usedConfigDirs.Range(func(k, _ any) bool {
		if d, _ := k.(string); d != "" {
			roots[d] = struct{}{}
		}
		return true
	})
	out := make([]string, 0, len(roots))
	for r := range roots {
		out = append(out, filepath.Join(r, "projects", key))
	}
	return out, nil
}

// PurgeSessionTranscripts deletes the Claude Code CLI transcripts stored for an
// llmd session. Call it when the llmd session is deleted: those transcripts only
// exist to back --resume for that session. Sessions whose provider sets an
// explicit Cwd are not under a neutral cwd and are never touched.
func PurgeSessionTranscripts(sessionID string) error {
	dirs, err := sessionTranscriptDirs(sessionID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, d := range dirs {
		if !strings.Contains(filepath.Base(d), "llmd-session-") {
			continue // defensive: never remove anything but our own keys
		}
		if err := os.RemoveAll(d); err != nil {
			llmdlog.Stdout("claude-code-cli", llmdlog.LevelWarn, "purge session transcripts %s: %v", d, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
