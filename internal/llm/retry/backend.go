package retry

import (
	"context"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/backend"
)

// Wrap decorates be so Chat, Generate and ChatStream retry per policy p.
// provider is used in per-attempt logs. A nil be is returned unchanged.
func Wrap(be backend.Backend, p Policy, provider string) backend.Backend {
	if be == nil {
		return be
	}
	p.Provider = provider
	return &retrying{inner: be, policy: p}
}

type retrying struct {
	inner  backend.Backend
	policy Policy
}

func (r *retrying) pol(opt *llmv1.CallOptions) Policy {
	p := r.policy
	p.Model = opt.GetModel()
	return p
}

func (r *retrying) Chat(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	return Unary(ctx, r.pol(opt), opt, "Chat", func(actx context.Context) (*llmv1.AssistantReply, error) {
		return r.inner.Chat(actx, messages, opt, tools)
	})
}

func (r *retrying) Generate(ctx context.Context, systemPrompt, prompt string, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	return Unary(ctx, r.pol(opt), opt, "Generate", func(actx context.Context) (*llmv1.AssistantReply, error) {
		return r.inner.Generate(actx, systemPrompt, prompt, opt, tools)
	})
}

func (r *retrying) GenerateParts(ctx context.Context, systemPrompt string, parts []*llmv1.PromptPart, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	return Unary(ctx, r.pol(opt), opt, "Generate", func(actx context.Context) (*llmv1.AssistantReply, error) {
		return backend.GenerateWithParts(actx, r.inner, systemPrompt, parts, opt, tools)
	})
}

func (r *retrying) ChatStream(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	return Stream(ctx, r.pol(opt), opt, "ChatStream", emit, func(actx context.Context, wrapped func(*llmv1.ConversationStreamFrame) error) error {
		return r.inner.ChatStream(actx, messages, opt, tools, wrapped)
	})
}
