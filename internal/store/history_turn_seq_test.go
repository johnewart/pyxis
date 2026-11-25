package store

import (
	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// TestTurnSeq_ChatMessageRowsCarryTurnSeq proves History.StoreInTurn stamps the
// canonical turn_seq onto every persisted row of a turn — user, assistant, and
// tool result alike — so a tool result is attributable to the turn that issued
// it, while plain Store leaves turn_seq NULL.
func TestTurnSeq_ChatMessageRowsCarryTurnSeq(t *testing.T) {
	db := NewTestSQLite(t)
	h := NewHistory(db, serverutil.Scope{TenantID: "ws-1"}, "sess-1")
	const sessionID = "sess-1"

	// One turn (seq 7): a user message, an assistant reply with a tool call, and
	// the resulting tool-role result all persisted under the same turn_seq.
	h.StoreInTurn(sessionID, 7, &llmv1.Message{Role: "user", Content: "hi", Id: "u1"})
	h.StoreInTurn(sessionID, 7, &llmv1.Message{
		Role:    "assistant",
		Id:      "a1",
		Content: "on it",
		ToolCalls: []*llmv1.ToolCall{
			{Id: "call_1", Name: "read_code", ArgumentsJson: "{}"},
		},
	})
	h.StoreInTurn(sessionID, 7, &llmv1.Message{
		Role:       "tool",
		Id:         "t1",
		ToolCallId: "call_1",
		Content:    "file contents",
	})

	var rows []ChatMessageRow
	if err := db.Where("session_id = ?", sessionID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query rows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no chat rows persisted")
	}

	var sawTool bool
	for _, r := range rows {
		if r.TurnSeq == nil {
			t.Fatalf("row kind=%q role=%q missing turn_seq", r.Kind, r.Role)
		}
		if *r.TurnSeq != 7 {
			t.Fatalf("row kind=%q: want turn_seq=7, got %d", r.Kind, *r.TurnSeq)
		}
		if r.Kind == ChatMsgKindToolResult {
			sawTool = true
			if r.ToolCallID != "call_1" {
				t.Fatalf("tool result row lost tool_call_id: %q", r.ToolCallID)
			}
		}
	}
	if !sawTool {
		t.Fatal("expected a tool_result row carrying turn_seq")
	}

	// History reconstruction still works with the added column present.
	if msgs := h.History(sessionID, 0); len(msgs) == 0 {
		t.Fatal("History() returned nothing after StoreInTurn")
	}
}

// TestTurnSeq_StoreLeavesTurnSeqNull proves the plain Store path (no known turn,
// e.g. bulk history replace) persists rows with turn_seq NULL rather than 0.
func TestTurnSeq_StoreLeavesTurnSeqNull(t *testing.T) {
	db := NewTestSQLite(t)
	h := NewHistory(db, serverutil.Scope{TenantID: "ws-2"}, "sess-2")

	h.Store("sess-2", &llmv1.Message{Role: "user", Content: "hi", Id: "u1"})

	var rows []ChatMessageRow
	if err := db.Where("session_id = ?", "sess-2").Find(&rows).Error; err != nil {
		t.Fatalf("query rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].TurnSeq != nil {
		t.Fatalf("plain Store must leave turn_seq NULL, got %d", *rows[0].TurnSeq)
	}
}
