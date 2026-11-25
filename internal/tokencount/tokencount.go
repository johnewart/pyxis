// Package tokencount provides rough local token counting for the tool-result compression delta.
// It is NOT used for provider-actual counts (those come from EvAssistantTurnFinished.Usage).
// Uses character-based approximation (len/4) as a close estimate for token counting.
// This is sufficient for compression delta calculation and avoids heavy tokenizer dependencies.
package tokencount

// Count returns the approximate token count of s using character-based estimation.
// Formula: roughly 4 characters per token (BPE average for English text).
// Falls back to character count / 4 on any error.
// This is only used for calculating compression delta, not for provider-actual accounting.
func Count(s string) int64 {
	if s == "" {
		return 0
	}
	// Rough approximation: English text is ~4 chars per token on average.
	// This works well enough for compression delta calculation.
	charCount := int64(len(s))
	return (charCount + 3) / 4 // Round up division
}
