package store

import (
	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// Storing an assistant message with no content, thinking, activity, or tool calls
// must not persist a placeholder row, and History must not surface an empty turn.
func TestHistory_emptyAssistant_notPersisted(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	h := NewHistory(db, serverutil.Scope{TenantID: "u1"}, "a1")
	if h == nil {
		t.Fatal("nil history")
	}
	h.Store("s1", &llmv1.Message{Role: "user", Id: "u-1", Content: "hello"})
	h.Store("s1", &llmv1.Message{Role: "assistant", Id: "m-empty"})

	var rows []ChatMessageRow
	if err := db.Where("session_id = ? AND message_id = ?", "s1", "m-empty").
		Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows for empty assistant turn, got %d", len(rows))
	}

	out := h.History("s1", 0)
	if len(out) != 1 {
		t.Fatalf("expected only the user turn, got len=%d", len(out))
	}
	if out[0].GetRole() != "user" {
		t.Fatalf("expected user turn, got role=%q", out[0].GetRole())
	}
}

// Already-stored empty assistant rows (e.g. from before the fix) must be filtered
// out of the reconstructed model context so they are never sent to the LLM.
func TestHistory_emptyAssistant_legacyRowFiltered(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	h := NewHistory(db, serverutil.Scope{TenantID: "u1"}, "a1")
	if h == nil {
		t.Fatal("nil history")
	}
	rows := []ChatMessageRow{
		{TenantID: "u1", SessionID: "s2", TurnID: "t1", Kind: ChatMsgKindUser, Role: "user", MessageID: "u-1", Content: "hi"},
		// Legacy blank placeholder row.
		{TenantID: "u1", SessionID: "s2", TurnID: "t2", Kind: ChatMsgKindAssistant, Role: "assistant", MessageID: "m-empty"},
		{TenantID: "u1", SessionID: "s2", TurnID: "t3", Kind: ChatMsgKindAssistant, Role: "assistant", MessageID: "m-real", Content: "Real answer"},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	out := h.History("s2", 0)
	if len(out) != 2 {
		t.Fatalf("expected user + non-empty assistant, got len=%d", len(out))
	}
	if out[0].GetRole() != "user" || out[1].GetContent() != "Real answer" {
		t.Fatalf("unexpected reconstruction: %+v", out)
	}
}

// Tool-call-only assistant turns are meaningful and must be retained.
func TestHistory_toolCallOnlyAssistant_retained(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	h := NewHistory(db, serverutil.Scope{TenantID: "u1"}, "a1")
	if h == nil {
		t.Fatal("nil history")
	}
	h.Store("s3", &llmv1.Message{
		Role: "assistant",
		Id:   "m-tool",
		ToolCalls: []*llmv1.ToolCall{
			{Id: "tc-1", Name: "search", ArgumentsJson: "{}"},
		},
	})
	out := h.History("s3", 0)
	if len(out) != 1 {
		t.Fatalf("expected tool-call assistant turn retained, got len=%d", len(out))
	}
	if len(out[0].GetToolCalls()) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(out[0].GetToolCalls()))
	}
}
