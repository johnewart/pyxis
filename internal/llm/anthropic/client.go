package anthropicllm

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/tools"
)

const defaultMaxTokens int64 = 8192

// Client calls the Anthropic Messages API using the official SDK (backend.Backend).
type Client struct {
	inner          anthropic.Client
	defaultModel   anthropic.Model
	defaultMaxToks int64
}

// New builds a client. apiKey is required. baseURL is optional (empty uses the SDK default).
func New(apiKey, baseURL string, model anthropic.Model, maxTokens int64) *Client {
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if u := strings.TrimSpace(baseURL); u != "" {
		opts = append(opts, option.WithBaseURL(strings.TrimRight(u, "/")))
	}
	return &Client{
		inner:          anthropic.NewClient(opts...),
		defaultModel:   model,
		defaultMaxToks: maxTokens,
	}
}

func (c *Client) applyCallOptions(body anthropic.MessageNewParams, opt *llmv1.CallOptions) anthropic.MessageNewParams {
	if opt == nil {
		return body
	}
	if m := strings.TrimSpace(opt.GetModel()); m != "" {
		body.Model = anthropic.Model(m)
	}
	if opt.GetMaxTokens() > 0 {
		body.MaxTokens = int64(opt.GetMaxTokens())
	}
	if opt.Temperature != nil {
		body.Temperature = param.NewOpt(float64(*opt.Temperature))
	}
	if opt.TopP != nil {
		body.TopP = param.NewOpt(float64(*opt.TopP))
	}
	if opt.TopK != nil {
		body.TopK = param.NewOpt(*opt.TopK)
	}
	return body
}

// Chat runs a single non-streaming Messages call (maps to TS AnthropicLLM.chat).
func (c *Client) Chat(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	system, msgParams, err := ProtoMessagesToParams(messages, opt)
	if err != nil {
		return nil, err
	}
	if len(msgParams) == 0 {
		return nil, fmt.Errorf("no messages to send after filtering (need at least one user/assistant turn)")
	}

	body := anthropic.MessageNewParams{
		Model:     c.defaultModel,
		MaxTokens: c.defaultMaxToks,
		Messages:  msgParams,
		System:    system,
	}
	body = c.applyCallOptions(body, opt)
	if len(tools) > 0 {
		at, err := llmtools.ToAnthropicTools(tools)
		if err != nil {
			return nil, err
		}
		body.Tools = at
	}
	body, err = applyToolChoice(body, opt, tools)
	if err != nil {
		return nil, err
	}
	body, forced := applyResponseFormat(body, opt, len(tools))

	resp, err := c.inner.Messages.New(ctx, body)
	if err != nil {
		return nil, err
	}
	reply, err := assistantReplyFromMessage(ctx, resp)
	if err != nil {
		return nil, err
	}
	if forced {
		liftStructuredOutput(reply)
	}
	return reply, nil
}

// Generate maps to TS AnthropicLLM.generate: system prompt + single user message.
func (c *Client) Generate(ctx context.Context, systemPrompt, prompt string, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("prompt must be non-empty")
	}

	msgs := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
	}
	return c.generateMessages(ctx, systemPrompt, msgs, opt, tools)
}

// GenerateParts is Generate with the user message split into one text block
// per part; breakpoint parts get cache_control within the 4-marker limit.
func (c *Client) GenerateParts(ctx context.Context, systemPrompt string, parts []*llmv1.PromptPart, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("prompt parts must be non-empty")
	}
	limit := maxCacheMarkers
	if strings.TrimSpace(systemPrompt) != "" {
		probe := anthropic.TextBlockParam{}
		attachAnthropicPromptCacheToTextBlock(&probe, opt)
		if probe.CacheControl.Type != "" {
			limit--
		}
	}
	blocks := buildUserBlocksLimit(parts, opt, limit)
	if len(blocks) == 0 {
		return nil, fmt.Errorf("prompt parts contain no non-empty text")
	}
	msgs := []anthropic.MessageParam{anthropic.NewUserMessage(blocks...)}
	return c.generateMessages(ctx, systemPrompt, msgs, opt, tools)
}

func (c *Client) generateMessages(ctx context.Context, systemPrompt string, msgs []anthropic.MessageParam, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	var system []anthropic.TextBlockParam
	if t := strings.TrimSpace(systemPrompt); t != "" {
		b := anthropic.TextBlockParam{Text: t}
		attachAnthropicPromptCacheToTextBlock(&b, opt)
		system = []anthropic.TextBlockParam{b}
	}

	body := anthropic.MessageNewParams{
		Model:     c.defaultModel,
		MaxTokens: c.defaultMaxToks,
		Messages:  msgs,
		System:    system,
	}
	body = c.applyCallOptions(body, opt)
	if len(tools) > 0 {
		at, err := llmtools.ToAnthropicTools(tools)
		if err != nil {
			return nil, err
		}
		body.Tools = at
	}
	var tcErr error
	body, tcErr = applyToolChoice(body, opt, tools)
	if tcErr != nil {
		return nil, tcErr
	}
	body, forced := applyResponseFormat(body, opt, len(tools))

	resp, err := c.inner.Messages.New(ctx, body)
	if err != nil {
		return nil, err
	}
	reply, err := assistantReplyFromMessage(ctx, resp)
	if err != nil {
		return nil, err
	}
	if forced {
		liftStructuredOutput(reply)
	}
	return reply, nil
}
