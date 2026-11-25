package suffixwire

import (
	"strings"
	"testing"
)

func TestCompute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		emitted        string
		d              string
		wantNew        string
		wantWire       string
		wantDivergence bool
		wantRegression bool
	}{
		{
			name:    "empty_emitted_first_chunk",
			emitted: "",
			d:       "hello",
			wantNew: "hello", wantWire: "hello",
		},
		{
			name:    "incremental_suffix",
			emitted: "hello",
			d:       " world",
			wantNew: "hello world", wantWire: " world",
		},
		{
			name:    "cumulative_snapshot_after_increments",
			emitted: "hello world",
			d:       "hello world",
			wantNew: "hello world", wantWire: "",
		},
		{
			name:    "cumulative_snapshot_extends_prefix",
			emitted: "hello",
			d:       "hello world",
			wantNew: "hello world", wantWire: " world",
		},
		{
			name:    "order_a_assistant_then_text_duplicate",
			emitted: "hello world",
			d:       "hello world",
			wantNew: "hello world", wantWire: "",
		},
		{
			name:    "repeated_assistant_growth",
			emitted: "a",
			d:       "ab",
			wantNew: "ab", wantWire: "b",
		},
		{
			name:    "repeated_assistant_growth_second_step",
			emitted: "ab",
			d:       "abc",
			wantNew: "abc", wantWire: "c",
		},
		{
			name:           "regression_shorter_prefix",
			emitted:        "hello world",
			d:              "hello",
			wantNew:        "hello world",
			wantWire:       "",
			wantRegression: true,
		},
		{
			name:    "identical_repeat",
			emitted: "same",
			d:       "same",
			wantNew: "same", wantWire: "",
		},
		{
			name:    "repeated_sentence_already_in_emitted",
			emitted: "I'll locate the symbol - BacktestPerformanceMetrics already exists in core - I'll read it next.",
			d:       "BacktestPerformanceMetrics already exists in core - I'll read it next.",
			wantNew: "I'll locate the symbol - BacktestPerformanceMetrics already exists in core - I'll read it next.",
			wantWire: "",
		},
		{
			name:    "repeated_trailing_suffix",
			emitted: "Hello world",
			d:       " world",
			wantNew: "Hello world", wantWire: "",
		},
		{
			name: "repeated_clause_with_trailing_period",
			emitted: "Well, it didn't fix it. Core already tracks runtime via `BacktestRuntimeReport`; next I'll see what trading performance metrics exist and what's missing",
			d:       "Core already tracks runtime via `BacktestRuntimeReport`; next I'll see what trading performance metrics exist and what's missing.",
			wantNew: "Well, it didn't fix it. Core already tracks runtime via `BacktestRuntimeReport`; next I'll see what trading performance metrics exist and what's missing",
			wantWire: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotNew, gotWire, gotDiv, gotReg := Compute(tt.emitted, tt.d)
			if gotNew != tt.wantNew || gotWire != tt.wantWire || gotDiv != tt.wantDivergence || gotReg != tt.wantRegression {
				t.Fatalf("Compute(%q,%q) = (%q,%q,div=%v,reg=%v) want (%q,%q,div=%v,reg=%v)",
					tt.emitted, tt.d, gotNew, gotWire, gotDiv, gotReg,
					tt.wantNew, tt.wantWire, tt.wantDivergence, tt.wantRegression)
			}
		})
	}
}

func TestSimulateInterleavedVisibleStream(t *testing.T) {
	t.Parallel()

	emitted := ""
	var wire []string
	feed := func(d string) {
		ne, w, div, reg := Compute(emitted, d)
		if div || reg {
			t.Fatalf("unexpected div=%v reg=%v at d=%q emitted=%q", div, reg, d, emitted)
		}
		emitted = ne
		if w != "" {
			wire = append(wire, w)
		}
	}

	feed("hello world")
	feed("hello world")
	feed("!")
	feed("hello world!")

	got := strings.Join(wire, "")
	if got != "hello world!" {
		t.Fatalf("wire=%q joined=%q want hello world!", wire, got)
	}
	if emitted != "hello world!" {
		t.Fatalf("emitted=%q want hello world!", emitted)
	}
}

func TestSimulateRepeatedIncrementalSentence(t *testing.T) {
	t.Parallel()

	emitted := "I'll locate the symbol - BacktestPerformanceMetrics already exists in core - I'll read it next."
	d := "BacktestPerformanceMetrics already exists in core - I'll read it next."
	gotNew, gotWire, gotDiv, gotReg := Compute(emitted, d)
	if gotNew != emitted || gotWire != "" || gotDiv || gotReg {
		t.Fatalf("Compute() = (%q,%q,div=%v,reg=%v) want (%q,\"\",false,false)",
			gotNew, gotWire, gotDiv, gotReg, emitted)
	}
}

func TestStateIdenticalRepeatEmitsNothing(t *testing.T) {
	t.Parallel()
	var s State
	w1, div1, _ := s.WireDelta("same")
	if div1 || w1 != "same" || s.Emitted != "same" {
		t.Fatalf("first: w=%q div=%v emitted=%q", w1, div1, s.Emitted)
	}
	w2, div2, _ := s.WireDelta("same")
	if div2 || w2 != "" || s.Emitted != "same" {
		t.Fatalf("second: w=%q div=%v emitted=%q", w2, div2, s.Emitted)
	}
}
