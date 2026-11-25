package claudecodeclillm

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdlog"
)

const debugMaxCLIStderrRunes = 8192

// initToolNameLogLimit caps the tool-name list on the always-on init line so one
// spawn cannot flood the log; the count is always exact even when names truncate.
const initToolNameLogLimit = 40

// DebugEnabled reports whether LLMD_DEBUG is on (verbose Claude CLI
// subprocess diagnostics).
func DebugEnabled() bool { return llmdlog.DebugEnabled() }

// ChatDebugEnabled reports whether LLMD_DEBUG_CHAT (or a chat debug file) is
// on (verbose LLM / chat diagnostics).
func ChatDebugEnabled() bool { return llmdlog.ChatDebugEnabled() }

func claudeCLIDebugEnabled() bool {
	return DebugEnabled() || ChatDebugEnabled()
}

// cliLog tags every log line from one CLI subprocess with a spawn id, so argv
// (DEBUG) and init/result (INFO) lines can be joined with `grep spawn=<id>`.
// The spawn id exists because Claude's own session_id is not known until the
// init frame arrives, and is empty entirely on a full resync -- so it cannot
// serve as the join key for the argv line emitted at exec time.
type cliLog struct {
	module string
	spawn  string
	// bin and version identify WHICH claude binary produced this spawn. They are
	// carried on every log line and folded into the MCP inventory gate error because
	// a stale-but-valid binary (e.g. a config pinned at an immutable versions/<n>
	// install dir) fails deep in the MCP handshake, where nothing else points at the
	// executable as the cause.
	bin     string
	version string
}

// describeBinary renders "bin=... version=..." for log lines and errors.
func (l cliLog) describeBinary() string {
	bin := strings.TrimSpace(l.bin)
	if bin == "" {
		bin = "unknown"
	}
	ver := strings.TrimSpace(l.version)
	if ver == "" {
		ver = "unknown"
	}
	return fmt.Sprintf("bin=%s version=%s", strconv.Quote(bin), ver)
}

func newCLILog(module string) cliLog {
	return cliLog{module: module, spawn: newClaudeSpawnID()}
}

func newClaudeSpawnID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// claudeRedactedArgFlags are flags whose *values* are prompt text (or bulky
// settings JSON) and are replaced with a length marker. Every other flag --
// notably --tools, --strict-mcp-config, --setting-sources, --mcp-config and
// --allowed-tools -- is logged verbatim, since those decide the tool inventory.
var claudeRedactedArgFlags = map[string]struct{}{
	"--system-prompt":        {},
	"--append-system-prompt": {},
	"--settings":             {},
}

func redactClaudeCLIArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		out = append(out, strconv.Quote(args[i]))
		if _, ok := claudeRedactedArgFlags[args[i]]; ok && i+1 < len(args) {
			out = append(out, fmt.Sprintf("<%d chars redacted>", len([]rune(args[i+1]))))
			i++
		}
	}
	return out
}

// exec logs the argv for one spawn with prompt-bearing values redacted.
// resumeID records whether this spawn resumed a Claude session or full-resynced,
// which selects materially different transcript-delivery paths in llmd.
//
// This is INFO, not DEBUG. The flags logged here (--tools, --mcp-config,
// --allowed-tools, --setting-sources) plus the resolved binary and its version are
// exactly what determines whether a spawn comes up with tools at all; when it does
// not, the failure surfaces far from its cause. Hiding argv behind LLMD_DEBUG=1 meant the
// one line that identified the problem was the one line nobody had. Prompt-bearing
// values are redacted by redactClaudeCLIArgs, so INFO leaks nothing sensitive.
func (l cliLog) exec(bin string, args []string, resumeID string) {
	resume := "none"
	if r := strings.TrimSpace(resumeID); r != "" {
		resume = r
	}
	llmdlog.Stdout(l.module, llmdlog.LevelInfo, "claude-code-cli exec: spawn=%s resume=%s %s argv=[%s]",
		l.spawn, resume, l.describeBinary(), strings.Join(redactClaudeCLIArgs(args), " "))
}

func (l cliLog) stderrLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" || !claudeCLIDebugEnabled() {
		return
	}
	llmdlog.Stdout(l.module, llmdlog.LevelDebug, "claude-code-cli stderr: spawn=%s %s", l.spawn, truncateForLog(line))
}

func (l cliLog) stdoutLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	// init/result carry the resolved tool inventory and token usage: always INFO.
	if l.logKeyStdoutFrame(line) {
		return
	}
	// Everything else is raw per-chunk stream traffic (content_block_delta, etc.)
	// which floods the log on every streamed token: gated behind LLMD_DEBUG_TRAFFIC=1.
	if !llmdlog.TrafficDebugEnabled() {
		return
	}
	llmdlog.Stdout(l.module, llmdlog.LevelDebug, "claude-code-cli stdout: spawn=%s %s",
		l.spawn, truncateForLog(summarizeClaudeCLIStdoutLineForDebug(line)))
}

// logKeyStdoutFrame emits always-on INFO lines for the two frames worth having on
// every run: system/init (what tools the CLI *actually* resolved, vs what we asked
// for) and result (token usage, incl. cache read/write). Returns true when handled.
func (l cliLog) logKeyStdoutFrame(line string) bool {
	var obj map[string]any
	if json.Unmarshal([]byte(line), &obj) != nil {
		return false
	}
	typ, _ := obj["type"].(string)
	switch typ {
	case "system":
		if subtype, _ := obj["subtype"].(string); subtype == "init" {
			sid, _ := obj["session_id"].(string)
			tools := claudeInitToolNames(obj["tools"])
			llmdlog.Stdout(l.module, llmdlog.LevelInfo,
				"claude-code-cli init: spawn=%s session_id=%q tools=%d [%s] mcp_servers=[%s]",
				l.spawn, sid, len(sliceLen(obj["tools"])), tools, claudeInitMCPServers(obj["mcp_servers"]))
			return true
		}
	case "result":
		subtype, _ := obj["subtype"].(string)
		sid, _ := obj["session_id"].(string)
		llmdlog.Stdout(l.module, llmdlog.LevelInfo,
			"claude-code-cli result: spawn=%s session_id=%q subtype=%q %s",
			l.spawn, sid, subtype, formatClaudeTokenUsage(tokenUsageFromClaudeResultMap(obj)))
		return true
	}
	return false
}

// claudeInitToolNames renders the CLI's resolved tool list as sorted names.
// Names (not a bare count) are the point: diffing intended vs resolved inventory
// is what identifies a polluted tool set (e.g. MCP servers merged from the
// user's personal Claude config when --strict-mcp-config is absent).
func claudeInitToolNames(v any) string {
	arr := sliceLen(v)
	names := make([]string, 0, len(arr))
	for _, t := range arr {
		if s, ok := t.(string); ok && strings.TrimSpace(s) != "" {
			names = append(names, s)
		}
	}
	sort.Strings(names)
	if len(names) > initToolNameLogLimit {
		extra := len(names) - initToolNameLogLimit
		names = append(names[:initToolNameLogLimit:initToolNameLogLimit], fmt.Sprintf("…+%d more", extra))
	}
	return strings.Join(names, ",")
}

// claudeInitMCPServers renders "name:status" pairs; a server that failed to
// connect explains a missing tool far faster than a raw count does.
func claudeInitMCPServers(v any) string {
	arr := sliceLen(v)
	out := make([]string, 0, len(arr))
	for _, s := range arr {
		m, ok := s.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		status, _ := m["status"].(string)
		if status == "" {
			status = "unknown"
		}
		out = append(out, fmt.Sprintf("%s:%s", name, status))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// formatClaudeTokenUsage renders result-line usage. cache_read vs cache_write is
// the measurement that shows whether resumed sessions actually buy cache hits.
func formatClaudeTokenUsage(u *llmv1.TokenUsage) string {
	if u == nil {
		return "usage=none"
	}
	s := fmt.Sprintf("in=%d out=%d cache_read=%d cache_write=%d",
		u.GetInputTokens(), u.GetOutputTokens(), u.GetCacheReadTokens(), u.GetCacheWriteTokens())
	if u.TotalCostUsd != nil {
		s += fmt.Sprintf(" cost_usd=%.4f", u.GetTotalCostUsd())
	}
	return s
}

func truncateForLog(line string) string {
	r := []rune(line)
	if len(r) > debugMaxCLIStderrRunes {
		return string(r[:debugMaxCLIStderrRunes]) + "…(truncated)"
	}
	return line
}

// summarizeClaudeCLIStdoutLineForDebug replaces bulky stream-json frames (notably the
// initialize control_response catalog) with short summaries so LLMD_DEBUG=1 stays readable.
// system/init and result are handled by logKeyStdoutFrame at INFO and do not reach here.
func summarizeClaudeCLIStdoutLineForDebug(line string) string {
	var obj map[string]any
	if json.Unmarshal([]byte(line), &obj) != nil {
		return line
	}
	typ, _ := obj["type"].(string)
	switch typ {
	case "control_response":
		subtype := ""
		if resp, ok := obj["response"].(map[string]any); ok {
			subtype, _ = resp["subtype"].(string)
			if inner, ok := resp["response"].(map[string]any); ok {
				return fmt.Sprintf(
					`{"type":"control_response","subtype":%q,"commands":%d,"agents":%d,"models":%d}`,
					subtype,
					len(sliceLen(inner["commands"])),
					len(sliceLen(inner["agents"])),
					len(sliceLen(inner["models"])),
				)
			}
		}
		return fmt.Sprintf(`{"type":"control_response","subtype":%q}`, subtype)
	case "system":
		subtype, _ := obj["subtype"].(string)
		if subtype == "status" {
			status, _ := obj["status"].(string)
			return fmt.Sprintf(`{"type":"system","subtype":"status","status":%q}`, status)
		}
	case "stream_event":
		if ev, ok := obj["event"].(map[string]any); ok {
			evType, _ := ev["type"].(string)
			return fmt.Sprintf(`{"type":"stream_event","event":%q}`, evType)
		}
	case "assistant":
		if msg, ok := obj["message"].(map[string]any); ok {
			return fmt.Sprintf(`{"type":"assistant","content_blocks":%d}`, len(sliceLen(msg["content"])))
		}
	}
	return line
}

func sliceLen(v any) []any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	return arr
}

// startClaudeCLIStderrLogger drains r and logs each line when LLMD_DEBUG=1 or
// chat debugging is on.
func startClaudeCLIStderrLogger(log cliLog, r io.Reader) func() string {
	var buf bytes.Buffer
	if r == nil {
		return func() string { return "" }
	}
	var mu sync.Mutex
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := sc.Text()
			mu.Lock()
			buf.WriteString(line)
			buf.WriteByte('\n')
			mu.Unlock()
			log.stderrLine(line)
		}
		// os.ErrClosed is our own teardown (claudeCLIStdio.Close); anything else
		// (read failure, line > bufio.MaxScanTokenSize) means stderr capture stopped
		// early and later CLI diagnostics were lost.
		if err := sc.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
			llmdlog.Stdout("claude-code-cli", llmdlog.LevelWarn,
				"stderr capture stopped early (later CLI stderr is lost): %v", err)
		}
	}()
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}
