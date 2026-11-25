package anthropicllm

import (
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestAttachAnthropicPromptCacheToTextBlock_shortTTL(t *testing.T) {
	opt := &llmv1.CallOptions{
		CacheOptions: &llmv1.CacheOptions{
			PromptCacheMode: llmv1.CacheOptions_PROMPT_CACHE_MODE_SHORT_TTL,
		},
	}
	b := anthropic.TextBlockParam{Text: "x"}
	attachAnthropicPromptCacheToTextBlock(&b, opt)
	if b.CacheControl.TTL != anthropic.CacheControlEphemeralTTLTTL5m {
		t.Fatalf("TTL=%v", b.CacheControl.TTL)
	}
}

func TestAttachAnthropicPromptCacheToTextBlock_longTTL(t *testing.T) {
	opt := &llmv1.CallOptions{
		CacheOptions: &llmv1.CacheOptions{
			PromptCacheMode: llmv1.CacheOptions_PROMPT_CACHE_MODE_LONG_TTL,
		},
	}
	b := anthropic.TextBlockParam{Text: "x"}
	attachAnthropicPromptCacheToTextBlock(&b, opt)
	if b.CacheControl.TTL != anthropic.CacheControlEphemeralTTLTTL1h {
		t.Fatalf("TTL=%v", b.CacheControl.TTL)
	}
}

func TestAttachAnthropicPromptCacheToTextBlock_disabledNoMarker(t *testing.T) {
	opt := &llmv1.CallOptions{
		CacheOptions: &llmv1.CacheOptions{
			PromptCacheMode: llmv1.CacheOptions_PROMPT_CACHE_MODE_DISABLED,
		},
	}
	b := anthropic.TextBlockParam{Text: "x"}
	attachAnthropicPromptCacheToTextBlock(&b, opt)
	raw, err := b.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "cache_control") {
		t.Fatalf("expected no cache_control, got %s", raw)
	}
}
