package claudecodeclillm

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdlog"
)

// claudeSafeDefaultModel is the ONLY model llmd may pick on the user's behalf.
// Whenever no model was explicitly chosen, we pass --model haiku rather than
// omitting --model (which lets the CLI pick its own, possibly expensive,
// default) or falling through to a pricey tier like fable.
const claudeSafeDefaultModel = "haiku"

const claudeModelLogModule = "claude-model"

func isFableModel(m string) bool {
	return strings.Contains(strings.ToLower(m), "fable")
}

// defaultClaudeHeadlessMCPPermissionMode is used when a client runs Claude via llmd MCP
// with no interactive permission UI (native Write/Bash prompts cannot be approved).
const defaultClaudeHeadlessMCPPermissionMode = "bypassPermissions"

// Built-in tools kept when MCP is configured. Do not pass --tools "" here:
// dropping WaitForMcpServers can stall MCP bootstrap.
// Eager (default): all MCP tool schemas are sent up front; ToolSearch is not exposed.
// Deferred: ToolSearch is enabled so the CLI defers MCP tool schemas until searched.
// Deferral is opt-in via Config.DeferToolSchemas (the client's token-saver setting).
const (
	claudeHeadlessMCPEagerBuiltinTools    = "WaitForMcpServers"
	claudeHeadlessMCPDeferredBuiltinTools = "WaitForMcpServers,ToolSearch"
)

var validClaudeEfforts = map[string]struct{}{
	"low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {},
}

var validClaudeThinkingTypes = map[string]struct{}{
	"enabled": {}, "disabled": {}, "adaptive": {},
}

func normalizeClaudeThinkingType(raw string) string {
	t := strings.ToLower(strings.TrimSpace(raw))
	if t == "" || t == "default" {
		return ""
	}
	if _, ok := validClaudeThinkingTypes[t]; ok {
		return t
	}
	return ""
}

func pickClaudeEffort(opt *llmv1.CallOptions, fallback string) string {
	if opt != nil && opt.ClaudeCode != nil {
		if e := strings.ToLower(strings.TrimSpace(opt.ClaudeCode.GetEffort())); e != "" {
			return e
		}
	}
	if opt != nil {
		if inferred := inferClaudeEffortFromProfileID(opt.GetModel()); inferred != "" {
			return inferred
		}
	}
	return strings.ToLower(strings.TrimSpace(fallback))
}

func pickClaudeMaxBudgetUSD(opt *llmv1.CallOptions, fallback *float64) *float64 {
	if opt != nil && opt.ClaudeCode != nil && opt.ClaudeCode.MaxBudgetUsd != nil {
		v := opt.ClaudeCode.GetMaxBudgetUsd()
		return &v
	}
	return fallback
}

func inferClaudeBaseModelFromProfileID(catalogID string) string {
	base, _ := splitClaudeProfileID(catalogID)
	return base
}

// splitClaudeProfileID splits a catalog/profile id (e.g.
// "opus-low-enabled", "sonnet-low-default-usd0_50") into the matched base
// model prefix and whatever remains (effort/thinking/budget tokens), using
// the longest-first prefix table derived from claude_base_models.json.
func splitClaudeProfileID(catalogID string) (base, rest string) {
	id := strings.ToLower(strings.TrimSpace(catalogID))
	// Canonical ids are "provider/model" (e.g. "claude-code/haiku"); match on
	// the model part, or a selector choice of haiku resolves to nothing and a
	// stale base_model (fable) wins instead.
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	if id == "" {
		return "", ""
	}
	for _, prefix := range ClaudeBaseModelPrefixes() {
		if id == prefix {
			return prefix, ""
		}
		if strings.HasPrefix(id, prefix+"-") {
			return prefix, strings.TrimPrefix(id, prefix+"-")
		}
	}
	return "", ""
}

// inferClaudeEffortFromProfileID extracts an --effort value ("low",
// "medium", ...) from the segment of a profile/catalog id that follows the
// base model prefix, e.g. "opus-low-enabled" -> "low".
func inferClaudeEffortFromProfileID(catalogID string) string {
	_, rest := splitClaudeProfileID(catalogID)
	if rest == "" {
		return ""
	}
	for _, part := range strings.Split(rest, "-") {
		if _, ok := validClaudeEfforts[part]; ok {
			return part
		}
	}
	return ""
}

// inferClaudeThinkingTypeFromProfileID extracts a thinking type
// ("enabled"/"disabled"/"adaptive") from the segment of a profile/catalog id
// that follows the base model prefix, e.g. "opus-low-enabled" -> "enabled".
func inferClaudeThinkingTypeFromProfileID(catalogID string) string {
	_, rest := splitClaudeProfileID(catalogID)
	if rest == "" {
		return ""
	}
	for _, part := range strings.Split(rest, "-") {
		if _, ok := validClaudeThinkingTypes[part]; ok {
			return part
		}
	}
	return ""
}

// ClaudeEffortLevels returns the CLI's supported --effort values, in catalog
// display order (least to most effort).
func ClaudeEffortLevels() []string {
	return []string{"low", "medium", "high", "xhigh", "max"}
}

// ClaudeEffortLabel returns a human-friendly label for an --effort value.
func ClaudeEffortLabel(effort string) string {
	switch effort {
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	case "high":
		return "High"
	case "xhigh":
		return "Extra High"
	case "max":
		return "Max"
	default:
		return effort
	}
}

// pickClaudeModel resolves the --model value. The result is ALWAYS a model id
// from claude_base_models.json; anything else resolves to haiku (the cheapest
// tier). Precedence:
//  1. a known tier named by opt.Model (session/selector model, e.g.
//     "opus-low-enabled" or "claude-code/haiku")
//  2. a known tier named by opt.ClaudeCode.BaseModel (for opaque profile ids)
//  3. if either of the above was set but unrecognized: haiku (never guess up)
//  4. a known tier named by the provider default — but NEVER fable
//  5. haiku
//
// Never returns "": an omitted --model lets the CLI choose its own default,
// which may be an expensive tier.
func pickClaudeModel(opt *llmv1.CallOptions, fallback string) string {
	var requested, base string
	if opt != nil {
		requested = strings.TrimSpace(opt.GetModel())
		if opt.ClaudeCode != nil {
			base = strings.TrimSpace(opt.ClaudeCode.GetBaseModel())
		}
	}
	if m := inferClaudeBaseModelFromProfileID(requested); m != "" {
		return m
	}
	if m := inferClaudeBaseModelFromProfileID(base); m != "" {
		return m
	}
	if requested != "" || base != "" {
		llmdlog.Stdout(claudeModelLogModule, llmdlog.LevelWarn,
			"unrecognized Claude model (model=%q base_model=%q); using %q", requested, base, claudeSafeDefaultModel)
		return claudeSafeDefaultModel
	}
	fb := strings.TrimSpace(fallback)
	if m := inferClaudeBaseModelFromProfileID(fb); m != "" && !isFableModel(m) {
		return m
	}
	llmdlog.Stdout(claudeModelLogModule, llmdlog.LevelWarn,
		"no usable Claude model for this call (provider default=%q); using %q", fb, claudeSafeDefaultModel)
	return claudeSafeDefaultModel
}

func pickClaudeThinkingType(opt *llmv1.CallOptions, fallback string) string {
	if opt != nil && opt.ClaudeCode != nil {
		if t := normalizeClaudeThinkingType(opt.ClaudeCode.GetThinkingType()); t != "" {
			return t
		}
	}
	if opt != nil {
		if inferred := normalizeClaudeThinkingType(inferClaudeThinkingTypeFromProfileID(opt.GetModel())); inferred != "" {
			return inferred
		}
	}
	return normalizeClaudeThinkingType(fallback)
}

func pickClaudeThinkingBudgetTokens(opt *llmv1.CallOptions, fallback *int) *int {
	if opt != nil && opt.ClaudeCode != nil && opt.ClaudeCode.ThinkingBudgetTokens != nil {
		v := int(opt.ClaudeCode.GetThinkingBudgetTokens())
		return &v
	}
	return fallback
}

func appendClaudeEffortArg(args []string, effort string) []string {
	e := strings.ToLower(strings.TrimSpace(effort))
	if _, ok := validClaudeEfforts[e]; ok {
		return append(args, "--effort", e)
	}
	return args
}

func appendClaudeSettingsArgs(args []string, cfg Config, opt *llmv1.CallOptions) []string {
	settings := map[string]any{}
	if t := pickClaudeThinkingType(opt, cfg.ThinkingType); t != "" {
		thinking := map[string]any{"type": t}
		if t != "disabled" {
			if budget := pickClaudeThinkingBudgetTokens(opt, cfg.ThinkingBudgetTokens); budget != nil && *budget > 0 {
				thinking["budget_tokens"] = *budget
			}
		}
		settings["thinking"] = thinking
	}
	if hooks := strings.TrimSpace(cfg.HooksJSON); hooks != "" {
		var hooksObj any
		if json.Unmarshal([]byte(hooks), &hooksObj) == nil {
			settings["hooks"] = hooksObj
		}
	}
	if len(settings) == 0 {
		return args
	}
	b, err := json.Marshal(settings)
	if err != nil {
		return args
	}
	return append(args, "--settings", string(b))
}

func appendClaudeProviderCLIArgs(args []string, cfg Config, opt *llmv1.CallOptions) []string {
	args = appendClaudeEffortArg(args, pickClaudeEffort(opt, cfg.Effort))
	if pm := strings.TrimSpace(cfg.PermissionMode); pm != "" {
		args = append(args, "--permission-mode", pm)
	}
	if maxBudget := pickClaudeMaxBudgetUSD(opt, cfg.MaxBudgetUSD); maxBudget != nil {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%g", *maxBudget))
	}
	if cfg.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(cfg.MaxTurns))
	}
	// When UseMyClaudeConfig is false (default), suppress all user/project/local settings
	// sources so the personal Claude config cannot interfere with agent runs
	// (e.g. plan mode stuck on, unexpected hooks). Credentials live in a separate store and
	// are unaffected by --setting-sources.
	// When UseMyClaudeConfig is true, respect whatever SettingSources the user configured
	// (empty = Claude's default of loading all sources).
	if !cfg.UseMyClaudeConfig {
		args = append(args, "--setting-sources", "")
	} else if len(cfg.SettingSources) > 0 {
		var src []string
		for _, s := range cfg.SettingSources {
			s = strings.TrimSpace(s)
			if s != "" {
				src = append(src, s)
			}
		}
		if len(src) > 0 {
			args = append(args, "--setting-sources", strings.Join(src, ","))
		}
	}
	// Strict MCP config is ON by default: unset (nil) means strict, so the client
	// agent runs only ever see the MCP servers llmd passed via --mcp-config and
	// never the user's personal ~/.claude.json servers. Only an explicit false
	// opts out.
	if cfg.StrictMcpConfig == nil || *cfg.StrictMcpConfig {
		args = append(args, "--strict-mcp-config")
	}
	if cfg.IncludeHookEvents != nil && *cfg.IncludeHookEvents {
		args = append(args, "--include-hook-events")
	}
	args = appendClaudeSettingsArgs(args, cfg, opt)
	return args
}

func claudeCLIHasArg(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

// appendClaudeNoBuiltinToolsArgs disables Claude Code's built-in tools for a spawn
// that has no llmd MCP bridge (e.g. a tool-less one-shot). llmd is a headless
// agent runtime: the model may only act through tools llmd hands it, never through
// the CLI's own Bash/Read/Write/Edit/WebFetch. Before this existed, a no-MCP spawn
// passed no --tools at all and the CLI enabled its full native set.
//
// It reuses the headless allowlist, which with no MCP server configured resolves
// to zero tools (measured: see errClaudeMCPInventoryEmpty). An operator who
// configured AllowedTools explicitly opted into specific built-ins, so that is
// left alone, as is a --tools value something earlier already set.
func appendClaudeNoBuiltinToolsArgs(args []string, cfg Config) []string {
	if claudeCLIHasArg(args, "--tools") || len(cfg.AllowedTools) > 0 {
		return args
	}
	return append(args, "--tools", claudeHeadlessMCPEagerBuiltinTools)
}

// appendClaudeHeadlessMCPArgs configures headless MCP sessions: skip permission
// prompts and disable built-in Claude Code tools so only MCP tools (write_code, etc.) are used.
func appendClaudeHeadlessMCPArgs(args []string, cfg Config) []string {
	if !claudeCLIHasArg(args, "--permission-mode") {
		pm := strings.TrimSpace(cfg.PermissionMode)
		if pm == "" {
			pm = defaultClaudeHeadlessMCPPermissionMode
		}
		args = append(args, "--permission-mode", pm)
	}
	if !claudeCLIHasArg(args, "--tools") {
		builtins := claudeHeadlessMCPEagerBuiltinTools
		if cfg.DeferToolSchemas {
			builtins = claudeHeadlessMCPDeferredBuiltinTools
		}
		args = append(args, "--tools", builtins)
	}
	return args
}
