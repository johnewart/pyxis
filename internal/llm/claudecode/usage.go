package claudecodeclillm

import (
	"encoding/json"
	"math"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/protobuf/proto"
)

func int64FromAny(values ...any) int64 {
	for _, v := range values {
		switch n := v.(type) {
		case float64:
			if n > 0 {
				return int64(n)
			}
		case int64:
			if n > 0 {
				return n
			}
		case int:
			if n > 0 {
				return int64(n)
			}
		case json.Number:
			if i, err := n.Int64(); err == nil && i > 0 {
				return i
			}
		}
	}
	return 0
}

func float64FromAny(values ...any) *float64 {
	for _, v := range values {
		switch n := v.(type) {
		case float64:
			if !math.IsNaN(n) && n >= 0 {
				return proto.Float64(n)
			}
		case int64:
			if n >= 0 {
				f := float64(n)
				return &f
			}
		case int:
			if n >= 0 {
				f := float64(n)
				return &f
			}
		case json.Number:
			if f, err := n.Float64(); err == nil && !math.IsNaN(f) && f >= 0 {
				return proto.Float64(f)
			}
		}
	}
	return nil
}

func tokenUsageFromClaudeResultMap(raw map[string]any) *llmv1.TokenUsage {
	if raw == nil {
		return nil
	}
	usageObj, _ := raw["usage"].(map[string]any)
	if usageObj == nil {
		if usageObj, _ = raw["Usage"].(map[string]any); usageObj == nil {
			return nil
		}
	}
	inTok := int64FromAny(
		usageObj["input_tokens"], usageObj["inputTokens"],
	)
	outTok := int64FromAny(
		usageObj["output_tokens"], usageObj["outputTokens"],
	)
	cacheRead := int64FromAny(
		usageObj["cache_read_input_tokens"], usageObj["cacheReadInputTokens"],
		usageObj["cache_read_tokens"], usageObj["cacheReadTokens"],
	)
	cacheWrite := int64FromAny(
		usageObj["cache_creation_input_tokens"], usageObj["cacheCreationInputTokens"],
		usageObj["cache_write_tokens"], usageObj["cacheWriteTokens"],
	)
	cost := float64FromAny(raw["total_cost_usd"], raw["totalCostUsd"])
	if inTok == 0 && outTok == 0 && cacheRead == 0 && cacheWrite == 0 && cost == nil {
		return nil
	}
	u := &llmv1.TokenUsage{}
	if inTok > 0 {
		u.InputTokens = proto.Int64(inTok)
	}
	if outTok > 0 {
		u.OutputTokens = proto.Int64(outTok)
	}
	if cacheRead > 0 {
		u.CacheReadTokens = proto.Int64(cacheRead)
	}
	if cacheWrite > 0 {
		u.CacheWriteTokens = proto.Int64(cacheWrite)
	}
	if cost != nil {
		u.TotalCostUsd = cost
	}
	return u
}

func tokenUsageFromClaudeResultLine(line []byte) *llmv1.TokenUsage {
	var raw map[string]any
	if json.Unmarshal(line, &raw) != nil {
		return nil
	}
	return tokenUsageFromClaudeResultMap(raw)
}

// extractStreamEventInputUsage reads per-API-call input token counts from a
// Claude Code stream_event that wraps an Anthropic "message_start" SSE event.
// Each time Claude Code starts a new internal Anthropic API call it emits one
// of these; the usage here reflects only that single call's context window,
// unlike the cumulative result-line usage which sums all calls in the run.
// Returns nil when the event is not a message_start or carries no usage.
func extractStreamEventInputUsage(obj map[string]any) *llmv1.TokenUsage {
	ev, _ := obj["event"].(map[string]any)
	if ev == nil {
		return nil
	}
	if t, _ := ev["type"].(string); t != "message_start" {
		return nil
	}
	msg, _ := ev["message"].(map[string]any)
	if msg == nil {
		return nil
	}
	usageObj, _ := msg["usage"].(map[string]any)
	if usageObj == nil {
		return nil
	}
	inTok := int64FromAny(usageObj["input_tokens"], usageObj["inputTokens"])
	cacheRead := int64FromAny(
		usageObj["cache_read_input_tokens"], usageObj["cacheReadInputTokens"],
		usageObj["cache_read_tokens"], usageObj["cacheReadTokens"],
	)
	cacheWrite := int64FromAny(
		usageObj["cache_creation_input_tokens"], usageObj["cacheCreationInputTokens"],
		usageObj["cache_write_tokens"], usageObj["cacheWriteTokens"],
	)
	if inTok == 0 && cacheRead == 0 && cacheWrite == 0 {
		return nil
	}
	u := &llmv1.TokenUsage{}
	if inTok > 0 {
		u.InputTokens = proto.Int64(inTok)
	}
	if cacheRead > 0 {
		u.CacheReadTokens = proto.Int64(cacheRead)
	}
	if cacheWrite > 0 {
		u.CacheWriteTokens = proto.Int64(cacheWrite)
	}
	return u
}

// extractStreamEventOutputTokens reads the output token count from a Claude
// Code stream_event wrapping an Anthropic "message_delta" SSE event (emitted
// at the end of each internal API call). Returns 0 when not applicable.
func extractStreamEventOutputTokens(obj map[string]any) int64 {
	ev, _ := obj["event"].(map[string]any)
	if ev == nil {
		return 0
	}
	if t, _ := ev["type"].(string); t != "message_delta" {
		return 0
	}
	usageObj, _ := ev["usage"].(map[string]any)
	if usageObj == nil {
		return 0
	}
	return int64FromAny(usageObj["output_tokens"], usageObj["outputTokens"])
}
