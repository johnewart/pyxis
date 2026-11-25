package common

import (
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

const DefaultMaxTokens = 8192

// PickModel returns opt model when set, otherwise fallback.
func PickModel(opt *llmv1.CallOptions, fallback string) string {
	if opt != nil {
		if m := strings.TrimSpace(opt.GetModel()); m != "" {
			return m
		}
	}
	return strings.TrimSpace(fallback)
}

// PickMaxTokens returns positive max tokens from opt or fallback.
func PickMaxTokens(opt *llmv1.CallOptions, fallback int) int {
	if opt != nil && opt.GetMaxTokens() > 0 {
		return int(opt.GetMaxTokens())
	}
	if fallback > 0 {
		return fallback
	}
	return DefaultMaxTokens
}

// PickTemperature returns (value, ok) from opt.
func PickTemperature(opt *llmv1.CallOptions) (float32, bool) {
	if opt != nil && opt.Temperature != nil {
		return float32(*opt.Temperature), true
	}
	return 0, false
}

// PickTopP returns (value, ok) from opt.
func PickTopP(opt *llmv1.CallOptions) (float32, bool) {
	if opt != nil && opt.TopP != nil {
		return float32(*opt.TopP), true
	}
	return 0, false
}
