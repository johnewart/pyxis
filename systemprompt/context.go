package systemprompt

import (
	"context"

	"github.com/johnewart/pyxis/internal/llm/common/memctx"
)

type ctxKey struct{}

// WithRenderedMDCFiles attaches rendered MDC files for Cursor workspace setup.
func WithRenderedMDCFiles(ctx context.Context, files map[string]string) context.Context {
	if len(files) == 0 {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, files)
}

// RenderedMDCFilesFromContext returns MDC files set by the gRPC server for this ChatStream.
func RenderedMDCFilesFromContext(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}
	v, ok := ctx.Value(ctxKey{}).(map[string]string)
	if !ok || len(v) == 0 {
		return nil
	}
	return v
}

// WithRenderedClaudeAppend attaches Claude Code --append-system-prompt text for this ChatStream.
func WithRenderedClaudeAppend(ctx context.Context, appendText string) context.Context {
	return memctx.WithRenderedClaudeAppend(ctx, appendText)
}

// RenderedClaudeAppendFromContext returns append text set by the gRPC server for this ChatStream.
func RenderedClaudeAppendFromContext(ctx context.Context) string {
	return memctx.RenderedClaudeAppendFromContext(ctx)
}
