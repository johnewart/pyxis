package store

import (
	"context"
	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"
)

// TestTurnDecorationRepo_RoundTrip proves insert → list-by-session /
// list-by-tool-call round-trips typed decoration rows, and DeleteBySession
// removes only the targeted session's rows.
func TestTurnDecorationRepo_RoundTrip(t *testing.T) {
	db := NewTestSQLite(t)
	repo := NewTurnDecorationRepo(db)
	ctx := context.Background()

	seq1, seq2 := int64(1), int64(2)
	rows := []TurnDecorationRow{
		{TenantID: "ws1", SessionID: "s1", TurnSeq: &seq1, Target: "turn", Kind: "verification", URI: "llmd://verification/1"},
		{TenantID: "ws1", SessionID: "s1", TurnSeq: &seq1, Target: "tool_result", ToolCallID: "call1", Kind: "host_provenance", URI: "host://provenance/high"},
		{TenantID: "ws1", SessionID: "s1", TurnSeq: &seq2, Target: "tool_result", ToolCallID: "call2", Kind: "host_provenance", URI: "host://provenance/low"},
		{TenantID: "ws1", SessionID: "s2", TurnSeq: &seq1, Target: "turn", Kind: "verification", URI: ""},
	}
	if err := repo.Insert(ctx, rows); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.ListBySession(ctx, serverutil.Scope{TenantID: "ws1"}, "s1")
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListBySession: want 3 rows, got %d", len(got))
	}
	if got[0].Kind != "verification" || got[0].Target != "turn" || got[0].URI != "llmd://verification/1" {
		t.Errorf("row0 mismatch: %+v", got[0])
	}
	if got[1].ToolCallID != "call1" || *got[1].TurnSeq != 1 {
		t.Errorf("row1 mismatch: %+v", got[1])
	}

	byCall, err := repo.ListByToolCall(ctx, serverutil.Scope{TenantID: "ws1"}, "s1", "call2")
	if err != nil {
		t.Fatalf("ListByToolCall: %v", err)
	}
	if len(byCall) != 1 || byCall[0].URI != "host://provenance/low" {
		t.Fatalf("ListByToolCall: want 1 row for call2, got %+v", byCall)
	}

	if err := repo.DeleteBySession(ctx, serverutil.Scope{TenantID: "ws1"}, "s1"); err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	left, err := repo.ListBySession(ctx, serverutil.Scope{TenantID: "ws1"}, "s1")
	if err != nil {
		t.Fatalf("ListBySession after delete: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("expected s1 rows deleted, got %d", len(left))
	}
	other, err := repo.ListBySession(ctx, serverutil.Scope{TenantID: "ws1"}, "s2")
	if err != nil {
		t.Fatalf("ListBySession s2: %v", err)
	}
	if len(other) != 1 {
		t.Fatalf("expected s2 untouched, got %d rows", len(other))
	}
}

// TestTurnDecorationRepo_NilSafe proves a nil repo (persistence disabled) is a
// no-op on every method.
func TestTurnDecorationRepo_NilSafe(t *testing.T) {
	var repo *TurnDecorationRepo
	ctx := context.Background()
	if err := repo.Insert(ctx, []TurnDecorationRow{{SessionID: "s"}}); err != nil {
		t.Fatalf("nil Insert: %v", err)
	}
	if rows, err := repo.ListBySession(ctx, serverutil.Scope{TenantID: ""}, "s"); err != nil || rows != nil {
		t.Fatalf("nil ListBySession: rows=%v err=%v", rows, err)
	}
	if err := repo.DeleteBySession(ctx, serverutil.Scope{TenantID: ""}, "s"); err != nil {
		t.Fatalf("nil DeleteBySession: %v", err)
	}
}
