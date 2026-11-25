package claudecodeclillm

import (
	"context"
	"strings"

	"github.com/johnewart/pyxis/internal/llm/common/memctx"
)

func mergedAppendSystemPrompt(ctx context.Context, cfgAppend string) string {
	var parts []string
	if ap := strings.TrimSpace(cfgAppend); ap != "" {
		parts = append(parts, ap)
	}
	if rendered := strings.TrimSpace(memctx.RenderedClaudeAppendFromContext(ctx)); rendered != "" {
		parts = append(parts, rendered)
	}
	return strings.Join(parts, "\n\n")
}
