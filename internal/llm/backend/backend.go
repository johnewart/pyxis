package backend

import (
	"context"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// Backend is the LLM surface exposed over gRPC (implemented by provider clients).
type Backend interface {
	Chat(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error)
	Generate(ctx context.Context, systemPrompt, prompt string, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error)
	// tools is consulted when the outbound streaming request is assembled so tools
	// registered concurrently (after the RPC handler starts) are still visible.
	ChatStream(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error
}
