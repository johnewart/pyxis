package anthropicllm

import (
	"github.com/anthropics/anthropic-sdk-go"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// attachAnthropicPromptCache maps llmv1.CacheOptions onto the Anthropic Messages API
// (prompt cache breakpoints on system text blocks). Other providers should read
// CallOptions.GetCacheOptions() and apply their own semantics.
func attachAnthropicPromptCacheToTextBlock(block *anthropic.TextBlockParam, opt *llmv1.CallOptions) {
	if block == nil || opt == nil || opt.GetCacheOptions() == nil {
		return
	}
	switch opt.GetCacheOptions().GetPromptCacheMode() {
	case llmv1.CacheOptions_PROMPT_CACHE_MODE_SHORT_TTL:
		cc := anthropic.NewCacheControlEphemeralParam()
		cc.TTL = anthropic.CacheControlEphemeralTTLTTL5m
		block.CacheControl = cc
	case llmv1.CacheOptions_PROMPT_CACHE_MODE_LONG_TTL:
		cc := anthropic.NewCacheControlEphemeralParam()
		cc.TTL = anthropic.CacheControlEphemeralTTLTTL1h
		block.CacheControl = cc
	case llmv1.CacheOptions_PROMPT_CACHE_MODE_UNSPECIFIED,
		llmv1.CacheOptions_PROMPT_CACHE_MODE_DISABLED:
		// No cache_control markers.
	default:
	}
}
