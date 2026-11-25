package server

import (
	"context"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/toolmerge"
	"github.com/johnewart/pyxis/llmdlog"
)

// tenantTools is the set of tools that EXIST for a tenant, independent of any
// session: the host's registered tools plus the tenant's executor tools
// (owned or ownerless). It is the single source for ListTools, agent-profile
// tool_mapping validation and profile materialization, so a tool that
// ListTools reports is never refused by a profile and vice versa.
//
// Returned definitions are shared, not cloned; callers that hand them out or
// mutate them must clone (RedactSecretsForList and BuildEnsureSessionRequest
// both do).
func (s *LlmServer) tenantTools(tenantID string) []*llmv1.ToolDefinition {
	tools := append([]*llmv1.ToolDefinition(nil), s.registeredTools()...)
	if t := strings.TrimSpace(tenantID); t != "" && s.ToolProviderHub != nil {
		tools = append(tools, s.ToolProviderHub.ExecutorDefinitions(t)...)
	}
	return tools
}

// ListTools returns every tool that exists for the caller's tenant (see
// tenantTools). It is session-independent: no disabled-tool, profile or
// session-tool filtering is applied. Secrets are redacted as for every other
// listing.
func (s *LlmServer) ListTools(ctx context.Context, _ *llmv1.ListToolsRequest) (*llmv1.ListToolsResponse, error) {
	sc, err := s.scope(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "ListTools refused: no tenant scope: %v", err)
		return nil, err
	}
	return &llmv1.ListToolsResponse{Tools: toolmerge.RedactSecretsForList(s.tenantTools(sc.TenantID))}, nil
}
