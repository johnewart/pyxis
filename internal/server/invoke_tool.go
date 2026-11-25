package server

// InvokeTool serves a single non-chat tool call: an RPC caller (e.g. a
// Connect client) names a registered builtin by name and gets back its
// content, without a chat turn or a stream. It is a thin wrapper over the
// SAME registry lookup and toolexec.RunWithHandlers execution the agent
// loop uses in executeToolForStream — no second dispatch table, no direct
// handler call.
//
// It is also the side door for per-session tool gating, and the one most easily
// forgotten because it sits outside the agent loop: it is reachable by any
// client with a hand-crafted request. When the request names a session, the
// tool MUST be in that session's effective set (see toolAllowedForSession) or
// the call is refused — hiding a tool from the model's list does nothing if
// this handler will still run it.

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolseam"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// lookupToolDefinition finds a registered tool by name in the composed
// registry. Returns nil when the name is unknown — RunWithHandlers already
// answers "tool is not registered" for a nil def, so no separate unknown-name
// branch is needed here.
func lookupToolDefinition(reg interface {
	Snapshot() []*llmv1.ToolDefinition
}, name string) *llmv1.ToolDefinition {
	if reg == nil {
		return nil
	}
	for _, d := range reg.Snapshot() {
		if d.GetName() == name {
			return d
		}
	}
	return nil
}

// InvokeTool executes one registered builtin tool by name for a caller that
// is not running a chat turn. Incoming gRPC metadata is the only binding for
// which tenant/scope the tool reads: the scope is resolved from ctx exactly as
// for the chat loop, and no request body field can override it. The request's
// workspace_id field is ignored.
func (s *LlmServer) InvokeTool(ctx context.Context, req *llmv1.InvokeToolRequest) (*llmv1.InvokeToolResponse, error) {
	name := strings.TrimSpace(req.GetToolId())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "tool_id is required")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"InvokeTool rejected: no tenant on incoming metadata: tool=%q session=%q err=%v",
			name, strings.TrimSpace(req.GetSessionId()), err)
		return nil, err
	}
	toolCtx := ctx

	sid := strings.TrimSpace(req.GetSessionId())
	inv := toolexec.Invocation{
		ToolCallID:    "invoke-tool-" + uuid.New().String(),
		Name:          name,
		ArgumentsJSON: req.GetArgumentsJson(),
		SessionID:     sid,
		ReadOnly:      false,
	}

	// Enforcement, not presentation. A session-scoped call is refused when the
	// tool is not in the session's effective set — because the profile disabled
	// it, or because the session is PROFILE_ONLY and never enabled it. The
	// refusal names the tool and the session so an operator can see what was
	// attempted; PermissionDenied distinguishes "blocked here" from the
	// NotFound "no such tool anywhere" below.
	if sid != "" && !s.toolAllowedForSession(ctx, sid, name) {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"InvokeTool refused: tool not in session's effective tool set: tool=%q session=%q scope=%s reason=%s",
			name, sid, sc.String(), "disabled_or_not_permitted_by_tool_policy")
		return nil, status.Errorf(codes.PermissionDenied,
			"tool %q is not available to session %q", name, sid)
	}

	def := lookupToolDefinition(s.Tools, name)
	if def == nil && s.ToolProviderHub != nil {
		// Executor-backed tools live in the hub, scoped to the caller's
		// tenant only — another tenant's executor tool stays unknown here.
		for _, d := range s.ToolProviderHub.ExecutorDefinitions(sc.TenantID) {
			if d.GetName() == name {
				def = d
				break
			}
		}
	}
	if def == nil {
		// Generic refusal: name only the requested tool, never enumerate
		// registered or gated names. Gating is additive — an unregistered
		// tool is simply unknown, indistinguishable from a gated-off one.
		return nil, status.Errorf(codes.NotFound, "unknown tool %q", name)
	}

	inv = s.bindWorkspace(toolCtx, inv)
	// InvokeTool runs outside any chat turn; InvokeToolRequest carries no
	// turn id, so TurnID stays empty.
	inv.Origin = toolseam.OriginInvokeTool

	toolStarted := time.Now()
	var success bool
	defer func() {
		s.RecordToolUsageScoped(ctx, sc, name, ToolUsageSourceInvokeTool, sid, success, time.Since(toolStarted))
	}()
	content, success, errMsg := toolexec.RunWithHandlers(toolCtx, def, inv, s.handlerSet())

	resp := &llmv1.InvokeToolResponse{
		ResultJson: content,
		IsError:    !success,
	}
	if errMsg != "" {
		resp.ErrorMessage = &errMsg
	}
	return resp, nil
}
