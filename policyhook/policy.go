// (Package documentation lives in hooks.go.)
package policyhook

import (
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// ContentScope says how much of the conversation a hook sees.
type ContentScope int

const (
	ContentFull ContentScope = iota
	ContentLatest
	ContentLatestN
)

// Cadence says on which rounds a hook is called.
type Cadence int

const (
	CadenceEveryRound Cadence = iota
	CadenceFirstRound
	CadenceEveryNRounds
)

// CheckPolicy says which content a hook sees and on which rounds it is called.
type CheckPolicy struct {
	Content             ContentScope
	LatestN             uint32
	Cadence             Cadence
	EveryNRounds        uint32
	IncludeSystemPrompt bool
	IncludeToolResults  bool
}

// DefaultCheckPolicy returns the built-in default: LATEST + EVERY_ROUND.
// Each call returns an independent copy.
func DefaultCheckPolicy() *CheckPolicy {
	return &CheckPolicy{Content: ContentLatest, Cadence: CadenceEveryRound}
}

// Resolve picks the effective policy: response override, else the hook's own
// policy, else the operator default, else the built-in default. The result is
// always a copy.
func Resolve(operator, hook, response *CheckPolicy) *CheckPolicy {
	for _, p := range []*CheckPolicy{response, hook, operator} {
		if p != nil {
			c := *p
			return &c
		}
	}
	return DefaultCheckPolicy()
}

// ShouldCheck reports whether the hook runs on the given zero-based round.
func (p CheckPolicy) ShouldCheck(round uint32) bool {
	switch p.Cadence {
	case CadenceFirstRound:
		return round == 0
	case CadenceEveryNRounds:
		if p.EveryNRounds == 0 {
			return true
		}
		return round%p.EveryNRounds == 0
	default:
		return true
	}
}

// SelectMessages returns the messages the policy lets the hook see.
func (p CheckPolicy) SelectMessages(in []*llmv1.Message) []*llmv1.Message {
	var system, rest []*llmv1.Message
	for _, m := range in {
		switch m.GetRole() {
		case "system":
			if p.IncludeSystemPrompt {
				system = append(system, m)
			}
		case "tool":
			if p.IncludeToolResults {
				rest = append(rest, m)
			}
		default:
			rest = append(rest, m)
		}
	}
	var keep int
	switch p.Content {
	case ContentLatest:
		keep = 1
	case ContentLatestN:
		keep = int(p.LatestN)
	default:
		keep = len(rest)
	}
	if keep < len(rest) {
		rest = rest[len(rest)-keep:]
	}
	return append(system, rest...)
}

func policyFromProto(p *llmv1.CheckPolicy) *CheckPolicy {
	if p == nil {
		return nil
	}
	return &CheckPolicy{
		Content:             ContentScope(p.GetContent()),
		LatestN:             p.GetLatestN(),
		Cadence:             Cadence(p.GetCadence()),
		EveryNRounds:        p.GetEveryNRounds(),
		IncludeSystemPrompt: p.GetIncludeSystemPrompt(),
		IncludeToolResults:  p.GetIncludeToolResults(),
	}
}
