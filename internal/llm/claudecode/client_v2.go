package claudecodeclillm

// Claude Code CLI v2: stream-json transport with optional --resume (stateful via llmd ProviderSessionSync).

import (
	"context"
	"fmt"
	"os"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdlog"
)

// V2Client implements backend.Backend with Claude Code session resume.
type V2Client struct {
	cfg Config
}

// NewV2 builds a stateful Claude Code CLI client (same Config shape as legacy).
func NewV2(cfg Config) *V2Client {
	if strings.TrimSpace(cfg.Binary) == "" {
		cfg.Binary = strings.TrimSpace(os.Getenv("CLAUDE_CLI_PATH"))
	}
	if strings.TrimSpace(cfg.Binary) == "" {
		cfg.Binary = "claude"
	}
	return &V2Client{cfg: cfg}
}

func (c *V2Client) buildStreamArgsV2(ctx context.Context, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition, mcpConfigPath string, autoLlmdHTTPMCP bool, systemFromMessages string) []string {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
	}
	if sid := ResumeSessionIDFromContext(ctx); sid != "" {
		args = append(args, "--resume", sid)
	}
	if a := strings.TrimSpace(c.cfg.Agent); a != "" {
		args = append(args, "--agent", a)
	}
	model := pickClaudeModel(opt, c.cfg.DefaultModel)
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", model)
	}
	llmdlog.Stdout(claudeModelLogModule, llmdlog.LevelInfo,
		"claude cli request => requested_model=%q cli_model=%q", opt.GetModel(), model)
	args = appendClaudeCodePresetSystemPromptArgs(args, ctx, c.cfg, systemFromMessages)
	args = appendClaudeProviderCLIArgs(args, c.cfg, opt)
	for _, d := range c.cfg.AddDir {
		d = strings.TrimSpace(d)
		if d != "" {
			args = append(args, "--add-dir", d)
		}
	}
	staticAllowed := append([]string(nil), c.cfg.AllowedTools...)
	if autoLlmdHTTPMCP {
		staticAllowed = append(staticAllowed, llmdClaudeMcpAllowedToolsWildcard)
	}
	allowed := mergeToolNames(staticAllowed, tools)
	if len(allowed) > 0 {
		args = append(args, "--allowed-tools")
		args = append(args, allowed...)
	}
	var disallowed []string
	for _, d := range c.cfg.DisallowedTools {
		d = strings.TrimSpace(d)
		if d != "" {
			disallowed = append(disallowed, d)
		}
	}
	if len(disallowed) > 0 {
		args = append(args, "--disallowed-tools")
		args = append(args, disallowed...)
	}
	mcpPath := strings.TrimSpace(mcpConfigPath)
	if mcpPath != "" {
		args = append(args, "--mcp-config", mcpPath)
	}
	if mcpPath != "" || autoLlmdHTTPMCP {
		args = appendClaudeHeadlessMCPArgs(args, c.cfg)
	} else {
		// No MCP bridge for this spawn. Claude Code must STILL not get its native
		// tools: without this, a call that reaches llmd with no tools ran with
		// Bash/Read/Write/Edit/WebFetch/... fully enabled.
		args = appendClaudeNoBuiltinToolsArgs(args, c.cfg)
	}
	// --print satisfies --include-partial-messages; -p "" selects headless stream-json stdin mode.
	args = append(args, "-p", "")
	return args
}

// Chat runs a single non-streaming CLI invocation with resume support.
func (c *V2Client) Chat(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	out, err := c.runStreamV2(ctx, messages, opt, tools, nil)
	if err != nil {
		return nil, err
	}
	return out.reply, nil
}

// Generate maps system + user into one prompt and runs the CLI.
func (c *V2Client) Generate(ctx context.Context, systemPrompt, prompt string, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("claude-code-cli-v2: prompt must be non-empty")
	}
	var msgs []*llmv1.Message
	if t := strings.TrimSpace(systemPrompt); t != "" {
		msgs = append(msgs, &llmv1.Message{Role: "system", Content: t})
	}
	msgs = append(msgs, &llmv1.Message{Role: "user", Content: prompt})
	return c.Chat(ctx, msgs, opt, tools)
}

// ChatStream runs the CLI with native stream-json output and structured stdin user frames.
func (c *V2Client) ChatStream(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, toolsFn func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	if emit == nil {
		return fmt.Errorf("claude-code-cli-v2: emit callback is required")
	}
	var tools []*llmv1.ToolDefinition
	if toolsFn != nil {
		tools = toolsFn()
	}
	_, err := c.runStreamV2(ctx, messages, opt, tools, emit)
	return err
}
