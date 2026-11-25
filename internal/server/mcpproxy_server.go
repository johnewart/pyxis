/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package server

import (
	"context"
	"encoding/json"

	mcpproxyv1 "github.com/johnewart/pyxis/gen/mcpproxy/v1"
	"github.com/johnewart/pyxis/internal/mcpproxy"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// McpProxyServer exposes llmd's upstream MCP proxy registry.
//
// llmd is authoritative for everything derived from a live upstream server:
// which providers are ready, what tools they expose, what a tool call returns.
// A client pushes the configured set through SetProviders and reads the rest
// back verbatim — it never recomputes a status or re-filters a tool set.
//
// When the MCP feature is not registered, Registry.Enabled() is false: every
// read returns empty and every call falls through to the generic unknown-tool
// error, so a gated feature cannot be confirmed by a deliberately wrong call.
type McpProxyServer struct {
	mcpproxyv1.UnimplementedMcpProxyServiceServer
	Registry *mcpproxy.Registry
}

func (s *McpProxyServer) registry() (*mcpproxy.Registry, error) {
	if s.Registry == nil {
		return nil, status.Error(codes.Unavailable, "MCP proxy registry is not configured")
	}
	return s.Registry, nil
}

// ---------------------------------------------------------------------------
// wire <-> domain
// ---------------------------------------------------------------------------

func transportKindFromWire(k mcpproxyv1.McpTransportKind) mcpproxy.TransportKind {
	switch k {
	case mcpproxyv1.McpTransportKind_MCP_TRANSPORT_KIND_HTTP:
		return mcpproxy.TransportHTTP
	case mcpproxyv1.McpTransportKind_MCP_TRANSPORT_KIND_SSE:
		return mcpproxy.TransportSSE
	default:
		return mcpproxy.TransportStdio
	}
}

func serverConfigFromWire(in *mcpproxyv1.McpServerConfig) mcpproxy.ServerConfig {
	if in == nil {
		return mcpproxy.ServerConfig{}
	}
	cfg := mcpproxy.ServerConfig{
		Enabled:               in.GetEnabled(),
		DisplayName:           in.GetDisplayName(),
		AttachToAgents:        in.GetAttachToAgents(),
		DefaultClassification: in.GetDefaultClassification(),
		ReadOnlyToolAllowlist: in.GetReadOnlyToolAllowlist(),
		ToolClassifications:   in.GetToolClassifications(),
		CallTimeoutMs:         int(in.GetCallTimeoutMs()),
		Scopes:                in.GetScopes(),
	}
	if t := in.GetTransport(); t != nil {
		cfg.Transport = mcpproxy.TransportConfig{
			Type:    transportKindFromWire(t.GetType()),
			Command: t.GetCommand(),
			Args:    t.GetArgs(),
			Env:     t.GetEnv(),
			Cwd:     t.GetCwd(),
			URL:     t.GetUrl(),
			Headers: t.GetHeaders(),
		}
	}
	return cfg
}

func providerStatusToWire(s mcpproxy.ProviderStatus) mcpproxyv1.McpProviderStatus {
	switch s {
	case mcpproxy.StatusConnecting:
		return mcpproxyv1.McpProviderStatus_MCP_PROVIDER_STATUS_CONNECTING
	case mcpproxy.StatusReady:
		return mcpproxyv1.McpProviderStatus_MCP_PROVIDER_STATUS_READY
	case mcpproxy.StatusError:
		return mcpproxyv1.McpProviderStatus_MCP_PROVIDER_STATUS_ERROR
	case mcpproxy.StatusDisabled:
		return mcpproxyv1.McpProviderStatus_MCP_PROVIDER_STATUS_DISABLED
	default:
		return mcpproxyv1.McpProviderStatus_MCP_PROVIDER_STATUS_UNSPECIFIED
	}
}

func snapshotsToWire(snaps []mcpproxy.ProviderSnapshot) []*mcpproxyv1.McpProviderSnapshot {
	out := make([]*mcpproxyv1.McpProviderSnapshot, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, &mcpproxyv1.McpProviderSnapshot{
			ProviderId:        s.ProviderID,
			Status:            providerStatusToWire(s.Status),
			DisplayName:       s.DisplayName,
			ToolCount:         int32(s.ToolCount),
			ResourceCount:     int32(s.ResourceCount),
			PromptCount:       int32(s.PromptCount),
			RegisteredToolIds: s.RegisteredToolIDs,
			LastRefreshAt:     s.LastRefreshAt,
			LastError:         s.LastError,
		})
	}
	return out
}

func proxiedToolToWire(t mcpproxy.RegisteredTool) *mcpproxyv1.ProxiedTool {
	schema := ""
	if t.InputSchema != nil {
		if raw, err := json.Marshal(t.InputSchema); err != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn,
				"mcpproxy: marshal input schema failed (tool advertised without schema): tool=%q provider=%q err=%v",
				t.ToolID, t.ProviderID, err)
		} else {
			schema = string(raw)
		}
	}
	return &mcpproxyv1.ProxiedTool{
		ToolId:          t.ToolID,
		ProviderId:      t.ProviderID,
		UpstreamName:    t.UpstreamName,
		Description:     t.Description,
		InputSchemaJson: schema,
		ReadOnly:        t.ReadOnly,
	}
}

// ---------------------------------------------------------------------------
// handlers
// ---------------------------------------------------------------------------

// SetProviders reconciles the live providers against the client's configured set.
func (s *McpProxyServer) SetProviders(ctx context.Context, req *mcpproxyv1.SetProvidersRequest) (*mcpproxyv1.SetProvidersResponse, error) {
	reg, err := s.registry()
	if err != nil {
		return nil, err
	}
	configured := make(map[string]mcpproxy.ServerConfig, len(req.GetServers()))
	for id, cfg := range req.GetServers() {
		configured[id] = serverConfigFromWire(cfg)
	}
	reg.Refresh(ctx, configured)
	return &mcpproxyv1.SetProvidersResponse{Providers: snapshotsToWire(reg.Snapshot())}, nil
}

func (s *McpProxyServer) ListProviders(_ context.Context, _ *mcpproxyv1.ListProvidersRequest) (*mcpproxyv1.ListProvidersResponse, error) {
	reg, err := s.registry()
	if err != nil {
		return nil, err
	}
	return &mcpproxyv1.ListProvidersResponse{
		Providers: snapshotsToWire(reg.Snapshot()),
		Enabled:   reg.Enabled(),
	}, nil
}

// ListProxiedTools returns the ready tools plus both id sets a client's agent
// composition needs. llmd applies attachToAgents and the workspace policy here
// so the client has nothing left to filter on.
func (s *McpProxyServer) ListProxiedTools(ctx context.Context, req *mcpproxyv1.ListProxiedToolsRequest) (*mcpproxyv1.ListProxiedToolsResponse, error) {
	sc, err := serverutil.ScopeFromContext(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "ListProxiedTools rejected: agent=%q err=%v", req.GetAgentId(), err)
		return nil, err
	}
	reg, err := s.registry()
	if err != nil {
		return nil, err
	}
	tools := reg.RegisteredTools()
	wire := make([]*mcpproxyv1.ProxiedTool, 0, len(tools))
	for _, t := range tools {
		wire = append(wire, proxiedToolToWire(t))
	}
	return &mcpproxyv1.ListProxiedToolsResponse{
		Tools:        wire,
		AgentToolIds: reg.ToolIDsForAgent(req.GetAgentId(), sc),
		ScopeToolIds: reg.AllReadyToolIDsForScope(sc),
	}, nil
}

func (s *McpProxyServer) CallProxiedTool(ctx context.Context, req *mcpproxyv1.CallProxiedToolRequest) (*mcpproxyv1.CallProxiedToolResponse, error) {
	reg, err := s.registry()
	if err != nil {
		return nil, err
	}
	args := map[string]any{}
	if raw := req.GetArgsJson(); raw != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "args_json is not a JSON object: %v", err)
		}
	}
	text, err := reg.CallTool(ctx, req.GetToolId(), args)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &mcpproxyv1.CallProxiedToolResponse{Text: text}, nil
}

func (s *McpProxyServer) GetProviderCatalog(_ context.Context, req *mcpproxyv1.GetProviderCatalogRequest) (*mcpproxyv1.GetProviderCatalogResponse, error) {
	reg, err := s.registry()
	if err != nil {
		return nil, err
	}
	resources, prompts, ok := reg.ProviderCatalog(req.GetProviderId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "MCP provider %q not found", req.GetProviderId())
	}
	res := make([]*mcpproxyv1.McpResource, 0, len(resources))
	for _, r := range resources {
		res = append(res, &mcpproxyv1.McpResource{
			Uri:         r.URI,
			Name:        r.Name,
			Description: r.Description,
			MimeType:    r.MimeType,
		})
	}
	pr := make([]*mcpproxyv1.McpPrompt, 0, len(prompts))
	for _, p := range prompts {
		args := make([]*mcpproxyv1.McpPromptArgument, 0, len(p.Arguments))
		for _, a := range p.Arguments {
			args = append(args, &mcpproxyv1.McpPromptArgument{
				Name:        a.Name,
				Description: a.Description,
				Required:    a.Required,
			})
		}
		pr = append(pr, &mcpproxyv1.McpPrompt{
			Name:        p.Name,
			Description: p.Description,
			Arguments:   args,
		})
	}
	return &mcpproxyv1.GetProviderCatalogResponse{
		ProviderId: req.GetProviderId(),
		Resources:  res,
		Prompts:    pr,
	}, nil
}

func (s *McpProxyServer) ProbeProvider(ctx context.Context, req *mcpproxyv1.ProbeProviderRequest) (*mcpproxyv1.ProbeProviderResponse, error) {
	reg, err := s.registry()
	if err != nil {
		return nil, err
	}
	providerID := req.GetProviderId()
	if providerID == "" {
		providerID = "test"
	}
	result := reg.Probe(ctx, providerID, serverConfigFromWire(req.GetConfig()))
	tools := make([]*mcpproxyv1.ProbedTool, 0, len(result.Tools))
	for _, t := range result.Tools {
		tools = append(tools, &mcpproxyv1.ProbedTool{Name: t.Name, Description: t.Description})
	}
	return &mcpproxyv1.ProbeProviderResponse{
		Ok:        result.OK,
		Error:     result.Error,
		ToolCount: int32(len(result.Tools)),
		Tools:     tools,
	}, nil
}
