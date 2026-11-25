// Package policyhook is llmd's policy contract: the three questions llmd asks
// before it acts — may this LLM call happen, which tools may this round see,
// may this tool call run — and the answers it accepts back.
//
// It is PUBLIC on purpose. A host embedding llmd (see llmdhost) enforces its
// own policy by implementing Hooks in its own process and passing it as
// Config.PolicyHooks. While this package lived under internal/ that was
// impossible: an embedder could name llmdhost.PolicyHooks but could not write
// a method taking an internal request type, so the only route left was serving
// PolicyHookService over gRPC from a second process — a remote call, a TLS
// decision and a failure mode, all to answer a question being asked inside the
// same binary. Hosting llmd and policing it are the same job and belong on the
// same side of the import boundary.
//
// Two implementations ship here: Funcs for an in-process hook, and the client
// built by NewGRPCClient for a remote PolicyHookService. Guard wraps either
// one with the fail mode, metrics and tracing.
package policyhook

import (
	"context"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// Decision is a hook's allow/deny answer. The zero value is Allow.
type Decision int

const (
	DecisionAllow Decision = iota
	DecisionDeny
)

func (d Decision) String() string {
	if d == DecisionDeny {
		return "deny"
	}
	return "allow"
}

// LLMCallRequest is the input to CheckLLMCall.
type LLMCallRequest struct {
	Scope        *llmv1.PolicyScope
	AgentID      string
	SessionID    string
	TurnID       string
	Round        uint32
	ProviderID   string
	ProviderType string
	Model        string
	Purpose      llmv1.PolicyPurpose
	ReadOnly     bool
	Messages     []*llmv1.Message
}

// FilterToolsRequest is the input to FilterTools.
type FilterToolsRequest struct {
	Scope        *llmv1.PolicyScope
	AgentID      string
	SessionID    string
	TurnID       string
	Round        uint32
	ProviderID   string
	ProviderType string
	Model        string
	Purpose      llmv1.PolicyPurpose
	ReadOnly     bool
	Messages     []*llmv1.Message
	Tools        []*llmv1.ToolDefinition
}

// ToolCallRequest is the input to CheckToolCall.
type ToolCallRequest struct {
	Scope         *llmv1.PolicyScope
	AgentID       string
	SessionID     string
	TurnID        string
	Round         uint32
	ProviderID    string
	ProviderType  string
	Model         string
	Purpose       llmv1.PolicyPurpose
	ReadOnly      bool
	ToolCallID    string
	Name          string
	ArgumentsJSON string
	Tool          *llmv1.ToolDefinition
	Mutation      bool
	Delegated     bool
	AssistantText string
	EntryPath     llmv1.PolicyEntryPath
	Messages      []*llmv1.Message
}

// Verdict is the answer to CheckLLMCall or CheckToolCall.
type Verdict struct {
	Decision              Decision
	Reason                string
	UserMessage           string
	ReplacementToolResult string
	PolicyOverride        *CheckPolicy
}

// FilterResult is the answer to FilterTools.
type FilterResult struct {
	Decision         Decision
	AllowedToolNames []string
	Reason           string
	PolicyOverride   *CheckPolicy
}

// HookPolicies holds one CheckPolicy per hook; nil means "not set".
type HookPolicies struct {
	LLMCall     *CheckPolicy
	FilterTools *CheckPolicy
	ToolCall    *CheckPolicy
}

// Hooks is the policy hook contract.
type Hooks interface {
	GetCheckPolicy(ctx context.Context) (HookPolicies, error)
	CheckLLMCall(ctx context.Context, req LLMCallRequest) (Verdict, error)
	FilterTools(ctx context.Context, req FilterToolsRequest) (FilterResult, error)
	CheckToolCall(ctx context.Context, req ToolCallRequest) (Verdict, error)
}

func allToolNames(tools []*llmv1.ToolDefinition) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.GetName())
	}
	return names
}

// Noop allows everything and never errors.
type Noop struct{}

func (Noop) GetCheckPolicy(context.Context) (HookPolicies, error) { return HookPolicies{}, nil }
func (Noop) CheckLLMCall(context.Context, LLMCallRequest) (Verdict, error) {
	return Verdict{Decision: DecisionAllow}, nil
}
func (Noop) FilterTools(_ context.Context, req FilterToolsRequest) (FilterResult, error) {
	return FilterResult{Decision: DecisionAllow, AllowedToolNames: allToolNames(req.Tools)}, nil
}
func (Noop) CheckToolCall(context.Context, ToolCallRequest) (Verdict, error) {
	return Verdict{Decision: DecisionAllow}, nil
}

// Funcs is an in-process adapter. A nil function allows.
type Funcs struct {
	GetCheckPolicyFn func(context.Context) (HookPolicies, error)
	CheckLLMCallFn   func(context.Context, LLMCallRequest) (Verdict, error)
	FilterToolsFn    func(context.Context, FilterToolsRequest) (FilterResult, error)
	CheckToolCallFn  func(context.Context, ToolCallRequest) (Verdict, error)
}

func (f Funcs) GetCheckPolicy(ctx context.Context) (HookPolicies, error) {
	if f.GetCheckPolicyFn == nil {
		return HookPolicies{}, nil
	}
	return f.GetCheckPolicyFn(ctx)
}

func (f Funcs) CheckLLMCall(ctx context.Context, r LLMCallRequest) (Verdict, error) {
	if f.CheckLLMCallFn == nil {
		return Verdict{}, nil
	}
	return f.CheckLLMCallFn(ctx, r)
}

func (f Funcs) FilterTools(ctx context.Context, r FilterToolsRequest) (FilterResult, error) {
	if f.FilterToolsFn == nil {
		return FilterResult{AllowedToolNames: allToolNames(r.Tools)}, nil
	}
	return f.FilterToolsFn(ctx, r)
}

func (f Funcs) CheckToolCall(ctx context.Context, r ToolCallRequest) (Verdict, error) {
	if f.CheckToolCallFn == nil {
		return Verdict{}, nil
	}
	return f.CheckToolCallFn(ctx, r)
}
