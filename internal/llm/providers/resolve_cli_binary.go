package providers

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnewart/pyxis/internal/llm/clidiscovery"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/internal/store"
)

// resolveLogModule tags the binary-resolution step of provider registration.
const resolveLogModule = "cli-binary"

// ResolveCLIBinaryForConfig fills in the binary path of a CLI-backed provider
// config (Claude Code, Cursor CLI) before it is validated and stored.
//
// Two rules, in this order:
//
//  1. An explicitly configured binary is authoritative. It is checked (so a typo
//     fails here, with a clear message, rather than on the user's first chat) but
//     never replaced -- naming a path is a deliberate act.
//  2. An empty binary triggers a scan of the machine llmd is running on, and the
//     single absolute path that scan picks is written back into cfg.
//
// Writing the resolved path back into cfg is the important part. The config is
// about to be persisted, and BuildEntryFromProviderConfig re-runs on every
// startup; if the binary field stayed empty, each boot would re-scan and could
// silently resolve to a different install as the filesystem changes. Resolving
// once, at create/update time, makes a provider mean one specific binary.
//
// For non-CLI provider kinds this is a no-op.
func ResolveCLIBinaryForConfig(ctx context.Context, cfg *store.LlmProviderConfig) error {
	if cfg == nil {
		return fmt.Errorf("nil provider config")
	}
	switch {
	case cfg.ClaudeCode != nil:
		path, err := resolveCLIBinary(ctx, clidiscovery.KindClaude, cfg.ClaudeCode.Binary, "Claude Code")
		if err != nil {
			return err
		}
		cfg.ClaudeCode.Binary = path
	case cfg.CursorCLI != nil:
		path, err := resolveCLIBinary(ctx, clidiscovery.KindCursor, cfg.CursorCLI.Binary, "Cursor Agent")
		if err != nil {
			return err
		}
		cfg.CursorCLI.Binary = path
	}
	return nil
}

// resolveCLIBinary returns the absolute binary path to store for one CLI kind.
//
// The chosen path is logged at INFO because it is about to be persisted and will
// be THE binary this provider uses from now on. When a provider later misbehaves,
// "which claude is it actually running?" is the first question, and this line is
// the answer.
func resolveCLIBinary(ctx context.Context, kind clidiscovery.Kind, configured, label string) (string, error) {
	cfgd := strings.TrimSpace(configured)
	if cfgd == "" {
		llmdlog.Stdout(resolveLogModule, llmdlog.LevelInfo,
			"%s: no binary configured, scanning this machine for one", label)
	}
	c, err := clidiscovery.Resolve(ctx, kind, cfgd)
	if err != nil {
		llmdlog.Stdout(resolveLogModule, llmdlog.LevelError, "%s: %v", label, err)
		return "", fmt.Errorf("%s CLI: %w", label, err)
	}
	llmdlog.Stdout(resolveLogModule, llmdlog.LevelInfo,
		"%s: using binary %s (version=%s, chosen by %s); storing this path on the provider",
		label, c.Path, c.Version, c.Source)
	return c.Path, nil
}
