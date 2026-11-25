package policyhook

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/status"
)

// GRPCClient adapts a remote PolicyHookService to Hooks.
type GRPCClient struct {
	client  llmv1.PolicyHookServiceClient
	timeout time.Duration

	mu       sync.Mutex
	policies *HookPolicies
}

var _ Hooks = (*GRPCClient)(nil)

// NewGRPCClient wraps conn. Each call is bounded by timeout (0 = no limit).
func NewGRPCClient(conn grpc.ClientConnInterface, timeout time.Duration) *GRPCClient {
	return &GRPCClient{client: llmv1.NewPolicyHookServiceClient(conn), timeout: timeout}
}

// WatchReconnects starts a goroutine that re-fetches the remote policies each
// time conn returns to READY after having left it. The goroutine ends when conn
// is closed (state SHUTDOWN) or ctx is canceled.
func (c *GRPCClient) WatchReconnects(ctx context.Context, conn *grpc.ClientConn) {
	go func() {
		wasReady := conn.GetState() == connectivity.Ready
		seenReady := wasReady
		for {
			state := conn.GetState()
			switch state {
			case connectivity.Shutdown:
				return
			case connectivity.Ready:
				first := !seenReady
				seenReady = true
				if !wasReady {
					wasReady = true
					if first {
						break
					}
					if err := c.RefreshPolicy(ctx); err != nil {
						// Drop the stale cache so the next call re-fetches.
						c.mu.Lock()
						c.policies = nil
						c.mu.Unlock()
						llmdlog.Stdout("policyhook", llmdlog.LevelWarn, "refresh policy after reconnect: %v", err)
					}
				}
			case connectivity.Idle:
				if wasReady {
					wasReady = false
					conn.Connect()
				}
			default:
				wasReady = false
			}
			if !conn.WaitForStateChange(ctx, state) {
				return
			}
		}
	}()
}

// IsTimeout reports whether err is a deadline expiry from a hook call.
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if s, ok := status.FromError(err); ok && s.Code() == codes.DeadlineExceeded {
		return true
	}
	return false
}

func (c *GRPCClient) callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.timeout)
}

func decisionFromProto(d llmv1.Decision) Decision {
	if d == llmv1.Decision_DECISION_DENY {
		return DecisionDeny
	}
	return DecisionAllow
}

// GetCheckPolicy returns the cached policies, fetching them on first use.
func (c *GRPCClient) GetCheckPolicy(ctx context.Context) (HookPolicies, error) {
	c.mu.Lock()
	cached := c.policies
	c.mu.Unlock()
	if cached != nil {
		return *cached, nil
	}
	if err := c.RefreshPolicy(ctx); err != nil {
		return HookPolicies{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return *c.policies, nil
}

// RefreshPolicy re-fetches the remote policies; call it after a reconnect.
func (c *GRPCClient) RefreshPolicy(ctx context.Context) error {
	cctx, cancel := c.callCtx(ctx)
	defer cancel()
	resp, err := c.client.GetCheckPolicy(cctx, &llmv1.GetCheckPolicyRequest{})
	if err != nil {
		return fmt.Errorf("policy hook GetCheckPolicy: %w", err)
	}
	p := &HookPolicies{
		LLMCall:     policyFromProto(resp.GetLlmCall()),
		FilterTools: policyFromProto(resp.GetFilterTools()),
		ToolCall:    policyFromProto(resp.GetToolCall()),
	}
	c.mu.Lock()
	c.policies = p
	c.mu.Unlock()
	return nil
}

func (c *GRPCClient) CheckLLMCall(ctx context.Context, r LLMCallRequest) (Verdict, error) {
	cctx, cancel := c.callCtx(ctx)
	defer cancel()
	resp, err := c.client.CheckLLMCall(cctx, &llmv1.CheckLLMCallRequest{
		Scope: r.Scope, AgentId: r.AgentID, SessionId: r.SessionID, TurnId: r.TurnID, Round: r.Round,
		ProviderId: r.ProviderID, ProviderType: r.ProviderType, Model: r.Model, Purpose: r.Purpose,
		ReadOnly: r.ReadOnly, Messages: r.Messages,
	})
	if err != nil {
		return Verdict{}, fmt.Errorf("policy hook CheckLLMCall: %w", err)
	}
	return Verdict{
		Decision:       decisionFromProto(resp.GetDecision()),
		Reason:         resp.GetReason(),
		UserMessage:    resp.GetUserMessage(),
		PolicyOverride: policyFromProto(resp.GetPolicyOverride()),
	}, nil
}

func (c *GRPCClient) FilterTools(ctx context.Context, r FilterToolsRequest) (FilterResult, error) {
	cctx, cancel := c.callCtx(ctx)
	defer cancel()
	resp, err := c.client.FilterTools(cctx, &llmv1.FilterToolsRequest{
		Scope: r.Scope, AgentId: r.AgentID, SessionId: r.SessionID, TurnId: r.TurnID, Round: r.Round,
		ProviderId: r.ProviderID, ProviderType: r.ProviderType, Model: r.Model, Purpose: r.Purpose,
		ReadOnly: r.ReadOnly, Messages: r.Messages, Tools: r.Tools,
	})
	if err != nil {
		return FilterResult{}, fmt.Errorf("policy hook FilterTools: %w", err)
	}
	return FilterResult{
		Decision:         decisionFromProto(resp.GetDecision()),
		AllowedToolNames: resp.GetAllowedToolNames(),
		Reason:           resp.GetReason(),
		PolicyOverride:   policyFromProto(resp.GetPolicyOverride()),
	}, nil
}

func (c *GRPCClient) CheckToolCall(ctx context.Context, r ToolCallRequest) (Verdict, error) {
	cctx, cancel := c.callCtx(ctx)
	defer cancel()
	resp, err := c.client.CheckToolCall(cctx, &llmv1.CheckToolCallRequest{
		Scope: r.Scope, AgentId: r.AgentID, SessionId: r.SessionID, TurnId: r.TurnID, Round: r.Round,
		ProviderId: r.ProviderID, ProviderType: r.ProviderType, Model: r.Model, Purpose: r.Purpose,
		ReadOnly: r.ReadOnly, ToolCallId: r.ToolCallID, Name: r.Name, ArgumentsJson: r.ArgumentsJSON,
		Tool: r.Tool, Mutation: r.Mutation, Delegated: r.Delegated, AssistantText: r.AssistantText,
		EntryPath: r.EntryPath, Messages: r.Messages,
	})
	if err != nil {
		return Verdict{}, fmt.Errorf("policy hook CheckToolCall: %w", err)
	}
	return Verdict{
		Decision:              decisionFromProto(resp.GetDecision()),
		Reason:                resp.GetReason(),
		UserMessage:           resp.GetUserMessage(),
		ReplacementToolResult: resp.GetReplacementToolResult(),
		PolicyOverride:        policyFromProto(resp.GetPolicyOverride()),
	}, nil
}
