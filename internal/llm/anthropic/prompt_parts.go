package anthropicllm

import (
	"github.com/anthropics/anthropic-sdk-go"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdlog"
)

// maxCacheMarkers is Anthropic's limit on cache_control markers per request.
const maxCacheMarkers = 4

// buildUserBlocks makes one user text block per part, marking breakpoint parts
// with cache_control (when caching is enabled), keeping at most
// maxCacheMarkers of the last markers.
func buildUserBlocks(parts []*llmv1.PromptPart, opt *llmv1.CallOptions) []anthropic.ContentBlockParamUnion {
	return buildUserBlocksLimit(parts, opt, maxCacheMarkers)
}

func buildUserBlocksLimit(parts []*llmv1.PromptPart, opt *llmv1.CallOptions, limit int) []anthropic.ContentBlockParamUnion {
	if limit < 0 {
		limit = 0
	}
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(parts))
	var marked []int // indexes into blocks that carry a marker
	for _, p := range parts {
		if p == nil || p.GetText() == "" {
			continue
		}
		tb := anthropic.TextBlockParam{Text: p.GetText()}
		if p.GetCacheBreakpoint() {
			attachAnthropicPromptCacheToTextBlock(&tb, opt)
		}
		if tb.CacheControl.Type != "" {
			marked = append(marked, len(blocks))
		}
		blocks = append(blocks, anthropic.ContentBlockParamUnion{OfText: &tb})
	}
	if drop := len(marked) - limit; drop > 0 {
		llmdlog.Stdout("anthropicllm", llmdlog.LevelDebug, "dropping %d earlier cache breakpoints (limit %d)", drop, limit)
		for _, i := range marked[:drop] {
			blocks[i].OfText.CacheControl = anthropic.CacheControlEphemeralParam{}
		}
	}
	return blocks
}
