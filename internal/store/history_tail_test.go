package store

import (
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
	"google.golang.org/protobuf/proto"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// TestHistory_tailWindowMatchesFullHistory proves the SQL tail window returns
// exactly the last N messages of the full history for every N — including
// windows that cut through the middle of a multi-row assistant turn (thinking +
// activity + content + tool calls), and sessions padded with rows that never
// become messages (captured provider-internal rows).
func TestHistory_tailWindowMatchesFullHistory(t *testing.T) {
	db := NewTestSQLite(t)
	const sid = "sess-tail"
	h := NewHistory(db, serverutil.Scope{TenantID: "ws-1"}, sid)

	for i := 0; i < 6; i++ {
		h.Store(sid, &llmv1.Message{Role: "user", Id: "u" + string(rune('a'+i)), Content: "question"})
		h.Store(sid, &llmv1.Message{
			Role:             "assistant",
			Id:               "a" + string(rune('a'+i)),
			ThinkingContent:  "thinking",
			ActivityHeadline: "doing",
			Content:          "answer",
			ToolCalls:        []*llmv1.ToolCall{{Id: "c" + string(rune('a'+i)), Name: "t", ArgumentsJson: "{}"}},
		})
		h.Store(sid, &llmv1.Message{Role: "tool", Id: "r" + string(rune('a'+i)), ToolCallId: "c" + string(rune('a'+i)), Content: "result"})
		// Rows that never become history messages still occupy the window.
		h.StoreCaptured(sid, 0, []*llmv1.Message{
			{Role: "assistant", Id: "x" + string(rune('a'+i)), Content: "inner"},
			{Role: "tool", Id: "y" + string(rune('a'+i)), Content: "inner"},
		})
	}

	full := h.History(sid, 0)
	if len(full) != 18 {
		t.Fatalf("full history: want 18 messages, got %d", len(full))
	}
	for n := 1; n <= len(full)+3; n++ {
		got := h.History(sid, n)
		want := full
		if n < len(full) {
			want = full[len(full)-n:]
		}
		if len(got) != len(want) {
			t.Fatalf("tail %d: want %d messages, got %d", n, len(want), len(got))
		}
		for i := range want {
			if !proto.Equal(got[i], want[i]) {
				t.Fatalf("tail %d: message %d differs:\n got  %v\n want %v", n, i, got[i], want[i])
			}
		}
	}
}
