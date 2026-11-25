package providers

import (
	"fmt"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	bedrockllm "github.com/johnewart/pyxis/internal/llm/bedrock"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/store"
)

// ProtoProviderKind maps API enum to providerkind.Kind.
func ProtoProviderKind(t llmv1.LlmProviderType) (providerkind.Kind, bool) {
	switch t {
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENAI:
		return providerkind.OpenAI, true
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OLLAMA:
		return providerkind.Ollama, true
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_ANTHROPIC:
		return providerkind.Anthropic, true
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CURSOR_CLI:
		return providerkind.CursorCLI, true
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE:
		return providerkind.ClaudeCodeV2, true
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE_V2:
		return providerkind.ClaudeCodeV2, true
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_BEDROCK:
		return providerkind.Bedrock, true
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_GEMINI:
		return providerkind.Gemini, true
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENROUTER:
		return providerkind.OpenRouter, true
	default:
		return providerkind.Unspecified, false
	}
}

func cursorProtoToMode(m llmv1.CursorCliMode) string {
	switch m {
	case llmv1.CursorCliMode_CURSOR_CLI_MODE_ASK:
		return "ask"
	case llmv1.CursorCliMode_CURSOR_CLI_MODE_PLAN:
		return "plan"
	case llmv1.CursorCliMode_CURSOR_CLI_MODE_DEFAULT:
		return "default"
	default:
		return ""
	}
}

// BuildProviderConfigFromRequest converts one gRPC registration request directly into the typed,
// per-kind database row it will be stored as (store.LlmProviderConfig — see its doc comment).
// There is no intermediate document: the proto oneof block IS the row, field for field.
func BuildProviderConfigFromRequest(req *llmv1.RegisterLlmProviderRequest) (*store.LlmProviderConfig, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	id := strings.TrimSpace(req.GetProviderId())
	if id == "" {
		return nil, fmt.Errorf("provider_id is required")
	}
	kind, ok := ProtoProviderKind(req.GetType())
	if !ok {
		return nil, fmt.Errorf("unsupported provider type %v", req.GetType())
	}

	cfg := &store.LlmProviderConfig{ProviderID: id, Kind: kind}

	switch c := req.GetConfig().(type) {
	case nil:
		return nil, fmt.Errorf("provider config is required (oneof anthropic, openai, ollama, cursor_cli, claude_code, bedrock, gemini, openrouter)")
	case *llmv1.RegisterLlmProviderRequest_Anthropic:
		a := c.Anthropic
		if a == nil {
			return nil, fmt.Errorf("anthropic config is required")
		}
		if kind != providerkind.Anthropic {
			return nil, fmt.Errorf("type does not match anthropic config")
		}
		cfg.Anthropic = &store.LlmProviderAnthropicRow{
			APIKey:          a.GetApiKey(),
			BaseURL:         a.GetBaseUrl(),
			DefaultModel:    a.GetDefaultModel(),
			MaxOutputTokens: a.GetMaxOutputTokens(),
		}

	case *llmv1.RegisterLlmProviderRequest_Openai:
		o := c.Openai
		if o == nil {
			return nil, fmt.Errorf("openai config is required")
		}
		if kind != providerkind.OpenAI {
			return nil, fmt.Errorf("type does not match openai config")
		}
		mt := 0
		if o.MaxTokens != nil {
			mt = int(*o.MaxTokens)
		}
		cfg.OpenAI = &store.LlmProviderOpenAIRow{
			APIKey:       o.GetApiKey(),
			BaseURL:      o.GetBaseUrl(),
			DefaultModel: o.GetDefaultModel(),
			MaxTokens:    mt,
		}

	case *llmv1.RegisterLlmProviderRequest_Ollama:
		o := c.Ollama
		if o == nil {
			return nil, fmt.Errorf("ollama config is required")
		}
		if kind != providerkind.Ollama {
			return nil, fmt.Errorf("type does not match ollama config")
		}
		mt := 0
		if o.MaxTokens != nil {
			mt = int(*o.MaxTokens)
		}
		cfg.Ollama = &store.LlmProviderOllamaRow{
			BaseURL:      o.GetBaseUrl(),
			DefaultModel: o.GetDefaultModel(),
			MaxTokens:    mt,
		}

	case *llmv1.RegisterLlmProviderRequest_CursorCli:
		cc := c.CursorCli
		if cc == nil {
			return nil, fmt.Errorf("cursor_cli config is required")
		}
		if kind != providerkind.CursorCLI {
			return nil, fmt.Errorf("type does not match cursor_cli config")
		}
		row := &store.LlmProviderCursorCLIRow{
			Binary:       protoOptionalString(cc.Binary),
			Cwd:          protoOptionalString(cc.Cwd),
			Workspace:    protoOptionalString(cc.Workspace),
			APIKey:       protoOptionalString(cc.ApiKey),
			DefaultModel: protoOptionalString(cc.DefaultModel),
			Mode:         cursorProtoToMode(cc.GetCliMode()),
			SessionID:    protoOptionalString(cc.SessionId),
		}
		if cc.Trust != nil {
			v := *cc.Trust
			row.Trust = &v
		}
		if cc.ApproveMcps != nil {
			v := *cc.ApproveMcps
			row.ApproveMcps = &v
		}
		if cc.Force != nil {
			v := *cc.Force
			row.Force = &v
		}
		if cc.MaxOutputTokens != nil {
			row.MaxOutputTokens = int(*cc.MaxOutputTokens)
		}
		cfg.CursorCLI = row

	case *llmv1.RegisterLlmProviderRequest_ClaudeCode:
		cc := c.ClaudeCode
		if cc == nil {
			return nil, fmt.Errorf("claude_code config is required")
		}
		if kind != providerkind.ClaudeCodeV2 {
			return nil, fmt.Errorf("type does not match claude_code config")
		}
		row := &store.LlmProviderClaudeCodeRow{
			Binary:             protoOptionalString(cc.Binary),
			Cwd:                protoOptionalString(cc.Cwd),
			Agent:              protoOptionalString(cc.Agent),
			DefaultModel:       protoOptionalString(cc.DefaultModel),
			SystemPrompt:       protoOptionalString(cc.SystemPrompt),
			AppendSystemPrompt: protoOptionalString(cc.AppendSystemPrompt),
			Effort:             protoOptionalString(cc.Effort),
			PermissionMode:     protoOptionalString(cc.PermissionMode),
			AddDir:             cc.GetAddDir(),
			AllowedTools:       cc.GetAllowedTools(),
			DisallowedTools:    cc.GetDisallowedTools(),
			McpConfigPath:      protoOptionalString(cc.McpConfigPath),
			SettingSources:     cc.GetSettingSources(),
			ThinkingType:       protoOptionalString(cc.ThinkingType),
			HooksJSON:          protoOptionalString(cc.HooksJson),
			ConfigDir:          protoOptionalString(cc.ConfigDir),
			OAuthToken:         protoOptionalString(cc.OauthToken),
		}
		if cc.MaxBudgetUsd != nil {
			v := *cc.MaxBudgetUsd
			row.MaxBudgetUSD = &v
		}
		if cc.TimeoutMs != nil {
			row.TimeoutMs = int(*cc.TimeoutMs)
		}
		if cc.MaxTurns != nil {
			row.MaxTurns = int(*cc.MaxTurns)
		}
		if cc.ThinkingBudgetTokens != nil {
			v := int(*cc.ThinkingBudgetTokens)
			row.ThinkingBudgetTokens = &v
		}
		if cc.StrictMcpConfig != nil {
			v := *cc.StrictMcpConfig
			row.StrictMcpConfig = &v
		}
		if cc.UseMyClaudeConfig != nil {
			v := *cc.UseMyClaudeConfig
			row.UseMyClaudeConfig = &v
		}
		if cc.DeferToolSchemas != nil {
			v := *cc.DeferToolSchemas
			row.DeferToolSchemas = &v
		}
		if cc.IncludeHookEvents != nil {
			v := *cc.IncludeHookEvents
			row.IncludeHookEvents = &v
		}
		if cc.UseClaudeCodePreset != nil {
			v := *cc.UseClaudeCodePreset
			row.UseClaudeCodePreset = &v
		}
		cfg.ClaudeCode = row

	case *llmv1.RegisterLlmProviderRequest_Bedrock:
		b := c.Bedrock
		if b == nil {
			return nil, fmt.Errorf("bedrock config is required")
		}
		if kind != providerkind.Bedrock {
			return nil, fmt.Errorf("type does not match bedrock config")
		}
		row, err := bedrockRowFromProto(b)
		if err != nil {
			return nil, err
		}
		cfg.Bedrock = row

	case *llmv1.RegisterLlmProviderRequest_Openrouter:
		o := c.Openrouter
		if o == nil {
			return nil, fmt.Errorf("openrouter config is required")
		}
		if kind != providerkind.OpenRouter {
			return nil, fmt.Errorf("type does not match openrouter config")
		}
		row, err := openRouterRowFromProto(o)
		if err != nil {
			return nil, err
		}
		cfg.OpenRouter = row

	case *llmv1.RegisterLlmProviderRequest_Gemini:
		g := c.Gemini
		if g == nil {
			return nil, fmt.Errorf("gemini config is required")
		}
		if kind != providerkind.Gemini {
			return nil, fmt.Errorf("type does not match gemini config")
		}
		cfg.Gemini = &store.LlmProviderGeminiRow{
			APIKey:          g.GetApiKey(),
			BaseURL:         g.GetBaseUrl(),
			DefaultModel:    g.GetDefaultModel(),
			MaxOutputTokens: g.GetMaxOutputTokens(),
		}

	default:
		return nil, fmt.Errorf("provider config is required (oneof anthropic, openai, ollama, cursor_cli, claude_code, bedrock, gemini, openrouter)")
	}

	return cfg, nil
}

// bedrockRowFromProto maps and validates a Bedrock proto block shared by register and update.
// An empty role ARN means no AssumeRole; a non-empty ARN and any non-empty session name must
// be valid. The external ID is stored as given and never validated or echoed in errors.
func bedrockRowFromProto(b *llmv1.BedrockLlmConfig) (*store.LlmProviderBedrockRow, error) {
	row := &store.LlmProviderBedrockRow{
		Region:          b.GetRegion(),
		Profile:         b.GetProfile(),
		DefaultModel:    b.GetDefaultModel(),
		MaxOutputTokens: b.GetMaxOutputTokens(),
		EndpointURL:     b.GetEndpointUrl(),
		AssumeRoleARN:   protoOptionalString(b.AssumeRoleArn),
		RoleSessionName: protoOptionalString(b.RoleSessionName),
		ExternalID:      protoOptionalString(b.ExternalId),
	}
	if row.AssumeRoleARN != "" {
		if err := bedrockllm.ValidateAssumeRoleARN(row.AssumeRoleARN); err != nil {
			return nil, err
		}
	}
	if row.RoleSessionName != "" {
		if err := bedrockllm.ValidateRoleSessionName(row.RoleSessionName); err != nil {
			return nil, err
		}
	}
	return row, nil
}

// openRouterRowFromProto maps an OpenRouter proto block shared by register and update.
// DefaultCallOptions fields are stored in typed columns.
func openRouterRowFromProto(o *llmv1.OpenRouterLlmConfig) (*store.LlmProviderOpenRouterRow, error) {
	row := &store.LlmProviderOpenRouterRow{
		APIKey:       o.GetApiKey(),
		BaseURL:      protoOptionalString(o.BaseUrl),
		DefaultModel: protoOptionalString(o.DefaultModel),
	}
	if o.AvoidUnhealthyEndpoints != nil {
		v := o.GetAvoidUnhealthyEndpoints()
		row.AvoidUnhealthyEndpoints = &v
	}
	if o.EndpointHealthTtlSeconds != nil {
		if o.GetEndpointHealthTtlSeconds() < 0 {
			return nil, fmt.Errorf("openrouter.endpoint_health_ttl_seconds must not be negative")
		}
		row.EndpointHealthTTLSeconds = o.GetEndpointHealthTtlSeconds()
	}
	if d := o.GetDefaultCallOptions(); d != nil {
		row.Sort = int32(d.GetSort())
		row.ProviderOrder = append([]string(nil), d.GetOrder()...)
		row.ProviderOnly = append([]string(nil), d.GetOnly()...)
		row.ProviderIgnore = append([]string(nil), d.GetIgnore()...)
		if d.AllowFallbacks != nil {
			v := d.GetAllowFallbacks()
			row.AllowFallbacks = &v
		}
	}
	return row, nil
}

// openRouterOptionsFromRow is the reverse of the mapping in openRouterRowFromProto; it
// returns nil when no default option is set.
func openRouterOptionsFromRow(r *store.LlmProviderOpenRouterRow) *llmv1.OpenRouterCallOptions {
	if r.Sort == 0 && len(r.ProviderOrder) == 0 && len(r.ProviderOnly) == 0 &&
		len(r.ProviderIgnore) == 0 && r.AllowFallbacks == nil {
		return nil
	}
	opts := &llmv1.OpenRouterCallOptions{
		Sort:   llmv1.OpenRouterCallOptions_Sort(r.Sort),
		Order:  append([]string(nil), r.ProviderOrder...),
		Only:   append([]string(nil), r.ProviderOnly...),
		Ignore: append([]string(nil), r.ProviderIgnore...),
	}
	if r.AllowFallbacks != nil {
		v := *r.AllowFallbacks
		opts.AllowFallbacks = &v
	}
	return opts
}

func protoOptionalString(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

// rowOptionalString is the reverse of protoOptionalString: nil for empty, else a pointer to the
// string. Used when building proto responses back from stored rows.
func rowOptionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// KindToProto is the reverse of ProtoProviderKind.
func KindToProto(k providerkind.Kind) llmv1.LlmProviderType {
	switch k {
	case providerkind.OpenAI:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENAI
	case providerkind.Ollama:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OLLAMA
	case providerkind.Anthropic:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_ANTHROPIC
	case providerkind.CursorCLI:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CURSOR_CLI
	case providerkind.ClaudeCodeV2:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE_V2
	case providerkind.Bedrock:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_BEDROCK
	case providerkind.Gemini:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_GEMINI
	case providerkind.OpenRouter:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENROUTER
	default:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_UNSPECIFIED
	}
}

// cursorModeToProto is the reverse of cursorProtoToMode.
func cursorModeToProto(mode string) llmv1.CursorCliMode {
	switch mode {
	case "ask":
		return llmv1.CursorCliMode_CURSOR_CLI_MODE_ASK
	case "plan":
		return llmv1.CursorCliMode_CURSOR_CLI_MODE_PLAN
	case "default":
		return llmv1.CursorCliMode_CURSOR_CLI_MODE_DEFAULT
	default:
		return llmv1.CursorCliMode_CURSOR_CLI_MODE_UNSPECIFIED
	}
}

// BuildProviderConfigFromUpdateRequest is BuildProviderConfigFromRequest's twin for
// UpdateLlmProviderConfigRequest (same field-for-field oneof shape, different generated Go type).
func BuildProviderConfigFromUpdateRequest(req *llmv1.UpdateLlmProviderConfigRequest) (*store.LlmProviderConfig, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	id := strings.TrimSpace(req.GetProviderId())
	if id == "" {
		return nil, fmt.Errorf("provider_id is required")
	}
	kind, ok := ProtoProviderKind(req.GetType())
	if !ok {
		return nil, fmt.Errorf("unsupported provider type %v", req.GetType())
	}

	cfg := &store.LlmProviderConfig{ProviderID: id, Kind: kind}

	switch c := req.GetConfig().(type) {
	case nil:
		return nil, fmt.Errorf("provider config is required (oneof anthropic, openai, ollama, cursor_cli, claude_code, bedrock, gemini, openrouter)")
	case *llmv1.UpdateLlmProviderConfigRequest_Anthropic:
		a := c.Anthropic
		if a == nil {
			return nil, fmt.Errorf("anthropic config is required")
		}
		if kind != providerkind.Anthropic {
			return nil, fmt.Errorf("type does not match anthropic config")
		}
		cfg.Anthropic = &store.LlmProviderAnthropicRow{
			APIKey:          a.GetApiKey(),
			BaseURL:         a.GetBaseUrl(),
			DefaultModel:    a.GetDefaultModel(),
			MaxOutputTokens: a.GetMaxOutputTokens(),
		}

	case *llmv1.UpdateLlmProviderConfigRequest_Openai:
		o := c.Openai
		if o == nil {
			return nil, fmt.Errorf("openai config is required")
		}
		if kind != providerkind.OpenAI {
			return nil, fmt.Errorf("type does not match openai config")
		}
		mt := 0
		if o.MaxTokens != nil {
			mt = int(*o.MaxTokens)
		}
		cfg.OpenAI = &store.LlmProviderOpenAIRow{
			APIKey:       o.GetApiKey(),
			BaseURL:      o.GetBaseUrl(),
			DefaultModel: o.GetDefaultModel(),
			MaxTokens:    mt,
		}

	case *llmv1.UpdateLlmProviderConfigRequest_Ollama:
		o := c.Ollama
		if o == nil {
			return nil, fmt.Errorf("ollama config is required")
		}
		if kind != providerkind.Ollama {
			return nil, fmt.Errorf("type does not match ollama config")
		}
		mt := 0
		if o.MaxTokens != nil {
			mt = int(*o.MaxTokens)
		}
		cfg.Ollama = &store.LlmProviderOllamaRow{
			BaseURL:      o.GetBaseUrl(),
			DefaultModel: o.GetDefaultModel(),
			MaxTokens:    mt,
		}

	case *llmv1.UpdateLlmProviderConfigRequest_CursorCli:
		cc := c.CursorCli
		if cc == nil {
			return nil, fmt.Errorf("cursor_cli config is required")
		}
		if kind != providerkind.CursorCLI {
			return nil, fmt.Errorf("type does not match cursor_cli config")
		}
		row := &store.LlmProviderCursorCLIRow{
			Binary:       protoOptionalString(cc.Binary),
			Cwd:          protoOptionalString(cc.Cwd),
			Workspace:    protoOptionalString(cc.Workspace),
			APIKey:       protoOptionalString(cc.ApiKey),
			DefaultModel: protoOptionalString(cc.DefaultModel),
			Mode:         cursorProtoToMode(cc.GetCliMode()),
			SessionID:    protoOptionalString(cc.SessionId),
		}
		if cc.Trust != nil {
			v := *cc.Trust
			row.Trust = &v
		}
		if cc.ApproveMcps != nil {
			v := *cc.ApproveMcps
			row.ApproveMcps = &v
		}
		if cc.Force != nil {
			v := *cc.Force
			row.Force = &v
		}
		if cc.MaxOutputTokens != nil {
			row.MaxOutputTokens = int(*cc.MaxOutputTokens)
		}
		cfg.CursorCLI = row

	case *llmv1.UpdateLlmProviderConfigRequest_ClaudeCode:
		cc := c.ClaudeCode
		if cc == nil {
			return nil, fmt.Errorf("claude_code config is required")
		}
		if kind != providerkind.ClaudeCodeV2 {
			return nil, fmt.Errorf("type does not match claude_code config")
		}
		row := &store.LlmProviderClaudeCodeRow{
			Binary:             protoOptionalString(cc.Binary),
			Cwd:                protoOptionalString(cc.Cwd),
			Agent:              protoOptionalString(cc.Agent),
			DefaultModel:       protoOptionalString(cc.DefaultModel),
			SystemPrompt:       protoOptionalString(cc.SystemPrompt),
			AppendSystemPrompt: protoOptionalString(cc.AppendSystemPrompt),
			Effort:             protoOptionalString(cc.Effort),
			PermissionMode:     protoOptionalString(cc.PermissionMode),
			AddDir:             cc.GetAddDir(),
			AllowedTools:       cc.GetAllowedTools(),
			DisallowedTools:    cc.GetDisallowedTools(),
			McpConfigPath:      protoOptionalString(cc.McpConfigPath),
			SettingSources:     cc.GetSettingSources(),
			ThinkingType:       protoOptionalString(cc.ThinkingType),
			HooksJSON:          protoOptionalString(cc.HooksJson),
			ConfigDir:          protoOptionalString(cc.ConfigDir),
			OAuthToken:         protoOptionalString(cc.OauthToken),
		}
		if cc.MaxBudgetUsd != nil {
			v := *cc.MaxBudgetUsd
			row.MaxBudgetUSD = &v
		}
		if cc.TimeoutMs != nil {
			row.TimeoutMs = int(*cc.TimeoutMs)
		}
		if cc.MaxTurns != nil {
			row.MaxTurns = int(*cc.MaxTurns)
		}
		if cc.ThinkingBudgetTokens != nil {
			v := int(*cc.ThinkingBudgetTokens)
			row.ThinkingBudgetTokens = &v
		}
		if cc.StrictMcpConfig != nil {
			v := *cc.StrictMcpConfig
			row.StrictMcpConfig = &v
		}
		if cc.UseMyClaudeConfig != nil {
			v := *cc.UseMyClaudeConfig
			row.UseMyClaudeConfig = &v
		}
		if cc.DeferToolSchemas != nil {
			v := *cc.DeferToolSchemas
			row.DeferToolSchemas = &v
		}
		if cc.IncludeHookEvents != nil {
			v := *cc.IncludeHookEvents
			row.IncludeHookEvents = &v
		}
		if cc.UseClaudeCodePreset != nil {
			v := *cc.UseClaudeCodePreset
			row.UseClaudeCodePreset = &v
		}
		cfg.ClaudeCode = row

	case *llmv1.UpdateLlmProviderConfigRequest_Bedrock:
		b := c.Bedrock
		if b == nil {
			return nil, fmt.Errorf("bedrock config is required")
		}
		if kind != providerkind.Bedrock {
			return nil, fmt.Errorf("type does not match bedrock config")
		}
		row, err := bedrockRowFromProto(b)
		if err != nil {
			return nil, err
		}
		cfg.Bedrock = row

	case *llmv1.UpdateLlmProviderConfigRequest_Openrouter:
		o := c.Openrouter
		if o == nil {
			return nil, fmt.Errorf("openrouter config is required")
		}
		if kind != providerkind.OpenRouter {
			return nil, fmt.Errorf("type does not match openrouter config")
		}
		row, err := openRouterRowFromProto(o)
		if err != nil {
			return nil, err
		}
		cfg.OpenRouter = row

	case *llmv1.UpdateLlmProviderConfigRequest_Gemini:
		g := c.Gemini
		if g == nil {
			return nil, fmt.Errorf("gemini config is required")
		}
		if kind != providerkind.Gemini {
			return nil, fmt.Errorf("type does not match gemini config")
		}
		cfg.Gemini = &store.LlmProviderGeminiRow{
			APIKey:          g.GetApiKey(),
			BaseURL:         g.GetBaseUrl(),
			DefaultModel:    g.GetDefaultModel(),
			MaxOutputTokens: g.GetMaxOutputTokens(),
		}

	default:
		return nil, fmt.Errorf("provider config is required (oneof anthropic, openai, ollama, cursor_cli, claude_code, bedrock, gemini, openrouter)")
	}

	return cfg, nil
}

// BuildGetLlmProviderConfigResponse converts a resolved store.LlmProviderConfig (as returned by
// LlmProviderRepo.Get) back into the wire response for GetLlmProviderConfig — the reverse of
// BuildProviderConfigFromRequest/BuildProviderConfigFromUpdateRequest.
func BuildGetLlmProviderConfigResponse(cfg *store.LlmProviderConfig) (*llmv1.GetLlmProviderConfigResponse, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil provider config")
	}
	// cfg.Kind IS the enum; the repo resolved the stored id once on read.
	if !cfg.Kind.Valid() {
		return nil, fmt.Errorf("provider %q has no kind set", cfg.ProviderID)
	}
	resp := &llmv1.GetLlmProviderConfigResponse{
		ProviderId: cfg.ProviderID,
		Type:       KindToProto(cfg.Kind),
	}
	switch {
	case cfg.Anthropic != nil:
		a := cfg.Anthropic
		resp.Config = &llmv1.GetLlmProviderConfigResponse_Anthropic{Anthropic: &llmv1.AnthropicLlmConfig{
			ApiKey:          a.APIKey,
			BaseUrl:         rowOptionalString(a.BaseURL),
			DefaultModel:    rowOptionalString(a.DefaultModel),
			MaxOutputTokens: rowOptionalInt64(a.MaxOutputTokens),
		}}
	case cfg.OpenAI != nil:
		o := cfg.OpenAI
		resp.Config = &llmv1.GetLlmProviderConfigResponse_Openai{Openai: &llmv1.OpenAiLlmConfig{
			ApiKey:       o.APIKey,
			BaseUrl:      rowOptionalString(o.BaseURL),
			DefaultModel: rowOptionalString(o.DefaultModel),
			MaxTokens:    rowOptionalInt32(o.MaxTokens),
		}}
	case cfg.Ollama != nil:
		o := cfg.Ollama
		resp.Config = &llmv1.GetLlmProviderConfigResponse_Ollama{Ollama: &llmv1.OllamaLlmConfig{
			BaseUrl:      rowOptionalString(o.BaseURL),
			DefaultModel: rowOptionalString(o.DefaultModel),
			MaxTokens:    rowOptionalInt32(o.MaxTokens),
		}}
	case cfg.CursorCLI != nil:
		cc := cfg.CursorCLI
		row := &llmv1.CursorCliLlmConfig{
			Binary:       rowOptionalString(cc.Binary),
			Cwd:          rowOptionalString(cc.Cwd),
			Workspace:    rowOptionalString(cc.Workspace),
			ApiKey:       rowOptionalString(cc.APIKey),
			DefaultModel: rowOptionalString(cc.DefaultModel),
			CliMode:      cursorModeToProto(cc.Mode),
			SessionId:    rowOptionalString(cc.SessionID),
			Trust:        cc.Trust,
			ApproveMcps:  cc.ApproveMcps,
			Force:        cc.Force,
		}
		if cc.MaxOutputTokens != 0 {
			v := int32(cc.MaxOutputTokens)
			row.MaxOutputTokens = &v
		}
		resp.Config = &llmv1.GetLlmProviderConfigResponse_CursorCli{CursorCli: row}
	case cfg.ClaudeCode != nil:
		cc := cfg.ClaudeCode
		row := &llmv1.ClaudeCodeLlmConfig{
			Binary:              rowOptionalString(cc.Binary),
			Cwd:                 rowOptionalString(cc.Cwd),
			Agent:               rowOptionalString(cc.Agent),
			DefaultModel:        rowOptionalString(cc.DefaultModel),
			SystemPrompt:        rowOptionalString(cc.SystemPrompt),
			AppendSystemPrompt:  rowOptionalString(cc.AppendSystemPrompt),
			Effort:              rowOptionalString(cc.Effort),
			PermissionMode:      rowOptionalString(cc.PermissionMode),
			MaxBudgetUsd:        cc.MaxBudgetUSD,
			AddDir:              cc.AddDir,
			AllowedTools:        cc.AllowedTools,
			DisallowedTools:     cc.DisallowedTools,
			McpConfigPath:       rowOptionalString(cc.McpConfigPath),
			SettingSources:      cc.SettingSources,
			ThinkingType:        rowOptionalString(cc.ThinkingType),
			HooksJson:           rowOptionalString(cc.HooksJSON),
			ConfigDir:           rowOptionalString(cc.ConfigDir),
			StrictMcpConfig:     cc.StrictMcpConfig,
			UseMyClaudeConfig:   cc.UseMyClaudeConfig,
			DeferToolSchemas:    cc.DeferToolSchemas,
			IncludeHookEvents:   cc.IncludeHookEvents,
			UseClaudeCodePreset: cc.UseClaudeCodePreset,
			OauthToken:          rowOptionalString(cc.OAuthToken),
		}
		if cc.TimeoutMs != 0 {
			v := int32(cc.TimeoutMs)
			row.TimeoutMs = &v
		}
		if cc.MaxTurns != 0 {
			v := int32(cc.MaxTurns)
			row.MaxTurns = &v
		}
		if cc.ThinkingBudgetTokens != nil {
			v := int32(*cc.ThinkingBudgetTokens)
			row.ThinkingBudgetTokens = &v
		}
		resp.Config = &llmv1.GetLlmProviderConfigResponse_ClaudeCode{ClaudeCode: row}
	case cfg.Bedrock != nil:
		b := cfg.Bedrock
		resp.Config = &llmv1.GetLlmProviderConfigResponse_Bedrock{Bedrock: &llmv1.BedrockLlmConfig{
			Region:          b.Region,
			Profile:         rowOptionalString(b.Profile),
			DefaultModel:    rowOptionalString(b.DefaultModel),
			MaxOutputTokens: rowOptionalInt64(b.MaxOutputTokens),
			EndpointUrl:     rowOptionalString(b.EndpointURL),
			AssumeRoleArn:   rowOptionalString(b.AssumeRoleARN),
			RoleSessionName: rowOptionalString(b.RoleSessionName),
			// ExternalId is a secret: deliberately never returned on read.
		}}
	case cfg.Gemini != nil:
		g := cfg.Gemini
		resp.Config = &llmv1.GetLlmProviderConfigResponse_Gemini{Gemini: &llmv1.GeminiLlmConfig{
			ApiKey:          g.APIKey,
			BaseUrl:         rowOptionalString(g.BaseURL),
			DefaultModel:    rowOptionalString(g.DefaultModel),
			MaxOutputTokens: rowOptionalInt64(g.MaxOutputTokens),
		}}
	case cfg.OpenRouter != nil:
		o := cfg.OpenRouter
		opts := openRouterOptionsFromRow(o)
		// ApiKey is a secret: deliberately never returned on read.
		resp.Config = &llmv1.GetLlmProviderConfigResponse_Openrouter{Openrouter: &llmv1.OpenRouterLlmConfig{
			BaseUrl:            rowOptionalString(o.BaseURL),
			DefaultModel:       rowOptionalString(o.DefaultModel),
			DefaultCallOptions: opts,

			AvoidUnhealthyEndpoints:  o.AvoidUnhealthyEndpoints,
			EndpointHealthTtlSeconds: rowOptionalInt32(int(o.EndpointHealthTTLSeconds)),
		}}
	default:
		return nil, fmt.Errorf("provider config has no backend block set")
	}
	return resp, nil
}

func rowOptionalInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func rowOptionalInt32(v int) *int32 {
	if v == 0 {
		return nil
	}
	vv := int32(v)
	return &vv
}
