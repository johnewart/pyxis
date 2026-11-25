package store

import (
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
)

// A stored tool result must come back paired with the call it answers, through
// both Store and StoreInTurn. It used to be written but dropped on read.
func TestHistory_toolResultKeepsToolCallID(t *testing.T) {
	const sid = "sess-tcid"
	h := NewHistory(NewTestSQLite(t), serverutil.Scope{TenantID: "ws-tcid"}, sid)
	h.Store(sid, &llmv1.Message{Id: "a1", Role: "assistant", ToolCalls: []*llmv1.ToolCall{{Id: "call_1", Name: "x", ArgumentsJson: `{}`}}})
	h.Store(sid, &llmv1.Message{Id: "t1", Role: "tool", ToolCallId: "call_1", Content: "r1"})
	h.StoreInTurn(sid, 2, &llmv1.Message{Id: "t2", Role: "tool", ToolCallId: "call_2", Content: "r2"})

	want := map[string]string{"t1": "call_1", "t2": "call_2"}
	seen := 0
	for _, m := range h.History(sid, 0) {
		if m.GetRole() != "tool" {
			continue
		}
		seen++
		if got := m.GetToolCallId(); got != want[m.GetId()] {
			t.Errorf("tool %q: tool_call_id = %q, want %q", m.GetId(), got, want[m.GetId()])
		}
	}
	if seen != 2 {
		t.Fatalf("want 2 tool rows, got %d", seen)
	}
}
