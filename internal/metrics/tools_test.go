package metrics

import (
	"testing"

	"github.com/johnewart/pyxis/toolseam"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestTools_countsTimesAndTracksInflight(t *testing.T) {
	m := New()
	tools := NewTools(m)

	done := tools.Begin("read_code", toolseam.OriginMCP, toolseam.BindingInline)
	if got := testutil.ToFloat64(tools.inflight.WithLabelValues("read_code", "mcp")); got != 1 {
		t.Fatalf("inflight during call = %v, want 1", got)
	}
	done(toolseam.InvocationResult{Outcome: toolseam.OutcomeOK, ResultBytes: 100})

	if got := testutil.ToFloat64(tools.inflight.WithLabelValues("read_code", "mcp")); got != 0 {
		t.Errorf("inflight after call = %v, want 0", got)
	}
	if got := testutil.ToFloat64(tools.invocations.WithLabelValues("read_code", "mcp", "inline", "ok")); got != 1 {
		t.Errorf("invocations_total = %v, want 1", got)
	}
	if n := testutil.CollectAndCount(tools.duration); n != 1 {
		t.Errorf("duration series = %d, want 1", n)
	}
	if n := testutil.CollectAndCount(tools.resultBytes); n != 1 {
		t.Errorf("result_bytes series = %d, want 1", n)
	}
}

// The model-supplied name of an unknown tool must never become a label.
func TestTools_unregisteredToolUsesFixedLabel(t *testing.T) {
	tools := NewTools(New())
	tools.Begin("", toolseam.OriginChatLoop, toolseam.BindingNone)(toolseam.InvocationResult{Outcome: toolseam.OutcomeNotRegistered})
	if got := testutil.ToFloat64(tools.invocations.WithLabelValues(unregisteredTool, "chat_loop", "none", "not_registered")); got != 1 {
		t.Fatalf("invocations_total{tool=%q} = %v, want 1", unregisteredTool, got)
	}
}
