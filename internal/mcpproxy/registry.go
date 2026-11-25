/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package mcpproxy

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// DefaultCallTimeoutMs is the default per-call timeout for a proxied MCP tool.
const DefaultCallTimeoutMs = 120_000

// TransportKind is the wire shape of an upstream MCP server.
type TransportKind string

const (
	TransportStdio TransportKind = "stdio"
	TransportHTTP  TransportKind = "http"
	TransportSSE   TransportKind = "sse"
)

// TransportConfig mirrors the McpTransportSchema discriminated union. Env and
// Headers hold secret *refs* (or literals), never resolved values.
type TransportConfig struct {
	Type    TransportKind     `json:"type"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ServerConfig mirrors McpServerConfigSchema from app.mcp-servers.ts.
type ServerConfig struct {
	Enabled               bool              `json:"enabled"`
	DisplayName           string            `json:"displayName,omitempty"`
	AttachToAgents        []string          `json:"attachToAgents,omitempty"`
	DefaultClassification string            `json:"defaultClassification,omitempty"`
	ReadOnlyToolAllowlist []string          `json:"readOnlyToolAllowlist,omitempty"`
	ToolClassifications   map[string]string `json:"toolClassifications,omitempty"`
	Transport             TransportConfig   `json:"transport"`
	CallTimeoutMs         int               `json:"callTimeoutMs,omitempty"`
	// Scopes optionally restricts this provider to named scope ids.
	// Empty means every scope.
	Scopes []string `json:"scopes,omitempty"`
}

func (c ServerConfig) agents() []string {
	if len(c.AttachToAgents) == 0 {
		return []string{"implementation"}
	}
	return c.AttachToAgents
}

func (c ServerConfig) allowsScope(sc serverutil.Scope) bool {
	if len(c.Scopes) == 0 {
		return true
	}
	for _, w := range c.Scopes {
		if w == sc.ScopeID {
			return true
		}
	}
	return false
}

// ProviderStatus is the lifecycle state of one upstream server.
type ProviderStatus string

const (
	StatusConnecting ProviderStatus = "connecting"
	StatusReady      ProviderStatus = "ready"
	StatusError      ProviderStatus = "error"
	StatusDisabled   ProviderStatus = "disabled"
)

// ProviderSnapshot is the status surface a client renders.
type ProviderSnapshot struct {
	ProviderID        string         `json:"providerId"`
	Status            ProviderStatus `json:"status"`
	DisplayName       string         `json:"displayName,omitempty"`
	ToolCount         int            `json:"toolCount"`
	ResourceCount     int            `json:"resourceCount"`
	PromptCount       int            `json:"promptCount"`
	RegisteredToolIDs []string       `json:"registeredToolIds"`
	LastRefreshAt     string         `json:"lastRefreshAt,omitempty"`
	LastError         string         `json:"lastError,omitempty"`
}

// RegisteredTool is one proxied tool as an agent sees it.
type RegisteredTool struct {
	ToolID       string
	ProviderID   string
	UpstreamName string
	Description  string
	InputSchema  map[string]any
	ReadOnly     bool
}

type providerState struct {
	config      ServerConfig
	transport   Transport
	status      ProviderStatus
	tools       []RegisteredTool
	resources   []UpstreamResource
	prompts     []UpstreamPrompt
	refreshedAt time.Time
	lastError   string
}

// TransportFactory builds a transport for a provider, resolving any secret refs
// at that moment. It is called once per connect attempt, never cached.
type TransportFactory func(ctx context.Context, providerID string, cfg ServerConfig) (Transport, error)

// Registry owns llmd's upstream MCP servers.
//
// Gating is additive: when Enabled is false the registry never connects a
// server and never registers a tool, so no proxied tool appears in the tool
// list, in generated tool descriptions, or in validator error text. Nothing is
// built and then pruned.
type Registry struct {
	enabled  bool
	factory  TransportFactory
	reserved map[string]struct{}

	mu        sync.RWMutex
	providers map[string]*providerState
}

// RegistryOptions configures a Registry.
type RegistryOptions struct {
	// Enabled is the MCP feature gate. False means unregistered, not pruned.
	Enabled bool
	// Factory builds transports; defaults to DefaultTransportFactory(nil).
	Factory TransportFactory
	// ReservedToolIDs are built-in tool ids a proxied id may not collide with.
	ReservedToolIDs []string
}

// NewRegistry builds an empty registry.
func NewRegistry(opts RegistryOptions) *Registry {
	reserved := make(map[string]struct{}, len(opts.ReservedToolIDs))
	for _, id := range opts.ReservedToolIDs {
		reserved[id] = struct{}{}
	}
	f := opts.Factory
	if f == nil {
		f = DefaultTransportFactory(nil)
	}
	return &Registry{
		enabled:   opts.Enabled,
		factory:   f,
		reserved:  reserved,
		providers: map[string]*providerState{},
	}
}

// Enabled reports whether the MCP proxy feature is registered at all.
func (r *Registry) Enabled() bool { return r.enabled }

// Refresh reconciles the live providers against the configured set. Disabled
// providers are closed; changed providers are reconnected.
func (r *Registry) Refresh(ctx context.Context, configured map[string]ServerConfig) {
	if !r.enabled {
		// Unregistered: the configured set is not even read into the registry.
		return
	}

	r.mu.Lock()
	for id, st := range r.providers {
		cfg, present := configured[id]
		if !present || !cfg.Enabled {
			if st.transport != nil {
				_ = st.transport.Close()
			}
			delete(r.providers, id)
		}
	}
	r.mu.Unlock()

	ids := make([]string, 0, len(configured))
	for id := range configured {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		cfg := configured[id]
		if !cfg.Enabled {
			continue
		}
		r.connectProvider(ctx, id, cfg)
	}
}

func (r *Registry) connectProvider(ctx context.Context, providerID string, cfg ServerConfig) {
	r.mu.Lock()
	if prev := r.providers[providerID]; prev != nil && prev.transport != nil {
		_ = prev.transport.Close()
	}
	r.providers[providerID] = &providerState{config: cfg, status: StatusConnecting}
	r.mu.Unlock()

	st := &providerState{config: cfg, status: StatusConnecting, refreshedAt: time.Now()}

	transport, err := r.factory(ctx, providerID, cfg)
	if err == nil {
		err = transport.Connect(ctx)
	}
	if err == nil {
		var upstream []UpstreamTool
		upstream, err = transport.ListTools(ctx)
		if err == nil {
			st.resources, _ = transport.ListResources(ctx)
			st.prompts, _ = transport.ListPrompts(ctx)
			st.tools, err = r.registerTools(providerID, cfg, upstream)
		}
	}

	if err != nil {
		if transport != nil {
			_ = transport.Close()
		}
		st.status = StatusError
		st.lastError = err.Error()
		st.tools = nil
	} else {
		st.transport = transport
		st.status = StatusReady
	}

	r.mu.Lock()
	r.providers[providerID] = st
	r.mu.Unlock()
}

func (r *Registry) registerTools(providerID string, cfg ServerConfig, upstream []UpstreamTool) ([]RegisteredTool, error) {
	readOnly := make(map[string]struct{}, len(cfg.ReadOnlyToolAllowlist))
	for _, n := range cfg.ReadOnlyToolAllowlist {
		readOnly[n] = struct{}{}
	}
	out := make([]RegisteredTool, 0, len(upstream))
	seen := map[string]struct{}{}
	for _, t := range upstream {
		id, err := ProxiedToolID(providerID, t.Name)
		if err != nil {
			return nil, err
		}
		if err := AssertProxiedToolIDAvailable(id, r.reserved); err != nil {
			return nil, err
		}
		if _, dup := seen[id]; dup {
			return nil, &ToolIDCollisionError{ToolID: id, ConflictsWith: t.Name}
		}
		seen[id] = struct{}{}
		classification := cfg.DefaultClassification
		if override, ok := cfg.ToolClassifications[t.Name]; ok {
			classification = override
		}
		_, inAllowlist := readOnly[t.Name]
		out = append(out, RegisteredTool{
			ToolID:       id,
			ProviderID:   providerID,
			UpstreamName: t.Name,
			Description:  t.Description,
			InputSchema:  t.InputSchema,
			ReadOnly:     inAllowlist || classification == "query",
		})
	}
	return out, nil
}

// Snapshot reports provider status. Empty when the feature is not registered.
func (r *Registry) Snapshot() []ProviderSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProviderSnapshot, 0, len(r.providers))
	for id, st := range r.providers {
		ids := make([]string, 0, len(st.tools))
		for _, t := range st.tools {
			ids = append(ids, t.ToolID)
		}
		snap := ProviderSnapshot{
			ProviderID:        id,
			Status:            st.status,
			DisplayName:       st.config.DisplayName,
			ToolCount:         len(st.tools),
			ResourceCount:     len(st.resources),
			PromptCount:       len(st.prompts),
			RegisteredToolIDs: ids,
			LastError:         st.lastError,
		}
		if !st.refreshedAt.IsZero() {
			snap.LastRefreshAt = st.refreshedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProviderID < out[j].ProviderID })
	return out
}

// RegisteredTools returns every ready proxied tool.
func (r *Registry) RegisteredTools() []RegisteredTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []RegisteredTool
	for _, st := range r.providers {
		if st.status != StatusReady {
			continue
		}
		out = append(out, st.tools...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ToolID < out[j].ToolID })
	return out
}

// RegisteredToolIDs returns every ready proxied tool id.
func (r *Registry) RegisteredToolIDs() []string {
	tools := r.RegisteredTools()
	ids := make([]string, 0, len(tools))
	for _, t := range tools {
		ids = append(ids, t.ToolID)
	}
	return ids
}

// AllReadyToolIDsForScope is the full_registry tool set: every ready tool
// whose provider is allowed in this scope.
func (r *Registry) AllReadyToolIDsForScope(sc serverutil.Scope) []string {
	return r.toolIDs(func(cfg ServerConfig) bool { return cfg.allowsScope(sc) })
}

// ToolIDsForAgent is the explicit_ids tool set: tools whose provider names this
// agent in attachToAgents and is allowed in this scope.
func (r *Registry) ToolIDsForAgent(agentID string, sc serverutil.Scope) []string {
	aid := strings.TrimSpace(agentID)
	if aid == "" {
		return nil
	}
	return r.toolIDs(func(cfg ServerConfig) bool {
		if !cfg.allowsScope(sc) {
			return false
		}
		for _, a := range cfg.agents() {
			if a == aid {
				return true
			}
		}
		return false
	})
}

func (r *Registry) toolIDs(match func(ServerConfig) bool) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var ids []string
	for _, st := range r.providers {
		if st.status != StatusReady || !st.config.Enabled || !match(st.config) {
			continue
		}
		for _, t := range st.tools {
			ids = append(ids, t.ToolID)
		}
	}
	sort.Strings(ids)
	return ids
}

// BuildToolDescription composes the proxied-tool section of a tool description
// from the registry itself, so a disabled feature contributes no text at all.
func (r *Registry) BuildToolDescription() string {
	tools := r.RegisteredTools()
	if len(tools) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tools))
	for _, t := range tools {
		if t.Description == "" {
			parts = append(parts, t.ToolID)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", t.ToolID, t.Description))
	}
	return "Proxied MCP tools:\n" + strings.Join(parts, "\n")
}

// ValidateToolID refuses an unknown id. The message lists only registered ids,
// so a gated feature cannot be confirmed by making a deliberately wrong call.
func (r *Registry) ValidateToolID(toolID string) error {
	for _, t := range r.RegisteredTools() {
		if t.ToolID == toolID {
			return nil
		}
	}
	return fmt.Errorf("unknown tool %q", toolID)
}

// CallTool dispatches to the upstream server and flattens the result to text.
func (r *Registry) CallTool(ctx context.Context, toolID string, args map[string]any) (string, error) {
	r.mu.RLock()
	var (
		found     bool
		upstream  string
		transport Transport
		providerS ProviderStatus
	)
	for _, st := range r.providers {
		for _, t := range st.tools {
			if t.ToolID != toolID {
				continue
			}
			found, upstream, transport, providerS = true, t.UpstreamName, st.transport, st.status
			break
		}
		if found {
			break
		}
	}
	r.mu.RUnlock()

	if !found {
		return "", r.ValidateToolID(toolID)
	}
	if providerS != StatusReady || transport == nil {
		return "", fmt.Errorf("MCP provider for %q is not ready (%s)", toolID, providerS)
	}
	blocks, err := transport.CallTool(ctx, upstream, args)
	if err != nil {
		return "", err
	}
	return MapContentToText(blocks), nil
}

// ProviderCatalog returns the resources and prompts of one provider. ok is
// false when the provider is not registered — which is also what a gated
// feature looks like, so the caller cannot distinguish "off" from "absent".
func (r *Registry) ProviderCatalog(providerID string) (resources []UpstreamResource, prompts []UpstreamPrompt, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	st := r.providers[providerID]
	if st == nil {
		return nil, nil, false
	}
	return st.resources, st.prompts, true
}

// ProbeResult is the outcome of a connect/list/disconnect test against a config
// that is not (yet) registered.
type ProbeResult struct {
	OK    bool
	Error string
	Tools []UpstreamTool
}

// Probe connects, lists tools and disconnects without registering anything.
// It never mutates the registry, so a failed probe cannot disturb live
// providers. A probe is refused outright when the feature is not registered.
func (r *Registry) Probe(ctx context.Context, providerID string, cfg ServerConfig) ProbeResult {
	if !r.enabled {
		return ProbeResult{OK: false, Error: "MCP proxy is not enabled"}
	}
	transport, err := r.factory(ctx, providerID, cfg)
	if err != nil {
		return ProbeResult{OK: false, Error: err.Error()}
	}
	defer func() { _ = transport.Close() }()
	if err := transport.Connect(ctx); err != nil {
		return ProbeResult{OK: false, Error: err.Error()}
	}
	tools, err := transport.ListTools(ctx)
	if err != nil {
		return ProbeResult{OK: false, Error: err.Error()}
	}
	return ProbeResult{OK: true, Tools: tools}
}

// Close shuts every upstream server down.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, st := range r.providers {
		if st.transport != nil {
			_ = st.transport.Close()
		}
		delete(r.providers, id)
	}
}
