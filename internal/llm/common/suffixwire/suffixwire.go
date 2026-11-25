package suffixwire

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Fingerprint returns a compact, log-safe fingerprint for provider text.
func Fingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("len=%d sha256_prefix=%s", len(s), hex.EncodeToString(sum[:4]))
}

const minRedundantRun = 12

func trimTrailingPunct(s string) string {
	return strings.TrimRight(s, ".,!?;:")
}

func deltaAlreadyInEmitted(emitted, d string) bool {
	if strings.Contains(emitted, d) {
		return true
	}
	trimmed := trimTrailingPunct(d)
	return len(trimmed) >= minRedundantRun && strings.Contains(emitted, trimmed)
}

// Compute turns post-pipeline *deltas* into strict suffix-only wire increments relative to emitted, so
// interleaved cumulative snapshots and incremental chunks cannot double-append the same visible answer.
//
// emitted is the llmd-visible transcript already committed on the wire (post assistanttext.VisiblePipeline
// for the answer channel, or post assistanttext.ReasoningPipeline for a native thinking channel).
// d is the next post-pipeline visible/thinking chunk produced for this turn.
//
// Policies:
//   - If d extends emitted as a prefix extension (d is a cumulative snapshot starting with emitted),
//     only the new suffix is returned as wire.
//   - If emitted already starts with d (provider regressed to a shorter prefix), emit nothing and keep
//     emitted unchanged (do not rewind user-visible text).
//   - If d is already contained in emitted (e.g. a repeated sentence snapshot after incremental text),
//     emit nothing and keep emitted unchanged.
//   - If d matches text already in emitted modulo trailing sentence punctuation (e.g. repeated
//     clause with an added "."), emit nothing and keep emitted unchanged.
//   - Otherwise treat d as an incremental continuation in arrival order: next = emitted + d.
//   - If next is not a strict prefix extension of emitted (unexpected divergence), return divergence=true.
//     Callers should stop streaming that channel for the remainder of the turn and rely on end-of-turn
//     assembly to avoid corrupting the UI.
func Compute(emitted, d string) (newEmitted, wire string, divergence, regression bool) {
	if d == "" {
		return emitted, "", false, false
	}

	var next string
	switch {
	case emitted == "":
		next = d
	case d == emitted:
		next = d
	case strings.HasPrefix(d, emitted):
		next = d
	case strings.HasPrefix(emitted, d):
		return emitted, "", false, true
	case deltaAlreadyInEmitted(emitted, d):
		return emitted, "", false, false
	default:
		next = emitted + d
	}

	if next == emitted {
		return emitted, "", false, false
	}
	if strings.HasPrefix(next, emitted) {
		return next, next[len(emitted):], false, false
	}
	return emitted, "", true, false
}

// State tracks emitted transcript for one stream channel (visible answer or thinking).
type State struct {
	Emitted string
	Halted  bool
}

// WireDelta returns the suffix-only wire increment for chunk d (post-pipeline text).
// On divergence, Halted is set true and callers should stop emitting on this channel.
//
// WireDelta updates Emitted immediately after Compute (before any separate commit step).
// Streaming backends that must only advance their local transcript after a successful emit
// should call Compute directly instead.
func (s *State) WireDelta(d string) (wire string, divergence, regression bool) {
	if s.Halted || d == "" {
		return "", false, false
	}
	ne, w, div, reg := Compute(s.Emitted, d)
	if div {
		s.Halted = true
		return "", true, reg
	}
	s.Emitted = ne
	return w, false, reg
}
