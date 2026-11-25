package policyhook

import (
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func msgs() []*llmv1.Message {
	return []*llmv1.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "tool", Content: "t1", ToolCallId: "c1"},
		{Role: "user", Content: "u2"},
	}
}

func contents(in []*llmv1.Message) string {
	s := ""
	for _, m := range in {
		s += m.GetContent() + ","
	}
	return s
}

func TestSelectMessages(t *testing.T) {
	tests := []struct {
		name string
		p    CheckPolicy
		want string
	}{
		{"full with all", CheckPolicy{Content: ContentFull, IncludeSystemPrompt: true, IncludeToolResults: true}, "sys,u1,a1,t1,u2,"},
		{"full no system", CheckPolicy{Content: ContentFull, IncludeToolResults: true}, "u1,a1,t1,u2,"},
		{"full no tool results", CheckPolicy{Content: ContentFull, IncludeSystemPrompt: true}, "sys,u1,a1,u2,"},
		{"latest", CheckPolicy{Content: ContentLatest}, "u2,"},
		{"latest with system", CheckPolicy{Content: ContentLatest, IncludeSystemPrompt: true}, "sys,u2,"},
		{"latest_n 2 no tool results", CheckPolicy{Content: ContentLatestN, LatestN: 2}, "a1,u2,"},
		{"latest_n 2 with tool results", CheckPolicy{Content: ContentLatestN, LatestN: 2, IncludeToolResults: true}, "t1,u2,"},
		{"latest_n larger than history", CheckPolicy{Content: ContentLatestN, LatestN: 50, IncludeSystemPrompt: true, IncludeToolResults: true}, "sys,u1,a1,t1,u2,"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := contents(tc.p.SelectMessages(msgs()))
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestShouldCheck(t *testing.T) {
	tests := []struct {
		name  string
		p     CheckPolicy
		round uint32
		want  bool
	}{
		{"every round 0", CheckPolicy{Cadence: CadenceEveryRound}, 0, true},
		{"every round 5", CheckPolicy{Cadence: CadenceEveryRound}, 5, true},
		{"first round 0", CheckPolicy{Cadence: CadenceFirstRound}, 0, true},
		{"first round 1", CheckPolicy{Cadence: CadenceFirstRound}, 1, false},
		{"every 3 round 0", CheckPolicy{Cadence: CadenceEveryNRounds, EveryNRounds: 3}, 0, true},
		{"every 3 round 1", CheckPolicy{Cadence: CadenceEveryNRounds, EveryNRounds: 3}, 1, false},
		{"every 3 round 3", CheckPolicy{Cadence: CadenceEveryNRounds, EveryNRounds: 3}, 3, true},
		{"every 3 round 4", CheckPolicy{Cadence: CadenceEveryNRounds, EveryNRounds: 3}, 4, false},
		{"every 0 treated as every round", CheckPolicy{Cadence: CadenceEveryNRounds}, 2, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.ShouldCheck(tc.round); got != tc.want {
				t.Fatalf("round %d: got %v want %v", tc.round, got, tc.want)
			}
		})
	}
}

func TestDefaultCheckPolicy(t *testing.T) {
	var p *CheckPolicy = DefaultCheckPolicy()
	if p.Content != ContentLatest {
		t.Fatalf("content = %v, want LATEST", p.Content)
	}
	if p.Cadence != CadenceEveryRound {
		t.Fatalf("cadence = %v, want EVERY_ROUND", p.Cadence)
	}
	// Each call returns an independent copy.
	p.LatestN = 9
	if DefaultCheckPolicy().LatestN == 9 {
		t.Fatal("DefaultCheckPolicy returned shared state")
	}
}

func TestResolveOverride(t *testing.T) {
	operator := &CheckPolicy{Content: ContentFull, Cadence: CadenceFirstRound}
	hook := &CheckPolicy{Content: ContentLatestN, LatestN: 4, Cadence: CadenceEveryNRounds, EveryNRounds: 2}
	resp := &CheckPolicy{Content: ContentLatest, Cadence: CadenceEveryRound}

	tests := []struct {
		name                     string
		operator, hook, response *CheckPolicy
		wantContent              ContentScope
		wantCadence              Cadence
	}{
		{"nothing set uses built-in default", nil, nil, nil, ContentLatest, CadenceEveryRound},
		{"operator default replaces built-in", operator, nil, nil, ContentFull, CadenceFirstRound},
		{"hook overrides operator", operator, hook, nil, ContentLatestN, CadenceEveryNRounds},
		{"response overrides hook", operator, hook, resp, ContentLatest, CadenceEveryRound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(tc.operator, tc.hook, tc.response)
			if got == nil || got.Content != tc.wantContent || got.Cadence != tc.wantCadence {
				t.Fatalf("got %+v want %v/%v", got, tc.wantContent, tc.wantCadence)
			}
		})
	}
}
