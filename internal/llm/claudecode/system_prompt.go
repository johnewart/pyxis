package claudecodeclillm

import (
	"context"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// claudeMinimalSystemPrompt is sent when there is no configured system content.
// It must be non-empty: omitting --system-prompt, or passing "", makes the CLI
// use its full default preset (persona, tool guidance, skills, attribution).
const claudeMinimalSystemPrompt = "."

// appendClaudeCodePresetSystemPromptArgs builds the --system-prompt (and, when the preset is
// opted into, --append-system-prompt) CLI args from client-sent system content plus cfg.
//
// Default (cfg.UseClaudeCodePreset == false): the configured content (the chat's system
// message, cfg.SystemPrompt, cfg.AppendSystemPrompt and any memctx append) replaces the
// CLI's default preset via --system-prompt. --system-prompt is always passed, falling back
// to claudeMinimalSystemPrompt, because leaving it out brings the full preset back.
//
// This does NOT give us the whole prompt. Verified against the CLI (2.1.x, subscription
// login): the model still sees text the CLI adds and no flag removes:
//   - an identity line before our prompt ("You are a Claude agent, built on ...");
//   - an environment block: cwd, git status, platform, shell, OS, model, date;
//   - account context (the user's email) as a reminder in the first user message.
//
// What we do about it: the cwd is a neutral temp dir (neutral_cwd.go), and the tool/skill
// lists, attribution and personal settings are suppressed by --tools, --setting-sources ""
// and --strict-mcp-config (cli_args.go). --bare would drop more, but it requires an API
// key and breaks subscription logins.
//
// Opt-in (cfg.UseClaudeCodePreset == true): empty --system-prompt selects the CLI's full
// default preset, and configured content is layered on top via --append-system-prompt.
func appendClaudeCodePresetSystemPromptArgs(args []string, ctx context.Context, cfg Config, systemFromMessages string) []string {
	var parts []string
	if s := strings.TrimSpace(systemFromMessages); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(cfg.SystemPrompt); s != "" {
		parts = append(parts, s)
	}
	if ap := mergedAppendSystemPrompt(ctx, cfg.AppendSystemPrompt); ap != "" {
		parts = append(parts, ap)
	}
	combined := strings.Join(parts, "\n\n")

	if cfg.UseClaudeCodePreset {
		args = append(args, "--system-prompt", "")
		if combined != "" {
			args = append(args, "--append-system-prompt", combined)
		}
		return args
	}

	if combined == "" {
		combined = claudeMinimalSystemPrompt
	}
	return append(args, "--system-prompt", combined)
}

func systemContentFromMessages(messages []*llmv1.Message) string {
	for _, m := range messages {
		if m == nil {
			continue
		}
		if strings.TrimSpace(m.GetRole()) == "system" {
			return strings.TrimSpace(m.GetContent())
		}
	}
	return ""
}
