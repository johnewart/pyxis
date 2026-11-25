package claudecodeclillm

// MCP inventory gate.
//
// An agent is useless without their MCP tools: the system prompt tells the
// model it must use write_code/search/etc, so a spawn that resolves zero MCP tools
// produces a model that fabricates tool calls as prose instead of failing. Previously
// nothing checked the resolved inventory, so a lost MCP-connect race degraded silently
// and intermittently. This gate turns that into a loud, retryable error.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnewart/pyxis/llmdlog"
)

// claudeMCPInventoryWaitTimeout bounds the post-user-frame wait for system/init.
// On timeout we proceed rather than fail: the pre-existing behaviour is no worse,
// and failing turns because a machine was slow would trade one bug for another.
const claudeMCPInventoryWaitTimeout = 45 * time.Second

// claudeMCPInventoryPendingGraceTimeout bounds an extra wait, after an unusable
// init frame whose servers are still "pending" (connecting) rather than "failed",
// for a follow-up system frame that reflects the settled connection state. The
// llmd MCP HTTP handshake can complete before the CLI's own init snapshot catches
// up (observed: mcp_tools_list succeeds server-side, yet init still reports
// mcp_servers=[llmd:pending] tools=0) -- this is a lost race in the CLI's own
// async connect-vs-emit ordering, not an llmd failure, so it is worth a short
// second look before giving up.
const claudeMCPInventoryPendingGraceTimeout = 5 * time.Second

// errClaudeMCPInventoryEmpty marks a spawn whose system/init reported no usable MCP
// tools. Callers retry once on this before surfacing it.
//
// Do NOT split this on len(tools)==0 as if that meant "broken flags, do not retry":
// llmd passes --tools with a built-in allowlist, so a spawn whose MCP server has not
// finished connecting legitimately reports ZERO tools. Zero is the normal shape of the
// very race this gate retries, not evidence of a config fault. Measured directly: a
// spawn with --tools and no MCP config reports tools=0 mcp_servers=[] on a CLI where
// real turns work fine.
var errClaudeMCPInventoryEmpty = errors.New("claude MCP tool inventory empty")

// claudeMCPToolPrefix is the namespace Claude Code gives tools from the llmd MCP server.
var claudeMCPToolPrefix = "mcp__" + llmdClaudeMcpServerName + "__"

// claudeToolSearchToolName is the built-in the CLI exposes when tool schemas are
// deferred; with deferral on, MCP tools legitimately do not appear at init and
// ToolSearch is the model's only route to them.
const claudeToolSearchToolName = "ToolSearch"

// claudeInitFrame is the subset of system/init the gate inspects.
type claudeInitFrame struct {
	toolNames  []string
	mcpServers string
	hasPending bool // true when any mcp_servers[].status == "pending" (still connecting, not failed)
}

// parseClaudeInitFrame returns the init payload when line is a system/init frame.
// A missing or malformed tools array yields an empty list, which is exactly the
// condition the gate exists to catch, so it must not be reported as "not an init".
func parseClaudeInitFrame(line []byte) (claudeInitFrame, bool) {
	var obj map[string]any
	if json.Unmarshal(trimClaudeStreamJSONLine(line), &obj) != nil {
		return claudeInitFrame{}, false
	}
	if typ, _ := obj["type"].(string); typ != "system" {
		return claudeInitFrame{}, false
	}
	if subtype, _ := obj["subtype"].(string); subtype != "init" {
		return claudeInitFrame{}, false
	}
	names := make([]string, 0, 8)
	for _, t := range sliceLen(obj["tools"]) {
		if s, ok := t.(string); ok && strings.TrimSpace(s) != "" {
			names = append(names, strings.TrimSpace(s))
		}
	}
	return claudeInitFrame{
		toolNames:  names,
		mcpServers: claudeInitMCPServers(obj["mcp_servers"]),
		hasPending: claudeInitHasPendingServer(obj["mcp_servers"]),
	}, true
}

// claudeInitHasPendingServer reports whether any entry in mcp_servers reports
// status "pending" -- i.e. still connecting, as opposed to "failed" or absent.
func claudeInitHasPendingServer(v any) bool {
	for _, s := range sliceLen(v) {
		m, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if status, _ := m["status"].(string); status == "pending" {
			return true
		}
	}
	return false
}

// claudeInventoryUsable reports whether the resolved tool list gives the model a
// route to MCP tools: either the tools themselves, or ToolSearch when deferred.
func claudeInventoryUsable(names []string, deferred bool) bool {
	for _, n := range names {
		if deferred && n == claudeToolSearchToolName {
			return true
		}
		if !deferred && strings.HasPrefix(n, claudeMCPToolPrefix) {
			return true
		}
	}
	return false
}

// verifyClaudeMCPInventory waits (bounded) for system/init and checks that MCP tools
// actually resolved.
//
// The wait happens *after* the first user frame is written, not before: the CLI emits
// init only once stdin traffic arrives, so gating earlier deadlocks. Non-init lines
// stay buffered for the stream consumer; the init line itself is consumed (nothing
// downstream needs it — session_id is captured during the scan).
//
// Returns errClaudeMCPInventoryEmpty when init reports an unusable inventory, nil when
// the inventory is good or when init never arrived in time.
func verifyClaudeMCPInventory(
	ctx context.Context,
	src *streamJSONLineSource,
	stderrSnapshot func() string,
	procDone <-chan struct{},
	deferred bool,
	log cliLog,
) error {
	waitCtx, cancel := context.WithTimeout(ctx, claudeMCPInventoryWaitTimeout)
	defer cancel()

	var frame claudeInitFrame
	match := func(line []byte) bool {
		f, ok := parseClaudeInitFrame(line)
		if ok {
			frame = f
		}
		return ok
	}

	if err := src.waitForLine(waitCtx, match, stderrSnapshot, procDone, "system/init"); err != nil {
		// Parent cancellation/timeout is a real failure; only our own bounded wait is soft.
		if ctx.Err() != nil {
			return err
		}
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			llmdlog.Stdout(log.module, llmdlog.LevelWarn,
				"mcp inventory gate: spawn=%s no system/init within %s; proceeding unverified",
				log.spawn, claudeMCPInventoryWaitTimeout)
			return nil
		}
		return err
	}

	if claudeInventoryUsable(frame.toolNames, deferred) {
		return nil
	}

	// Servers reported "pending" (still connecting) rather than "failed"/absent get one
	// short extra look: the CLI's init snapshot can race ahead of its own MCP client
	// finishing the handshake even when the server side (llmd) already completed it.
	if frame.hasPending {
		graceCtx, graceCancel := context.WithTimeout(ctx, claudeMCPInventoryPendingGraceTimeout)
		err := src.waitForLine(graceCtx, match, stderrSnapshot, procDone, "system/init (pending settle)")
		graceCancel()
		switch {
		case err == nil:
			if claudeInventoryUsable(frame.toolNames, deferred) {
				llmdlog.Stdout(log.module, llmdlog.LevelInfo,
					"mcp inventory gate: spawn=%s pending server settled to usable inventory", log.spawn)
				return nil
			}
		case ctx.Err() != nil:
			return err
			// context.DeadlineExceeded on graceCtx (no follow-up frame arrived) falls through
			// to the error below with whatever frame we last saw.
		}
	}

	// Name the binary and version: a stale or version-pinned CLI is the cheapest cause to
	// rule out, and nothing else in this error points at the executable.
	hint := ""
	if claudeCLIVersionTooOld(log.version) {
		hint = " -- " + claudeVersionTooOldError(log.version)
	}
	return fmt.Errorf("%w: spawn=%s %s deferred=%t tools=%d [%s] mcp_servers=[%s]%s",
		errClaudeMCPInventoryEmpty, log.spawn, log.describeBinary(), deferred, len(frame.toolNames),
		strings.Join(frame.toolNames, ","), frame.mcpServers, hint)
}
