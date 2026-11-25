package server

import (
	"context"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/llmdlog"
)

// Sources recorded on llmd_tool_usage rows, one per tool dispatch path.
const (
	ToolUsageSourceChat       = "chat"       // API-provider loop (executeToolForStream)
	ToolUsageSourceInvokeTool = "invoke_tool" // InvokeTool RPC
	ToolUsageSourceMCP        = "mcp"        // MCP tools/call
)

// RecordToolUsage writes one llmd_tool_usage row on a context detached from the
// request, bounded by a timeout. A write failure is logged and never changes
// the tool result. No-op when tool usage persistence is off.
func (s *LlmServer) RecordToolUsage(ctx context.Context, tool, source, sessionID string, success bool, d time.Duration) {
	if s == nil || s.ToolUsage == nil {
		return
	}
	var sc serverutil.Scope
	if resolved, err := s.scope(ctx); err == nil {
		sc = resolved
	} else {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"tool usage scope resolution failed: tool=%q source=%q err=%v", tool, source, err)
	}
	s.RecordToolUsageScoped(ctx, sc, tool, source, sessionID, success, d)
}

// RecordToolUsageScoped is RecordToolUsage for callers that already hold the
// validated scope (the MCP path, whose ctx carries no gRPC metadata).
func (s *LlmServer) RecordToolUsageScoped(ctx context.Context, sc serverutil.Scope, tool, source, sessionID string, success bool, d time.Duration) {
	if s == nil || s.ToolUsage == nil {
		return
	}
	tenant, scope := sc.TenantID, sc.ScopeID
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), usageWriteTimeout)
	defer cancel()
	row := store.ToolUsageRow{
		TenantID:   tenant,
		ScopeID:    scope,
		SessionID:  sessionID,
		ToolName:   tool,
		Source:     source,
		Success:    success,
		DurationMs: d.Milliseconds(),
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.ToolUsage.Record(wctx, row); err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"record tool usage failed: tenant=%q scope=%q tool=%q source=%q err=%v",
			tenant, scope, tool, source, err)
	}
}
