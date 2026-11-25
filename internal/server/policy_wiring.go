package server

import (
	"context"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	conversationevents "github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/policyhook"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/toolmerge"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// policyRound carries what executeToolForStream needs to call CheckToolCall
// for one agent-loop round.
type policyRound struct {
	round        uint32
	allowed      map[string]bool // nil means FilterTools removed nothing
	providerID   string
	providerType string
	model        string
	messages     []*llmv1.Message
}

type policyRoundKey struct{}

func withPolicyRound(ctx context.Context, pr *policyRound) context.Context {
	return context.WithValue(ctx, policyRoundKey{}, pr)
}

func policyRoundFrom(ctx context.Context) *policyRound {
	pr, _ := ctx.Value(policyRoundKey{}).(*policyRound)
	return pr
}

// policyGuard wraps the configured hooks; nil hooks mean no-op.
func (s *LlmServer) policyGuard() *policyhook.Guard {
	// A host-built Guard already carries the configured fail mode and metrics.
	if g, ok := s.PolicyHooks.(*policyhook.Guard); ok && g != nil {
		return g
	}
	return policyhook.NewGuard(s.PolicyHooks, policyhook.FailClosed, nil, s.Tracer)
}

func policyScopeOf(sc serverutil.Scope) *llmv1.PolicyScope {
	return &llmv1.PolicyScope{TenantId: sc.TenantID, WorkspaceId: sc.ScopeID}
}

// policyCheckPolicy resolves the effective check policy for a hook.
func (s *LlmServer) policyCheckPolicy(ctx context.Context, pick func(policyhook.HookPolicies) *policyhook.CheckPolicy) *policyhook.CheckPolicy {
	hp, err := s.PolicyHooks.GetCheckPolicy(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "policy: GetCheckPolicy failed, using default: %v", err)
		if s.DefaultCheckPolicy != nil {
			return s.DefaultCheckPolicy
		}
		return policyhook.DefaultCheckPolicy()
	}
	return policyhook.Resolve(s.DefaultCheckPolicy, pick(hp), nil)
}

// policyCheckLLM runs CheckLLMCall when the cadence selects this round. It
// returns the denial reason and true on DENY.
func (s *LlmServer) policyCheckLLM(ctx context.Context, req policyhook.LLMCallRequest) (string, bool) {
	if s.PolicyHooks == nil {
		return "", false
	}
	cp := s.policyCheckPolicy(ctx, func(h policyhook.HookPolicies) *policyhook.CheckPolicy { return h.LLMCall })
	if !cp.ShouldCheck(req.Round) {
		return "", false
	}
	req.Messages = cp.SelectMessages(req.Messages)
	v, _ := s.policyGuard().CheckLLMCall(ctx, req)
	if v.Decision != policyhook.DecisionDeny {
		return "", false
	}
	reason := strings.TrimSpace(v.Reason)
	if reason == "" {
		reason = "denied by policy"
	}
	return reason, true
}

// policyFilterTools returns the tools FilterTools allows plus the allowed name
// set. With no hook configured it returns tools unchanged and a nil set.
// prev is the allowed set from the last round in this turn where the filter
// ran (nil if none). On a round the cadence skips, prev is applied again so
// the hook's decision holds until it runs next.
func (s *LlmServer) policyFilterTools(ctx context.Context, req policyhook.FilterToolsRequest, prev map[string]bool) ([]*llmv1.ToolDefinition, map[string]bool) {
	if s.PolicyHooks == nil || len(req.Tools) == 0 {
		return req.Tools, prev
	}
	cp := s.policyCheckPolicy(ctx, func(h policyhook.HookPolicies) *policyhook.CheckPolicy { return h.FilterTools })
	if !cp.ShouldCheck(req.Round) {
		if prev == nil {
			return req.Tools, nil
		}
		return keepAllowedTools(req.Tools, prev), prev
	}
	all := req.Tools
	req.Messages = cp.SelectMessages(req.Messages)
	res, _ := s.policyGuard().FilterTools(ctx, req)
	allowed := make(map[string]bool, len(res.AllowedToolNames))
	for _, n := range res.AllowedToolNames {
		allowed[n] = true
	}
	if res.Decision == policyhook.DecisionDeny {
		allowed = map[string]bool{}
	}
	return keepAllowedTools(all, allowed), allowed
}

func keepAllowedTools(all []*llmv1.ToolDefinition, allowed map[string]bool) []*llmv1.ToolDefinition {
	kept := make([]*llmv1.ToolDefinition, 0, len(all))
	for _, d := range all {
		if allowed[d.GetName()] {
			kept = append(kept, d)
		}
	}
	return kept
}

// applyCompletePolicy applies CheckLLMCall and FilterTools to a prepared
// Complete/CompleteStream call. It returns a gRPC error on DENY.
func (s *LlmServer) applyCompletePolicy(ctx context.Context, p *preparedComplete) error {
	if s.PolicyHooks == nil {
		return nil
	}
	model := p.opt.GetModel()
	scope := policyScopeOf(p.sc)
	if reason, deny := s.policyCheckLLM(ctx, policyhook.LLMCallRequest{
		Scope: scope, ProviderID: p.providerID, ProviderType: p.provider.String(), Model: model,
		Purpose: llmv1.PolicyPurpose_POLICY_PURPOSE_COMPLETE, Messages: p.messages,
	}); deny {
		return status.Error(codes.PermissionDenied, "policy denied LLM call: "+reason)
	}
	p.tools, _ = s.policyFilterTools(ctx, policyhook.FilterToolsRequest{
		Scope: scope, ProviderID: p.providerID, ProviderType: p.provider.String(), Model: model,
		Purpose: llmv1.PolicyPurpose_POLICY_PURPOSE_COMPLETE, Messages: p.messages, Tools: p.tools,
	}, nil)
	return nil
}

// policyToolDenial runs the tool-call policy for the agent loop. It returns
// the failed tool result text and true when the call must not execute.
func (s *LlmServer) policyToolDenial(ctx context.Context, aid, sid, turnID string, def *llmv1.ToolDefinition, tc *llmv1.ToolCall, readOnly bool) (string, bool) {
	if s.PolicyHooks == nil {
		return "", false
	}
	pr := policyRoundFrom(ctx)
	if pr == nil {
		pr = &policyRound{}
	}
	name := strings.TrimSpace(tc.GetName())
	if pr.allowed != nil && !pr.allowed[name] {
		return "tool " + name + " is not permitted by policy", true
	}
	cp := s.policyCheckPolicy(ctx, func(h policyhook.HookPolicies) *policyhook.CheckPolicy { return h.ToolCall })
	if !cp.ShouldCheck(pr.round) {
		return "", false
	}
	v, _ := s.policyGuard().CheckToolCall(ctx, policyhook.ToolCallRequest{
		Scope: policyScopeOf(s.scopeOrZero(ctx)), AgentID: aid, SessionID: sid, TurnID: turnID, Round: pr.round,
		ProviderID: pr.providerID, ProviderType: pr.providerType, Model: pr.model,
		Purpose: llmv1.PolicyPurpose_POLICY_PURPOSE_CHAT_STREAM, ReadOnly: readOnly,
		ToolCallID: tc.GetId(), Name: name, ArgumentsJSON: tc.GetArgumentsJson(), Tool: def,
		Mutation: def != nil && toolmerge.IsMutationTool(def), Delegated: def != nil && def.GetDelegateToClient() != nil,
		EntryPath: llmv1.PolicyEntryPath_POLICY_ENTRY_PATH_AGENT_LOOP,
		Messages:  cp.SelectMessages(pr.messages),
	})
	if v.Decision != policyhook.DecisionDeny {
		return "", false
	}
	if r := strings.TrimSpace(v.ReplacementToolResult); r != "" {
		return r, true
	}
	if r := strings.TrimSpace(v.Reason); r != "" {
		return r, true
	}
	return "tool call denied by policy", true
}

// emitDeniedToolFrames sends ToolExecutionStarted and a failed
// ToolExecutionFinished for a tool call that policy denied.
func emitDeniedToolFrames(turnID string, tc *llmv1.ToolCall, msg string, send func(*llmv1.ConversationStreamFrame) error) error {
	if send == nil {
		return nil
	}
	if err := send(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_ToolExecutionStarted{ToolExecutionStarted: toolExecutionStarted(turnID, tc)},
	}); err != nil {
		return err
	}
	fin := &llmv1.EvToolExecutionFinished{
		TurnId: turnID, ToolCallId: tc.GetId(), Name: tc.GetName(), Success: false,
		ErrorMessage: proto.String(msg),
	}
	return send(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_ToolExecutionFinished{ToolExecutionFinished: fin},
	})
}
