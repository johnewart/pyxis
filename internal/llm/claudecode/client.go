package claudecodeclillm

// Shared Claude Code CLI plumbing for the stream-json backend (V2Client): process spawn,
// env scrubbing (no ANTHROPIC_API_KEY, so the CLI uses its own auth), and llmd MCP config
// generation. The v1 unary --print backend was removed; V2Client is the only Claude backend.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnewart/pyxis/agentpid"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/mcpbridge"
	"github.com/johnewart/pyxis/internal/tooltimeout"
)

// Config holds Claude Code CLI options, persisted per provider in llmd's stored config document.
type Config struct {
	Binary               string
	Cwd                  string
	Agent                string
	DefaultModel         string
	SystemPrompt         string
	AppendSystemPrompt   string
	Effort               string
	PermissionMode       string
	MaxBudgetUSD         *float64
	AddDir               []string
	AllowedTools         []string
	DisallowedTools      []string
	McpConfigPath        string
	SkipAutoMCP          bool // when true, never auto-generate llmd HTTP MCP config from env
	TimeoutMs            int
	MaxTurns             int
	SettingSources       []string
	ThinkingType         string
	ThinkingBudgetTokens *int
	HooksJSON            string
	StrictMcpConfig      *bool
	IncludeHookEvents    *bool
	ConfigDir            string
	// When true, the user's own Claude config (~/.claude) is loaded normally.
	// When false (default), --setting-sources "" is passed so personal settings (e.g. plan
	// mode) cannot interfere. Not --bare: bare mode only accepts ANTHROPIC_API_KEY /
	// apiKeyHelper and would break subscription (OAuth) logins.
	UseMyClaudeConfig bool
	// When true, enable Claude Code's ToolSearch built-in so MCP tool schemas are
	// deferred (loaded on demand) instead of sent eagerly. Off by default; opt-in
	// via the client's token-saver setting (proto defer_tool_schemas, field 24).
	DeferToolSchemas bool
	// When true, use Claude Code's own full preset system prompt (persona, tool
	// guidance, etc.) with configured content appended. Off by default: configured
	// content replaces the preset via --system-prompt. Either way the CLI still adds
	// text we cannot remove (an identity line, an environment block, account
	// context); see appendClaudeCodePresetSystemPromptArgs in system_prompt.go.
	UseClaudeCodePreset bool
	// OAuthToken is a long-lived token from `claude setup-token`. When set it is
	// passed as CLAUDE_CODE_OAUTH_TOKEN; any ambient value is always stripped so a
	// provider never authenticates with another provider's (or llmd's) token.
	OAuthToken string
}

// Client carries the CLI process and MCP-config plumbing shared by V2Client.
// It is not an llm backend on its own; construct it as &Client{cfg: ...}.
type Client struct {
	cfg Config
	// tools is the turn's offered tool set; cmd() derives the CLI's
	// MCP_TOOL_TIMEOUT backstop from it (see tooltimeout.Backstop).
	tools []*llmv1.ToolDefinition
}

func (c *Client) resolvedBinary() (string, error) {
	b := c.cfg.Binary
	if filepath.Base(b) == b {
		path, err := exec.LookPath(b)
		if err != nil {
			return "", fmt.Errorf("claude-code-cli: look up %q: %w", b, err)
		}
		return path, nil
	}
	return filepath.Abs(b)
}

// claudeOAuthTokenEnv is the variable the Claude CLI reads a `claude setup-token`
// credential from.
const claudeOAuthTokenEnv = "CLAUDE_CODE_OAUTH_TOKEN"

// claudeCLIEnv returns llmd's environment with every ambient Claude credential
// stripped (ANTHROPIC_API_KEY, so the CLI never silently bills an API key, and
// CLAUDE_CODE_OAUTH_TOKEN, so the provider config is the SOLE authority for the
// token -- required when several tenants each configure their own), then sets
// the provider's own OAuth token when one is configured.
func claudeCLIEnv(oauthToken string) []string {
	return claudeCLIEnvFrom(os.Environ(), oauthToken)
}

func claudeCLIEnvFrom(env []string, oauthToken string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") || strings.HasPrefix(e, claudeOAuthTokenEnv+"=") {
			continue
		}
		out = append(out, e)
	}
	if t := strings.TrimSpace(oauthToken); t != "" {
		out = append(out, claudeOAuthTokenEnv+"="+t)
	}
	return out
}

func ensureClaudeConfigDir(dir string) error {
	d := strings.TrimSpace(dir)
	if d == "" {
		return nil
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return fmt.Errorf("claude-code-cli: create config dir %q: %w", d, err)
	}
	return nil
}

// appendClaudeConfigDirEnv makes the provider config the SOLE authority for
// CLAUDE_CONFIG_DIR: the ambient value is always stripped, and the variable is
// only set when the provider explicitly configures a dir. Inheriting the
// ambient env here meant a dev instance launched from a terminal (with
// CLAUDE_CONFIG_DIR in the shell) and a packaged instance launched from the
// Dock (without it) wrote CLI conversations into DIFFERENT stores while
// sharing one llmd DB — so --resume ids recorded by one instance were
// "No conversation found" in the other.
func appendClaudeConfigDirEnv(env []string, dir string) []string {
	d := strings.TrimSpace(dir)
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
			continue
		}
		out = append(out, e)
	}
	if d == "" {
		return out
	}
	return append(out, "CLAUDE_CONFIG_DIR="+d)
}

func (c *Client) cmd(ctx context.Context, args ...string) (*exec.Cmd, error) {
	bin, err := c.resolvedBinary()
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, bin, args...)
	// Put the CLI in its own process group and, on ctx cancel/timeout, SIGKILL the
	// whole tree. claude spawns node + MCP-server children; CommandContext's default
	// Cancel only kills the direct child PID, orphaning grandchildren that then spin
	// at ~100% CPU each (the integration-test "runaway claude" symptom). WaitDelay
	// guarantees Wait()/Run() unblocks even if the tree ignores the signal, so a
	// a turn can never hang on a runaway subprocess.
	setClaudeCLIProcessGroup(command)
	command.Cancel = func() error {
		killClaudeCLIProcessTree(command)
		return nil
	}
	command.WaitDelay = 5 * time.Second
	if err := ensureClaudeConfigDir(c.cfg.ConfigDir); err != nil {
		return nil, err
	}
	recordClaudeConfigDir(c.cfg.ConfigDir) // so PurgeSessionTranscripts can find this store
	// Last fallible step: nothing after it can fail and leak the created dir.
	if cd := strings.TrimSpace(c.cfg.Cwd); cd != "" {
		// Explicit operator choice: honour it.
		command.Dir = cd
	} else {
		// Never inherit llmd's own cwd: the CLI discloses it to the model (see
		// neutral_cwd.go). Callers must releaseNeutralCwd(cmd) once the process exits.
		dir, err := createNeutralCwd(ctx)
		if err != nil {
			return nil, err
		}
		command.Dir = dir
	}
	command.Env = appendMCPToolTimeoutEnv(appendClaudeConfigDirEnv(claudeCLIEnv(c.cfg.OAuthToken), c.cfg.ConfigDir), c.mcpToolBackstop(ctx))
	return command, nil
}

// mcpToolBackstop prefers the backstop the server attached to ctx: it was
// computed from the full tool definitions, whereas c.tools has been stripped
// for the provider (no executor max_duration_seconds), so computing from
// c.tools alone always yields Default+margin.
func (c *Client) mcpToolBackstop(ctx context.Context) time.Duration {
	if d, ok := tooltimeout.BackstopFromContext(ctx); ok {
		return d
	}
	return tooltimeout.Backstop(c.tools)
}

// appendMCPToolTimeoutEnv sets the CLI's per-call MCP tool timeout to the
// turn's backstop: the slowest offered tool's effective timeout plus a margin,
// so llmd's per-tool deadline always fires first (see tooltimeout). Any
// ambient MCP_TOOL_TIMEOUT is replaced so llmd is the single source of truth;
// otherwise the CLI's version-dependent default could cut long tools off.
func appendMCPToolTimeoutEnv(env []string, backstop time.Duration) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, "MCP_TOOL_TIMEOUT=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, fmt.Sprintf("MCP_TOOL_TIMEOUT=%d", backstop.Milliseconds()))
}

func mergeToolNames(static []string, tools []*llmv1.ToolDefinition) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, s := range static {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, t := range tools {
		if t == nil {
			continue
		}
		n := strings.TrimSpace(t.GetName())
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func llmdMCPHTTPHeadersFromContext(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}
	run, ok := agentpid.FromContext(ctx)
	if !ok {
		return nil
	}
	h := make(map[string]string)
	if t := strings.TrimSpace(run.Scope.TenantID); t != "" {
		hh := http.Header{}
		serverutil.Scope{TenantID: t, ScopeID: run.Scope.ScopeID}.ApplyHTTPHeader(hh)
		for k := range hh {
			h[k] = hh.Get(k)
		}
	}
	if t := strings.TrimSpace(run.AgentID); t != "" {
		h["X-Llmd-Agent-Id"] = t
	}
	if t := strings.TrimSpace(run.SessionID); t != "" {
		h["X-Llmd-Session-Id"] = t
	}
	if t := strings.TrimSpace(run.ClientWorkspaceID); t != "" {
		h["X-Client-Workspace-Id"] = t
	}
	if len(h) == 0 {
		return nil
	}
	return h
}

const (
	llmdClaudeMcpServerName = "llmd"
	// llmdClaudeMcpAllowedToolsWildcard is passed to Claude Code --allowed-tools so MCP tools from the
	// auto-generated llmd HTTP server (mcpServers.<name>) are permitted without interactive approval.
	llmdClaudeMcpAllowedToolsWildcard = "mcp__" + llmdClaudeMcpServerName + "__*"
)

// prepareMcpConfigPath returns a path for --mcp-config: the YAML mcp_config_path when set, otherwise
// a temp JSON file pointing at llmd HTTP MCP (POST /mcp) when mcpbridge.BaseURL resolves (the host's
// Host.StartMCP listener, or LLMD_MCP_HTTP_BASE_URL / LLMD_MCP_HTTP_ADDR).
// When the second bool is true, the temp file uses mcpServers.llmd and callers should allow llmdClaudeMcpAllowedToolsWildcard.
func (c *Client) prepareMcpConfigPath(ctx context.Context) (path string, cleanup func(), autoLlmdHTTPMCP bool, err error) {
	cleanup = func() {}
	if p := strings.TrimSpace(c.cfg.McpConfigPath); p != "" {
		return p, cleanup, false, nil
	}
	if c.cfg.SkipAutoMCP {
		return "", cleanup, false, nil
	}
	base := mcpbridge.BaseURL()
	if base == "" {
		return "", cleanup, false, nil
	}
	tmp, err := os.CreateTemp("", "llmd-claude-mcp-*.json")
	if err != nil {
		return "", cleanup, false, fmt.Errorf("claude-code-cli: temp mcp config: %w", err)
	}
	path = tmp.Name()
	cleanup = func() { _ = os.Remove(path) }

	type mcpServerEntry struct {
		Type    string            `json:"type"` // Claude Code requires explicit transport; llmd serves JSON-RPC over HTTP POST /mcp.
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers,omitempty"`
	}
	type mcpFile struct {
		MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	}
	llmdEntry := mcpServerEntry{Type: "http", URL: base + "/mcp"}
	if hdr := llmdMCPHTTPHeadersFromContext(ctx); len(hdr) > 0 {
		llmdEntry.Headers = hdr
	}
	payload := mcpFile{
		MCPServers: map[string]mcpServerEntry{
			llmdClaudeMcpServerName: llmdEntry,
		},
	}
	raw, jerr := json.MarshalIndent(payload, "", "  ")
	if jerr != nil {
		_ = tmp.Close()
		cleanup()
		return "", func() {}, false, fmt.Errorf("claude-code-cli: marshal mcp config: %w", jerr)
	}
	if _, werr := tmp.Write(append(raw, '\n')); werr != nil {
		_ = tmp.Close()
		cleanup()
		return "", func() {}, false, fmt.Errorf("claude-code-cli: write mcp config: %w", werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		cleanup()
		return "", func() {}, false, fmt.Errorf("claude-code-cli: close mcp config: %w", cerr)
	}
	return path, cleanup, true, nil
}

func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	ms := c.cfg.TimeoutMs
	if ms <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
}
