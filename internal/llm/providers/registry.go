package providers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	anthropicllm "github.com/johnewart/pyxis/internal/llm/anthropic"
	"github.com/johnewart/pyxis/internal/llm/backend"
	bedrockllm "github.com/johnewart/pyxis/internal/llm/bedrock"
	claudecodeclillm "github.com/johnewart/pyxis/internal/llm/claudecode"
	cursorclillm "github.com/johnewart/pyxis/internal/llm/cursorcli"
	geminillm "github.com/johnewart/pyxis/internal/llm/gemini"
	ollamallm "github.com/johnewart/pyxis/internal/llm/ollama"
	openaillm "github.com/johnewart/pyxis/internal/llm/openai"
	openrouterllm "github.com/johnewart/pyxis/internal/llm/openrouter"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/store"
)

// Registry holds named LLM backends loaded at startup (tests only; production wires none — see
// Composite's doc comment).
type Registry struct {
	defaultID string
	byID      map[string]*Entry
	order     []string
}

// BuildEntryFromProviderConfig builds an Entry directly from typed, per-kind database columns
// (store.LlmProviderConfig — see its doc comment) — there is no document to parse: the columns
// already are the fields.
func BuildEntryFromProviderConfig(cfg *store.LlmProviderConfig) (*Entry, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil provider config")
	}
	id := strings.TrimSpace(cfg.ProviderID)
	if id == "" {
		return nil, fmt.Errorf("provider id is required")
	}
	// cfg.Kind IS the enum -- the repo resolved it once when it read the row, so
	// there is nothing to parse (and no name to get wrong) here.
	if !cfg.Kind.Valid() {
		return nil, fmt.Errorf("provider %q has no kind set", id)
	}
	return buildEntry(id, cfg.Kind, cfg)
}

func buildEntry(id string, kind providerkind.Kind, p *store.LlmProviderConfig) (*Entry, error) {
	switch kind {
	case providerkind.Anthropic:
		if p.Anthropic == nil {
			return nil, fmt.Errorf("missing anthropic block")
		}
		if p.OpenAI != nil || p.Ollama != nil || p.CursorCLI != nil || p.ClaudeCode != nil || p.Bedrock != nil {
			return nil, fmt.Errorf("anthropic provider must only include anthropic settings")
		}
		key := strings.TrimSpace(p.Anthropic.APIKey)
		if key == "" {
			return nil, fmt.Errorf("anthropic.api_key is required")
		}
		model := strings.TrimSpace(p.Anthropic.DefaultModel)
		if model == "" {
			model = string(anthropic.ModelClaudeHaiku4_5)
		}
		maxTok := p.Anthropic.MaxOutputTokens
		base := strings.TrimSpace(p.Anthropic.BaseURL)
		be := anthropicllm.New(key, base, anthropic.Model(model), maxTok)
		return &Entry{
			ID: id, Kind: kind, Backend: be, DefaultModel: model,
			anthropicBase: base, anthropicKey: key,
		}, nil

	case providerkind.OpenAI:
		if p.OpenAI == nil {
			return nil, fmt.Errorf("missing openai block")
		}
		if p.Anthropic != nil || p.Ollama != nil || p.CursorCLI != nil || p.ClaudeCode != nil || p.Bedrock != nil {
			return nil, fmt.Errorf("openai provider must only include openai settings")
		}
		key := strings.TrimSpace(p.OpenAI.APIKey)
		base := strings.TrimSpace(p.OpenAI.BaseURL)
		model := strings.TrimSpace(p.OpenAI.DefaultModel)
		maxTok := p.OpenAI.MaxTokens
		be := openaillm.New(key, base, model, maxTok)
		return &Entry{
			ID: id, Kind: kind, Backend: be, DefaultModel: model,
			openAIBase: base, openAIKey: key,
		}, nil

	case providerkind.Ollama:
		if p.Ollama == nil {
			return nil, fmt.Errorf("missing ollama block")
		}
		if p.Anthropic != nil || p.OpenAI != nil || p.CursorCLI != nil || p.ClaudeCode != nil || p.Bedrock != nil {
			return nil, fmt.Errorf("ollama provider must only include ollama settings")
		}
		base := strings.TrimSpace(p.Ollama.BaseURL)
		model := strings.TrimSpace(p.Ollama.DefaultModel)
		maxTok := p.Ollama.MaxTokens
		be := ollamallm.New(base, model, maxTok)
		return &Entry{
			ID: id, Kind: kind, Backend: be, DefaultModel: model,
			ollamaBase: base,
		}, nil

	case providerkind.CursorCLI:
		if p.CursorCLI == nil {
			return nil, fmt.Errorf("missing cursor_cli block")
		}
		if p.Anthropic != nil || p.OpenAI != nil || p.Ollama != nil || p.ClaudeCode != nil || p.Bedrock != nil {
			return nil, fmt.Errorf("cursor_cli provider must only include cursor_cli settings")
		}
		bin := strings.TrimSpace(p.CursorCLI.Binary)
		if bin == "" {
			bin = strings.TrimSpace(os.Getenv("CURSOR_AGENT_PATH"))
		}
		if bin == "" {
			bin = "agent"
		}
		cwd := strings.TrimSpace(p.CursorCLI.Cwd)
		wsFlag := strings.TrimSpace(p.CursorCLI.Workspace)
		key := strings.TrimSpace(p.CursorCLI.APIKey)
		model := strings.TrimSpace(p.CursorCLI.DefaultModel)
		if model == "" {
			model = "auto"
		}
		mode := strings.TrimSpace(p.CursorCLI.Mode)
		trust := true
		if p.CursorCLI.Trust != nil {
			trust = *p.CursorCLI.Trust
		}
		approveMcps := true
		if p.CursorCLI.ApproveMcps != nil {
			approveMcps = *p.CursorCLI.ApproveMcps
		}
		force := true
		if p.CursorCLI.Force != nil {
			force = *p.CursorCLI.Force
		}
		maxTok := p.CursorCLI.MaxOutputTokens
		sess := strings.TrimSpace(p.CursorCLI.SessionID)
		be := cursorclillm.New(cursorclillm.Config{
			Binary:        bin,
			Cwd:           cwd,
			WorkspaceFlag: wsFlag,
			APIKey:        key,
			DefaultModel:  model,
			Mode:          mode,
			Trust:         trust,
			ApproveMcps:   approveMcps,
			Force:         force,
			SessionID:     sess,
			DefaultMaxTok: maxTok,
		})
		return &Entry{
			ID: id, Kind: kind, Backend: be, DefaultModel: model,
			cursorBinary: bin, cursorCwd: cwd, cursorAPIKey: key,
			cursorWorkspace: wsFlag,
		}, nil

	case providerkind.ClaudeCodeV2:
		yc := p.ClaudeCode
		if yc == nil {
			return nil, fmt.Errorf("missing claude_code_v2 block")
		}
		if p.Anthropic != nil || p.OpenAI != nil || p.Ollama != nil || p.CursorCLI != nil || p.Bedrock != nil {
			return nil, fmt.Errorf("claude_code_v2 provider must only include claude_code_v2 settings")
		}
		bin := strings.TrimSpace(yc.Binary)
		if bin == "" {
			bin = strings.TrimSpace(os.Getenv("CLAUDE_CLI_PATH"))
		}
		if bin == "" {
			bin = "claude"
		}
		cwd := strings.TrimSpace(yc.Cwd)
		model := strings.TrimSpace(yc.DefaultModel)
		be := claudecodeclillm.NewV2(claudecodeclillm.Config{
			Binary:               bin,
			Cwd:                  cwd,
			Agent:                strings.TrimSpace(yc.Agent),
			DefaultModel:         model,
			SystemPrompt:         strings.TrimSpace(yc.SystemPrompt),
			AppendSystemPrompt:   strings.TrimSpace(yc.AppendSystemPrompt),
			Effort:               strings.TrimSpace(yc.Effort),
			PermissionMode:       strings.TrimSpace(yc.PermissionMode),
			MaxBudgetUSD:         yc.MaxBudgetUSD,
			AddDir:               yc.AddDir,
			AllowedTools:         yc.AllowedTools,
			DisallowedTools:      yc.DisallowedTools,
			McpConfigPath:        strings.TrimSpace(yc.McpConfigPath),
			TimeoutMs:            yc.TimeoutMs,
			MaxTurns:             yc.MaxTurns,
			SettingSources:       yc.SettingSources,
			ThinkingType:         strings.TrimSpace(yc.ThinkingType),
			ThinkingBudgetTokens: yc.ThinkingBudgetTokens,
			HooksJSON:            strings.TrimSpace(yc.HooksJSON),
			StrictMcpConfig:      yc.StrictMcpConfig,
			IncludeHookEvents:    yc.IncludeHookEvents,
			ConfigDir:            strings.TrimSpace(yc.ConfigDir),
			UseMyClaudeConfig:    yc.UseMyClaudeConfig != nil && *yc.UseMyClaudeConfig,
			DeferToolSchemas:     yc.DeferToolSchemas != nil && *yc.DeferToolSchemas,
			UseClaudeCodePreset:  yc.UseClaudeCodePreset != nil && *yc.UseClaudeCodePreset,
			OAuthToken:           strings.TrimSpace(yc.OAuthToken),
		})
		return &Entry{
			ID: id, Kind: kind, Backend: be, DefaultModel: model,
			claudeBinary: bin, claudeCwd: cwd,
			claudeConfigDir:  strings.TrimSpace(yc.ConfigDir),
			claudeOAuthToken: strings.TrimSpace(yc.OAuthToken),
		}, nil

	case providerkind.Bedrock:
		if p.Bedrock == nil {
			return nil, fmt.Errorf("missing bedrock block")
		}
		if p.Anthropic != nil || p.OpenAI != nil || p.Ollama != nil || p.CursorCLI != nil || p.ClaudeCode != nil {
			return nil, fmt.Errorf("bedrock provider must only include bedrock settings")
		}
		region := strings.TrimSpace(p.Bedrock.Region)
		if region == "" {
			return nil, fmt.Errorf("bedrock.region is required")
		}
		model := strings.TrimSpace(p.Bedrock.DefaultModel)
		be, err := bedrockllm.New(context.Background(), bedrockllm.Config{
			Region:        region,
			Profile:       strings.TrimSpace(p.Bedrock.Profile),
			DefaultModel:  model,
			MaxOutputToks: p.Bedrock.MaxOutputTokens,
			EndpointURL:   strings.TrimSpace(p.Bedrock.EndpointURL),

			AssumeRoleARN:   strings.TrimSpace(p.Bedrock.AssumeRoleARN),
			RoleSessionName: strings.TrimSpace(p.Bedrock.RoleSessionName),
			ExternalID:      strings.TrimSpace(p.Bedrock.ExternalID),
		})
		if err != nil {
			return nil, err
		}
		return &Entry{
			ID: id, Kind: kind, Backend: be, DefaultModel: model,
		}, nil

	case providerkind.Gemini:
		if p.Gemini == nil {
			return nil, fmt.Errorf("missing gemini block")
		}
		if p.Anthropic != nil || p.OpenAI != nil || p.Ollama != nil || p.CursorCLI != nil || p.ClaudeCode != nil || p.Bedrock != nil {
			return nil, fmt.Errorf("gemini provider must only include gemini settings")
		}
		key := strings.TrimSpace(p.Gemini.APIKey)
		if key == "" {
			return nil, fmt.Errorf("gemini.api_key is required")
		}
		model := strings.TrimSpace(p.Gemini.DefaultModel)
		if model == "" {
			model = geminillm.DefaultModel
		}
		base := strings.TrimSpace(p.Gemini.BaseURL)
		be, err := geminillm.New(key, base, model, p.Gemini.MaxOutputTokens)
		if err != nil {
			return nil, err
		}
		return &Entry{
			ID: id, Kind: kind, Backend: be, DefaultModel: model,
			geminiBase: base, geminiKey: key,
		}, nil

	case providerkind.OpenRouter:
		if p.OpenRouter == nil {
			return nil, fmt.Errorf("missing openrouter block")
		}
		if p.Anthropic != nil || p.OpenAI != nil || p.Ollama != nil || p.CursorCLI != nil || p.ClaudeCode != nil || p.Bedrock != nil || p.Gemini != nil {
			return nil, fmt.Errorf("openrouter provider must only include openrouter settings")
		}
		key := strings.TrimSpace(p.OpenRouter.APIKey)
		if key == "" {
			return nil, fmt.Errorf("openrouter.api_key is required")
		}
		base := strings.TrimSpace(p.OpenRouter.BaseURL)
		if base == "" {
			base = openrouterllm.DefaultBaseURL
		}
		model := strings.TrimSpace(p.OpenRouter.DefaultModel)
		defaults := openRouterOptionsFromRow(p.OpenRouter)
		be := openrouterllm.New(key, base, model, 0, defaults).
			WithEndpointHealth(openrouterllm.ResolveEndpointHealth(p.OpenRouter.AvoidUnhealthyEndpoints, p.OpenRouter.EndpointHealthTTLSeconds))
		// The catalog scan is the OpenAI-compatible /models call, so it reuses the openAI fields.
		return &Entry{
			ID: id, Kind: kind, Backend: be, DefaultModel: model,
			openAIBase: base, openAIKey: key,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported kind")
	}
}

// DefaultID returns the configured default provider id.
func (r *Registry) DefaultID() string {
	if r == nil {
		return ""
	}
	return r.defaultID
}

// DefaultBackend returns the backend for DefaultID().
func (r *Registry) DefaultBackend() backend.Backend {
	if r == nil {
		return nil
	}
	if e := r.byID[r.defaultID]; e != nil {
		return e.Backend
	}
	return nil
}

// Get returns an entry by id.
func (r *Registry) Get(id string) (*Entry, bool) {
	if r == nil {
		return nil, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false
	}
	e, ok := r.byID[id]
	return e, ok
}

// Backend resolves an backend.Backend by provider id.
func (r *Registry) Backend(id string) (backend.Backend, bool) {
	e, ok := r.Get(id)
	if !ok || e == nil {
		return nil, false
	}
	return e.Backend, true
}

// OrderedIDs returns provider ids in YAML order.
func (r *Registry) OrderedIDs() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// CursorWorkspaces returns absolute, deduplicated cursor_cli.workspace paths for Cursor providers.
func (r *Registry) CursorWorkspaces() []string {
	if r == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, id := range r.order {
		e := r.byID[id]
		if e == nil || e.Kind != providerkind.CursorCLI {
			continue
		}
		w := strings.TrimSpace(e.cursorWorkspace)
		if w == "" {
			continue
		}
		abs, err := filepath.Abs(w)
		if err != nil {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out
}

// RefreshModels queries the provider and replaces the cached catalog.
func (r *Registry) RefreshModels(ctx context.Context, providerID string) (int, error) {
	if r == nil {
		return 0, fmt.Errorf("nil registry")
	}
	e, ok := r.Get(providerID)
	if !ok {
		return 0, fmt.Errorf("unknown provider %q", providerID)
	}
	models, err := e.refreshModels(ctx)
	if err != nil {
		return 0, err
	}
	e.replaceModels(models, time.Now().UnixMilli())
	return len(models), nil
}

// ListCatalog returns cached models, optionally filtered by provider id.
func (r *Registry) ListCatalog(providerFilter string) []CatalogModel {
	if r == nil {
		return nil
	}
	pf := strings.TrimSpace(providerFilter)
	var out []CatalogModel
	for _, id := range r.order {
		if pf != "" && id != pf {
			continue
		}
		if e := r.byID[id]; e != nil {
			out = append(out, e.SnapshotCatalog()...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out
}

// NewRegistryFromEntries builds a registry from explicit entries (primarily for tests).
// defaultID must match a key in entries. Map keys become canonical provider ids; Entry.ID is set when empty.
func NewRegistryFromEntries(defaultID string, entries map[string]*Entry) (*Registry, error) {
	defID := strings.TrimSpace(defaultID)
	if defID == "" {
		return nil, fmt.Errorf("defaultID is required")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("entries is empty")
	}
	byID := make(map[string]*Entry)
	order := make([]string, 0, len(entries))
	for id, e := range entries {
		id = strings.TrimSpace(id)
		if id == "" || e == nil {
			continue
		}
		if strings.TrimSpace(e.ID) == "" {
			e.ID = id
		} else if strings.TrimSpace(e.ID) != id {
			return nil, fmt.Errorf("entry key %q vs Entry.ID %q mismatch", id, e.ID)
		}
		byID[id] = e
		order = append(order, id)
	}
	sort.Strings(order)
	if _, ok := byID[defID]; !ok {
		return nil, fmt.Errorf("default provider %q not found in entries", defID)
	}
	return &Registry{defaultID: defID, byID: byID, order: order}, nil
}
