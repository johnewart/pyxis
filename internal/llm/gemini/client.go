package geminillm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/common"
	llmtools "github.com/johnewart/pyxis/internal/llm/tools"
)

const defaultMaxTokens int32 = 8192

const DefaultModel = "gemini-3.5-flash"

// Client calls the Gemini Developer API via go-genai.
type Client struct {
	inner          *genai.Client
	defaultModel   string
	defaultMaxToks int32
}

// New builds a client. apiKey is required for production; baseURL is optional.
func New(apiKey, baseURL, model string, maxTokens int64) (*Client, error) {
	if maxTokens <= 0 {
		maxTokens = int64(defaultMaxTokens)
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}
	cc := &genai.ClientConfig{
		APIKey:  strings.TrimSpace(apiKey),
		Backend: genai.BackendGeminiAPI,
	}
	if u := strings.TrimSpace(baseURL); u != "" {
		cc.HTTPOptions = genai.HTTPOptions{
			BaseURL: strings.TrimRight(u, "/") + "/",
		}
	}
	inner, err := genai.NewClient(context.Background(), cc)
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}
	return &Client{
		inner:          inner,
		defaultModel:   model,
		defaultMaxToks: int32(maxTokens),
	}, nil
}

func newClientFromGenAI(inner *genai.Client, model string, maxTokens int32) *Client {
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	return &Client{inner: inner, defaultModel: model, defaultMaxToks: maxTokens}
}

func (c *Client) buildConfig(system *genai.Content, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*genai.GenerateContentConfig, error) {
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: system,
		MaxOutputTokens:   c.defaultMaxToks,
	}
	if opt != nil {
		if v, ok := common.PickTemperature(opt); ok {
			cfg.Temperature = genai.Ptr(v)
		}
		if v, ok := common.PickTopP(opt); ok {
			cfg.TopP = genai.Ptr(v)
		}
		if opt.GetMaxTokens() > 0 {
			cfg.MaxOutputTokens = int32(opt.GetMaxTokens())
		}
	}
	if len(tools) > 0 {
		gt, err := llmtools.ToGeminiTools(tools)
		if err != nil {
			return nil, err
		}
		cfg.Tools = gt
	}
	applyResponseFormat(cfg, opt)
	return cfg, nil
}

// applyResponseFormat maps structured-output requests onto Gemini's
// response_mime_type / response_schema.
//
// The MIME type alone already forces valid JSON, so a schema we cannot convert
// costs shape enforcement but never the JSON guarantee — we attach what we can
// and leave the rest to the caller's parser, per the proto's contract.
func applyResponseFormat(cfg *genai.GenerateContentConfig, opt *llmv1.CallOptions) {
	rf, ok := common.PickResponseFormat(opt)
	if !ok || !rf.JSON {
		return
	}
	cfg.ResponseMIMEType = "application/json"
	if len(rf.Schema) == 0 {
		return
	}
	if schema, ok := jsonSchemaToGenai(rf.Schema); ok {
		cfg.ResponseSchema = schema
	}
	// Unconvertible: keep the JSON guarantee, lose shape enforcement.
}

// jsonSchemaToGenai converts a JSON Schema document to genai.Schema.
//
// Not a plain json.Unmarshal: genai.Type is an UPPERCASE enum ("OBJECT",
// "STRING") while JSON Schema uses lowercase ("object", "string"). Unmarshalling
// directly succeeds but yields a schema Gemini does not recognise — valid-looking
// and silently wrong. Normalize type names first.
func jsonSchemaToGenai(raw []byte) (*genai.Schema, bool) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, false
	}
	normalized, ok := normalizeSchemaTypes(doc)
	if !ok {
		return nil, false
	}
	out, err := json.Marshal(normalized)
	if err != nil {
		return nil, false
	}
	var schema genai.Schema
	if err := json.Unmarshal(out, &schema); err != nil {
		return nil, false
	}
	if schema.Type == "" {
		return nil, false
	}
	return &schema, true
}

// normalizeSchemaTypes uppercases every "type" value in the document.
// Reports false for shapes genai cannot express — notably union types
// (`"type": ["string","null"]`), which degrade rather than mislead.
func normalizeSchemaTypes(node any) (any, bool) {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, val := range v {
			if key == "type" {
				s, isString := val.(string)
				if !isString {
					return nil, false
				}
				out[key] = strings.ToUpper(s)
				continue
			}
			converted, ok := normalizeSchemaTypes(val)
			if !ok {
				return nil, false
			}
			out[key] = converted
		}
		return out, true
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			converted, ok := normalizeSchemaTypes(item)
			if !ok {
				return nil, false
			}
			out = append(out, converted)
		}
		return out, true
	default:
		return node, true
	}
}

func (c *Client) model(opt *llmv1.CallOptions) string {
	return common.PickModel(opt, c.defaultModel)
}

// Chat runs a single non-streaming GenerateContent call.
func (c *Client) Chat(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	system, contents, err := ProtoMessagesToGemini(messages)
	if err != nil {
		return nil, err
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("no messages to send after filtering (need at least one user/assistant turn)")
	}
	cfg, err := c.buildConfig(system, opt, tools)
	if err != nil {
		return nil, err
	}
	resp, err := c.inner.Models.GenerateContent(ctx, c.model(opt), contents, cfg)
	if err != nil {
		return nil, err
	}
	return assistantReplyFromResponse(ctx, resp)
}

// Generate maps system prompt + single user message to GenerateContent.
func (c *Client) Generate(ctx context.Context, systemPrompt, prompt string, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("prompt must be non-empty")
	}
	var system *genai.Content
	if t := strings.TrimSpace(systemPrompt); t != "" {
		system = &genai.Content{Parts: []*genai.Part{genai.NewPartFromText(t)}}
	}
	contents := []*genai.Content{{
		Role:  string(genai.RoleUser),
		Parts: []*genai.Part{genai.NewPartFromText(prompt)},
	}}
	cfg, err := c.buildConfig(system, opt, tools)
	if err != nil {
		return nil, err
	}
	resp, err := c.inner.Models.GenerateContent(ctx, c.model(opt), contents, cfg)
	if err != nil {
		return nil, err
	}
	return assistantReplyFromResponse(ctx, resp)
}

// ScanModels lists Gemini models via the Developer API (for integration tests and diagnostics).
func ScanModels(ctx context.Context, apiKey, baseURL, fallbackModel string) ([]string, error) {
	cc := &genai.ClientConfig{
		APIKey:  strings.TrimSpace(apiKey),
		Backend: genai.BackendGeminiAPI,
	}
	if u := strings.TrimSpace(baseURL); u != "" {
		cc.HTTPOptions = genai.HTTPOptions{BaseURL: strings.TrimRight(u, "/") + "/"}
	}
	inner, err := genai.NewClient(ctx, cc)
	if err != nil {
		return nil, err
	}
	var ids []string
	for model, err := range inner.Models.All(ctx) {
		if err != nil {
			return nil, err
		}
		if model == nil {
			continue
		}
		id := strings.TrimSpace(model.Name)
		id = strings.TrimPrefix(id, "models/")
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 && strings.TrimSpace(fallbackModel) != "" {
		ids = []string{strings.TrimSpace(fallbackModel)}
	}
	return ids, nil
}
