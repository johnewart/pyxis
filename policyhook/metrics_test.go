package policyhook

import (
	"context"
	"errors"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func findMetric(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			matched := 0
			for k, v := range labels {
				for _, lp := range m.GetLabel() {
					if lp.GetName() == k && lp.GetValue() == v {
						matched++
					}
				}
			}
			if matched == len(labels) {
				return m
			}
		}
	}
	return nil
}

func TestMetricsRecorded(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	h := Funcs{
		CheckLLMCallFn: func(context.Context, LLMCallRequest) (Verdict, error) {
			return Verdict{Decision: DecisionDeny}, nil
		},
		CheckToolCallFn: func(context.Context, ToolCallRequest) (Verdict, error) {
			return Verdict{}, errors.New("down")
		},
		FilterToolsFn: func(context.Context, FilterToolsRequest) (FilterResult, error) {
			return FilterResult{AllowedToolNames: []string{"a"}}, nil
		},
	}
	g := NewGuard(h, FailClosed, m, nil)
	ctx := context.Background()

	if _, err := g.CheckLLMCall(ctx, LLMCallRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CheckToolCall(ctx, ToolCallRequest{}); err != nil {
		t.Fatal(err)
	}
	fr, err := g.FilterTools(ctx, FilterToolsRequest{Tools: []*llmv1.ToolDefinition{{Name: "a"}, {Name: "b"}, {Name: "c"}}})
	if err != nil || len(fr.AllowedToolNames) != 1 {
		t.Fatalf("FilterTools = %+v, %v", fr, err)
	}

	cases := []map[string]string{
		{"hook": "check_llm_call", "decision": "deny", "outcome": "ok"},
		{"hook": "check_tool_call", "decision": "deny", "outcome": "error"},
		{"hook": "filter_tools", "decision": "allow", "outcome": "ok"},
	}
	for _, labels := range cases {
		c := findMetric(t, reg, "llmd_policy_hook_calls_total", labels)
		if c == nil || c.GetCounter().GetValue() != 1 {
			t.Fatalf("calls_total%v = %v", labels, c)
		}
	}

	for _, hook := range []string{"check_llm_call", "check_tool_call", "filter_tools"} {
		hm := findMetric(t, reg, "llmd_policy_hook_duration_seconds", map[string]string{"hook": hook})
		if hm == nil || hm.GetHistogram().GetSampleCount() != 1 {
			t.Fatalf("duration histogram for %s = %v", hook, hm)
		}
	}

	tf := findMetric(t, reg, "llmd_policy_tools_filtered_total", nil)
	if tf == nil || tf.GetCounter().GetValue() != 2 {
		t.Fatalf("tools_filtered = %v, want 2", tf)
	}
}
