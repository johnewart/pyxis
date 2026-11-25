/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package server

import (
	"context"
	"fmt"
	"testing"

	mcpproxyv1 "github.com/johnewart/pyxis/gen/mcpproxy/v1"
	"github.com/johnewart/pyxis/internal/mcpproxy"
)

// fakeProxyTransport is an upstream MCP server that never leaves the process.
type fakeProxyTransport struct {
	tools      []mcpproxy.UpstreamTool
	resources  []mcpproxy.UpstreamResource
	prompts    []mcpproxy.UpstreamPrompt
	connectErr error
	lastName   string
	lastArgs   map[string]any
}

func (f *fakeProxyTransport) Connect(context.Context) error { return f.connectErr }

func (f *fakeProxyTransport) ListTools(context.Context) ([]mcpproxy.UpstreamTool, error) {
	return f.tools, nil
}

func (f *fakeProxyTransport) ListResources(context.Context) ([]mcpproxy.UpstreamResource, error) {
	return f.resources, nil
}

func (f *fakeProxyTransport) ListPrompts(context.Context) ([]mcpproxy.UpstreamPrompt, error) {
	return f.prompts, nil
}

func (f *fakeProxyTransport) CallTool(_ context.Context, name string, args map[string]any) ([]mcpproxy.ContentBlock, error) {
	f.lastName, f.lastArgs = name, args
	return []mcpproxy.ContentBlock{{Type: "text", Text: "ok:" + name}}, nil
}

func (f *fakeProxyTransport) Close() error { return nil }

func newProxyServer(t *testing.T, enabled bool, fakes map[string]*fakeProxyTransport) *McpProxyServer {
	t.Helper()
	reg := mcpproxy.NewRegistry(mcpproxy.RegistryOptions{
		Enabled: enabled,
		Factory: func(_ context.Context, providerID string, _ mcpproxy.ServerConfig) (mcpproxy.Transport, error) {
			f, ok := fakes[providerID]
			if !ok {
				return nil, fmt.Errorf("no fake transport for %q", providerID)
			}
			return f, nil
		},
	})
	t.Cleanup(reg.Close)
	return &McpProxyServer{Registry: reg}
}

func stdioConfig(agents ...string) *mcpproxyv1.McpServerConfig {
	return &mcpproxyv1.McpServerConfig{
		Enabled:        true,
		DisplayName:    "Fake",
		AttachToAgents: agents,
		Transport: &mcpproxyv1.McpTransportConfig{
			Type:    mcpproxyv1.McpTransportKind_MCP_TRANSPORT_KIND_STDIO,
			Command: "fake",
		},
	}
}

// TestMcpProxySetProvidersThenListToolsAndCall is the round trip a client makes:
// push the configured set, read back status and tools, then dispatch a call.
func TestMcpProxySetProvidersThenListToolsAndCall(t *testing.T) {
	fake := &fakeProxyTransport{
		tools: []mcpproxy.UpstreamTool{
			{Name: "search-issues", Description: "Find issues", InputSchema: map[string]any{"type": "object"}},
			{Name: "create_issue", Description: "Create an issue"},
		},
		resources: []mcpproxy.UpstreamResource{{URI: "res://one", Name: "One"}},
		prompts:   []mcpproxy.UpstreamPrompt{{Name: "p1"}},
	}
	srv := newProxyServer(t, true, map[string]*fakeProxyTransport{"linear": fake})
	ctx := context.Background()

	setResp, err := srv.SetProviders(ctx, &mcpproxyv1.SetProvidersRequest{
		Servers: map[string]*mcpproxyv1.McpServerConfig{"linear": stdioConfig("implementation")},
	})
	if err != nil {
		t.Fatalf("SetProviders: %v", err)
	}
	if len(setResp.GetProviders()) != 1 {
		t.Fatalf("want 1 provider snapshot, got %d", len(setResp.GetProviders()))
	}
	snap := setResp.GetProviders()[0]
	if snap.GetStatus() != mcpproxyv1.McpProviderStatus_MCP_PROVIDER_STATUS_READY {
		t.Fatalf("provider not ready: %v (%s)", snap.GetStatus(), snap.GetLastError())
	}
	if snap.GetToolCount() != 2 || snap.GetResourceCount() != 1 || snap.GetPromptCount() != 1 {
		t.Fatalf("unexpected counts: tools=%d resources=%d prompts=%d",
			snap.GetToolCount(), snap.GetResourceCount(), snap.GetPromptCount())
	}

	listResp, err := srv.ListProviders(ctx, &mcpproxyv1.ListProvidersRequest{})
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if !listResp.GetEnabled() {
		t.Fatal("ListProviders reported the feature disabled")
	}

	toolsResp, err := srv.ListProxiedTools(wsScope("ws-1"), &mcpproxyv1.ListProxiedToolsRequest{
		AgentId: "implementation",
	})
	if err != nil {
		t.Fatalf("ListProxiedTools: %v", err)
	}
	if len(toolsResp.GetTools()) != 2 {
		t.Fatalf("want 2 proxied tools, got %d", len(toolsResp.GetTools()))
	}
	// The id is composed by llmd and must match the client's saved ids: the
	// non-identifier char in "search-issues" normalizes to an underscore.
	var found *mcpproxyv1.ProxiedTool
	for _, tool := range toolsResp.GetTools() {
		if tool.GetToolId() == "linear_search_issues" {
			found = tool
		}
	}
	if found == nil {
		t.Fatalf("missing linear_search_issues; got %v", toolsResp.GetTools())
	}
	if found.GetInputSchemaJson() != `{"type":"object"}` {
		t.Fatalf("input schema not passed through: %q", found.GetInputSchemaJson())
	}
	if len(toolsResp.GetAgentToolIds()) != 2 {
		t.Fatalf("want 2 agent tool ids, got %v", toolsResp.GetAgentToolIds())
	}
	if len(toolsResp.GetScopeToolIds()) != 2 {
		t.Fatalf("want 2 scope tool ids, got %v", toolsResp.GetScopeToolIds())
	}

	callResp, err := srv.CallProxiedTool(ctx, &mcpproxyv1.CallProxiedToolRequest{
		ToolId:   "linear_search_issues",
		ArgsJson: `{"query":"bug"}`,
	})
	if err != nil {
		t.Fatalf("CallProxiedTool: %v", err)
	}
	if callResp.GetText() != "ok:search-issues" {
		t.Fatalf("unexpected call text: %q", callResp.GetText())
	}
	if fake.lastArgs["query"] != "bug" {
		t.Fatalf("args did not reach upstream: %v", fake.lastArgs)
	}

	catalog, err := srv.GetProviderCatalog(ctx, &mcpproxyv1.GetProviderCatalogRequest{ProviderId: "linear"})
	if err != nil {
		t.Fatalf("GetProviderCatalog: %v", err)
	}
	if len(catalog.GetResources()) != 1 || catalog.GetResources()[0].GetUri() != "res://one" {
		t.Fatalf("unexpected catalog resources: %v", catalog.GetResources())
	}
}

// TestMcpProxyAgentScopingUsesAttachToAgents pins that llmd — not the client —
// applies the attachToAgents filter, so a client caller has nothing to
// re-derive.
func TestMcpProxyAgentScopingUsesAttachToAgents(t *testing.T) {
	fake := &fakeProxyTransport{tools: []mcpproxy.UpstreamTool{{Name: "only_tool"}}}
	srv := newProxyServer(t, true, map[string]*fakeProxyTransport{"linear": fake})
	ctx := context.Background()

	if _, err := srv.SetProviders(ctx, &mcpproxyv1.SetProvidersRequest{
		Servers: map[string]*mcpproxyv1.McpServerConfig{"linear": stdioConfig("planning")},
	}); err != nil {
		t.Fatalf("SetProviders: %v", err)
	}

	resp, err := srv.ListProxiedTools(wsScope("ws-1"), &mcpproxyv1.ListProxiedToolsRequest{
		AgentId: "implementation",
	})
	if err != nil {
		t.Fatalf("ListProxiedTools: %v", err)
	}
	if len(resp.GetAgentToolIds()) != 0 {
		t.Fatalf("implementation must get no tools from a planning-only provider, got %v", resp.GetAgentToolIds())
	}
	// full_registry agents still see it: scope allowlist only.
	if len(resp.GetScopeToolIds()) != 1 {
		t.Fatalf("want 1 scope tool id, got %v", resp.GetScopeToolIds())
	}
}

// TestMcpProxyDisabledFeatureRegistersNothing pins the additive gate: with the
// feature off nothing is connected, nothing is listed, and a call fails with the
// generic unknown-tool error rather than naming the gated provider.
func TestMcpProxyDisabledFeatureRegistersNothing(t *testing.T) {
	fake := &fakeProxyTransport{tools: []mcpproxy.UpstreamTool{{Name: "only_tool"}}}
	srv := newProxyServer(t, false, map[string]*fakeProxyTransport{"linear": fake})
	ctx := context.Background()

	setResp, err := srv.SetProviders(ctx, &mcpproxyv1.SetProvidersRequest{
		Servers: map[string]*mcpproxyv1.McpServerConfig{"linear": stdioConfig("implementation")},
	})
	if err != nil {
		t.Fatalf("SetProviders: %v", err)
	}
	if len(setResp.GetProviders()) != 0 {
		t.Fatalf("disabled feature must register no provider, got %v", setResp.GetProviders())
	}

	listResp, err := srv.ListProviders(ctx, &mcpproxyv1.ListProvidersRequest{})
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if listResp.GetEnabled() || len(listResp.GetProviders()) != 0 {
		t.Fatalf("disabled feature leaked: enabled=%v providers=%v", listResp.GetEnabled(), listResp.GetProviders())
	}

	toolsResp, err := srv.ListProxiedTools(wsScope("ws-1"), &mcpproxyv1.ListProxiedToolsRequest{
		AgentId: "implementation",
	})
	if err != nil {
		t.Fatalf("ListProxiedTools: %v", err)
	}
	if len(toolsResp.GetTools()) != 0 || len(toolsResp.GetAgentToolIds()) != 0 {
		t.Fatalf("disabled feature exposed tools: %v", toolsResp)
	}

	if _, err := srv.CallProxiedTool(ctx, &mcpproxyv1.CallProxiedToolRequest{
		ToolId: "linear_only_tool",
	}); err == nil {
		t.Fatal("expected a call against a gated tool to fail")
	}
}

// TestMcpProxyProbeProviderDoesNotRegister pins that a probe is a throwaway
// connection: a successful probe leaves the registry untouched.
func TestMcpProxyProbeProviderDoesNotRegister(t *testing.T) {
	fake := &fakeProxyTransport{tools: []mcpproxy.UpstreamTool{{Name: "a"}, {Name: "b"}}}
	srv := newProxyServer(t, true, map[string]*fakeProxyTransport{"candidate": fake})
	ctx := context.Background()

	resp, err := srv.ProbeProvider(ctx, &mcpproxyv1.ProbeProviderRequest{
		ProviderId: "candidate", Config: stdioConfig("implementation"),
	})
	if err != nil {
		t.Fatalf("ProbeProvider: %v", err)
	}
	if !resp.GetOk() || resp.GetToolCount() != 2 {
		t.Fatalf("unexpected probe result: ok=%v count=%d err=%q", resp.GetOk(), resp.GetToolCount(), resp.GetError())
	}

	listResp, err := srv.ListProviders(ctx, &mcpproxyv1.ListProvidersRequest{})
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(listResp.GetProviders()) != 0 {
		t.Fatalf("probe registered a provider: %v", listResp.GetProviders())
	}
}
