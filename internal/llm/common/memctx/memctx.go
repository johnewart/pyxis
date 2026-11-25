package memctx

import (
	"context"
	"strings"
)

type memoryHookKey struct{}

type claudeAppendKey struct{}

// WithAssistantMemoryHook attaches a callback invoked for each completed <memory>...</memory> block
// during assistant streaming (trimmed non-empty body). Used by LLM backends; hook may be nil.
func WithAssistantMemoryHook(ctx context.Context, hook func(text string)) context.Context {
	if hook == nil {
		return ctx
	}
	return context.WithValue(ctx, memoryHookKey{}, hook)
}

// AssistantMemoryHook returns the hook from ctx, or nil.
func AssistantMemoryHook(ctx context.Context) func(text string) {
	h, _ := ctx.Value(memoryHookKey{}).(func(string))
	return h
}

// WithRenderedClaudeAppend attaches Claude Code --append-system-prompt text for this ChatStream.
func WithRenderedClaudeAppend(ctx context.Context, appendText string) context.Context {
	if strings.TrimSpace(appendText) == "" {
		return ctx
	}
	return context.WithValue(ctx, claudeAppendKey{}, appendText)
}

// RenderedClaudeAppendFromContext returns append text set for this ChatStream.
func RenderedClaudeAppendFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, ok := ctx.Value(claudeAppendKey{}).(string)
	if !ok {
		return ""
	}
	return v
}
