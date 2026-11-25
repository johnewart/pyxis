package store

import (
	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestHistory_activityHeadline_roundTrip(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	h := NewHistory(db, serverutil.Scope{TenantID: "u1"}, "a1")
	if h == nil {
		t.Fatal("nil history")
	}
	h.Store("s1", &llmv1.Message{
		Role:             "assistant",
		Id:               "m1",
		ThinkingContent:  "internal notes",
		ActivityHeadline: "Indexing repository",
		Content:          "Here is the answer.",
	})
	out := h.History("s1", 0)
	if len(out) != 1 {
		t.Fatalf("len=%d", len(out))
	}
	m := out[0]
	if m.GetActivityHeadline() != "Indexing repository" || m.GetThinkingContent() != "internal notes" || m.GetContent() != "Here is the answer." {
		t.Fatalf("got %+v", m)
	}
}

func TestHistory_activityHeadline_mergeMultipleRows(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	h := NewHistory(db, serverutil.Scope{TenantID: "u1"}, "a1")
	if h == nil {
		t.Fatal("nil history")
	}
	// Simulate two activity slices for one assistant turn (same message id) by storing rows directly.
	turnID := "shared-turn"
	msgID := "m2"
	rows := []ChatMessageRow{
		{TenantID: "u1", SessionID: "s2", TurnID: turnID, Kind: ChatMsgKindActivity, Role: "assistant", MessageID: msgID, Content: "First status"},
		{TenantID: "u1", SessionID: "s2", TurnID: turnID, Kind: ChatMsgKindActivity, Role: "assistant", MessageID: msgID, Content: "Second status"},
		{TenantID: "u1", SessionID: "s2", TurnID: turnID, Kind: ChatMsgKindAssistant, Role: "assistant", MessageID: msgID, Content: "Body"},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	out := h.History("s2", 0)
	if len(out) != 1 {
		t.Fatalf("len=%d", len(out))
	}
	if out[0].GetActivityHeadline() != "First status\nSecond status" {
		t.Fatalf("activity=%q", out[0].GetActivityHeadline())
	}
}
