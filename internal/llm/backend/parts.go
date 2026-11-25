package backend

import (
	"context"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// PartsGenerator is an optional interface for backends that can use prompt
// parts (and their cache breakpoints) directly.
type PartsGenerator interface {
	GenerateParts(ctx context.Context, systemPrompt string, parts []*llmv1.PromptPart, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error)
}

// JoinPromptParts joins the non-empty parts' text with blank lines.
func JoinPromptParts(parts []*llmv1.PromptPart) string {
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == nil || p.GetText() == "" {
			continue
		}
		texts = append(texts, p.GetText())
	}
	return strings.Join(texts, "\n\n")
}

// GenerateWithParts calls be.GenerateParts when supported, otherwise Generate
// with the parts joined by blank lines.
func GenerateWithParts(ctx context.Context, be Backend, systemPrompt string, parts []*llmv1.PromptPart, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	if pg, ok := be.(PartsGenerator); ok {
		return pg.GenerateParts(ctx, systemPrompt, parts, opt, tools)
	}
	return be.Generate(ctx, systemPrompt, JoinPromptParts(parts), opt, tools)
}
