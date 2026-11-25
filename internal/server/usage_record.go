package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/grpcauth"
	"github.com/johnewart/pyxis/internal/llm/llmerr"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// principalMetadataKey is the gRPC metadata key naming the caller (user or
// service) on whose behalf a request runs. Optional; empty when absent.
const principalMetadataKey = "llmd-principal"

const (
	usageOutcomeOK       = "ok"
	usageOutcomeCanceled = "canceled"
)

// usageWriteTimeout bounds the usage insert, which runs on a context detached
// from the (possibly canceled) request.
const usageWriteTimeout = 5 * time.Second

// principalFromContext returns the caller identity from incoming metadata.
func principalFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if v := md.Get(principalMetadataKey); len(v) > 0 {
		return strings.TrimSpace(v[0])
	}
	return ""
}

// usageCall describes one provider call to record.
type usageCall struct {
	rpc          string
	providerID   string
	providerType llmv1.LlmProviderType
	model        string
	started      time.Time
	// reply is the completed reply (may be nil); usage is used when set,
	// otherwise the reply's usage.
	reply *llmv1.AssistantReply
	usage *llmv1.TokenUsage
	// err is the provider error, if the call failed.
	err error
}

// usageOutcome maps a call result to the outcome string stored on the row:
// "ok", "canceled", or the llmerr kind name (e.g. LLM_ERROR_KIND_RATE_LIMITED).
func usageOutcome(ctx context.Context, err error, pt llmv1.LlmProviderType) string {
	if err == nil {
		if ctx.Err() != nil {
			return usageOutcomeCanceled
		}
		return usageOutcomeOK
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return usageOutcomeCanceled
	}
	if c, ok := llmerr.FromStatus(err); ok {
		return c.Kind.String()
	}
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return usageOutcomeCanceled
	}
	return llmerr.Classify(err, pt).Kind.String()
}

// recordUsage writes one usage row for a provider call. A write failure is
// logged and never changes the RPC result. No-op when usage persistence is off.
func (s *LlmServer) recordUsage(ctx context.Context, sc serverutil.Scope, c usageCall) {
	if s.Usage == nil {
		return
	}
	u := c.usage
	if u == nil {
		u = c.reply.GetUsage()
	}
	row := store.UsageRecordRow{
		TenantID:         sc.TenantID,
		ScopeID:          sc.ScopeID,
		Principal:        principalFromContext(ctx),
		ClientID:         grpcauth.ClientID(ctx),
		ProviderID:       c.providerID,
		ProviderType:     c.providerType.String(),
		Model:            c.model,
		ServedBy:         c.reply.GetServedBy(),
		RPC:              c.rpc,
		Outcome:          usageOutcome(ctx, c.err, c.providerType),
		CreatedAt:        time.Now().UTC(),
		InputTokens:      u.GetInputTokens(),
		OutputTokens:     u.GetOutputTokens(),
		CacheReadTokens:  u.GetCacheReadTokens(),
		CacheWriteTokens: u.GetCacheWriteTokens(),
		ReasoningTokens:  u.GetReasoningTokens(),
		CostUSD:          u.GetTotalCostUsd(),
	}
	if !c.started.IsZero() {
		row.DurationMs = time.Since(c.started).Milliseconds()
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), usageWriteTimeout)
	defer cancel()
	if err := s.Usage.RecordUsage(wctx, row); err != nil {
		principal := ""
		if sc.PrincipalID != "" {
			principal = fmt.Sprintf(" principal=%q", sc.PrincipalID)
		}
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"%s: record usage failed: tenant=%q scope=%q%s provider=%q model=%q outcome=%q err=%v",
			c.rpc, sc.TenantID, sc.ScopeID, principal, c.providerID, c.model, row.Outcome, err)
	}
}

// QueryToolUsage returns per-tool call/error counts and min/median/mean/max
// duration over a time window. Tenant/scope enforcement matches QueryUsage.
func (s *LlmServer) QueryToolUsage(ctx context.Context, req *llmv1.QueryToolUsageRequest) (*llmv1.QueryToolUsageResponse, error) {
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if s.ToolUsage == nil {
		return nil, status.Error(codes.FailedPrecondition, "tool usage recording is not configured")
	}
	if t := strings.TrimSpace(req.GetTenantId()); t != "" && t != sc.TenantID {
		return nil, status.Error(codes.PermissionDenied, "tenant_id does not match the caller's tenant")
	}
	scopeID := strings.TrimSpace(req.GetScopeId())
	if sc.ScopeID != "" {
		if scopeID != "" && scopeID != sc.ScopeID {
			return nil, status.Error(codes.PermissionDenied, "scope_id does not match the caller's scope")
		}
		scopeID = sc.ScopeID
	}
	f := store.ToolUsageFilter{TenantID: sc.TenantID, ScopeID: scopeID}
	if req.GetSinceUnix() != 0 {
		t := time.Unix(req.GetSinceUnix(), 0).UTC()
		f.Since = &t
	}
	if req.GetUntilUnix() != 0 {
		t := time.Unix(req.GetUntilUnix(), 0).UTC()
		f.Until = &t
	}
	stats, err := s.ToolUsage.AggregateByTool(ctx, f)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "QueryToolUsage: query failed: tenant=%q err=%v", sc.TenantID, err)
		return nil, status.Error(codes.Internal, "query tool usage failed")
	}
	resp := &llmv1.QueryToolUsageResponse{}
	for _, st := range stats {
		resp.Stats = append(resp.Stats, &llmv1.ToolUsageStat{
			ToolName: st.ToolName, Calls: st.Calls, Errors: st.Errors,
			MinMs: st.MinMs, MedianMs: st.MedianMs, MeanMs: st.MeanMs, MaxMs: st.MaxMs,
		})
	}
	return resp, nil
}

// QueryUsage returns recorded usage rows and/or per-(tenant, scope, model)
// totals, per group_by. The caller's tenant is always applied as a filter; a
// request naming a different tenant or scope is refused.
func (s *LlmServer) QueryUsage(ctx context.Context, req *llmv1.QueryUsageRequest) (*llmv1.QueryUsageResponse, error) {
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if s.Usage == nil {
		return nil, status.Error(codes.FailedPrecondition, "usage recording is not configured")
	}
	if t := strings.TrimSpace(req.GetTenantId()); t != "" && t != sc.TenantID {
		return nil, status.Error(codes.PermissionDenied, "tenant_id does not match the caller's tenant")
	}
	scopeID := strings.TrimSpace(req.GetScopeId())
	if sc.ScopeID != "" {
		if scopeID != "" && scopeID != sc.ScopeID {
			return nil, status.Error(codes.PermissionDenied, "scope_id does not match the caller's scope")
		}
		scopeID = sc.ScopeID
	}
	f := store.UsageFilter{TenantID: sc.TenantID, ScopeID: scopeID, Model: strings.TrimSpace(req.GetModel())}
	if req.SinceUnix != nil {
		t := time.Unix(req.GetSinceUnix(), 0).UTC()
		f.Since = &t
	}
	if req.UntilUnix != nil {
		t := time.Unix(req.GetUntilUnix(), 0).UTC()
		f.Until = &t
	}
	rows, totals, err := s.Usage.QueryUsage(ctx, f)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "QueryUsage: query failed: tenant=%q err=%v", sc.TenantID, err)
		return nil, status.Error(codes.Internal, "query usage failed")
	}
	resp := &llmv1.QueryUsageResponse{}
	gb := req.GetGroupBy()
	if gb != llmv1.UsageGroupBy_USAGE_GROUP_BY_AGGREGATE {
		for _, r := range rows {
			resp.Rows = append(resp.Rows, &llmv1.UsageRow{
				TenantId:         r.TenantID,
				ScopeId:          r.ScopeID,
				Model:            r.Model,
				RecordedUnix:     r.CreatedAt.Unix(),
				InputTokens:      r.InputTokens,
				OutputTokens:     r.OutputTokens,
				CacheReadTokens:  r.CacheReadTokens,
				CacheWriteTokens: r.CacheWriteTokens,
				ReasoningTokens:  r.ReasoningTokens,
				CostUsd:          r.CostUSD,
			})
		}
	}
	if gb != llmv1.UsageGroupBy_USAGE_GROUP_BY_ROWS {
		for _, t := range totals {
			resp.Totals = append(resp.Totals, &llmv1.UsageTotal{
				TenantId:         t.TenantID,
				ScopeId:          t.ScopeID,
				Model:            t.Model,
				InputTokens:      t.InputTokens,
				OutputTokens:     t.OutputTokens,
				CacheReadTokens:  t.CacheReadTokens,
				CacheWriteTokens: t.CacheWriteTokens,
				ReasoningTokens:  t.ReasoningTokens,
				CostUsd:          t.CostUSD,
				CallCount:        t.Calls,
			})
		}
	}
	return resp, nil
}
