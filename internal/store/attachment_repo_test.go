package store

import (
	"context"
	"encoding/json"
	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"

	"gorm.io/gorm"
)

// seedAnchorMessage inserts a minimal chat message row so ChatAttachmentRepo.Upsert
// can resolve an anchor turn for it.
func seedAnchorMessage(t *testing.T, db *gorm.DB, ws, sid, turnID, messageID, role string) {
	t.Helper()
	if err := db.Create(&ChatMessageRow{
		TenantID: ws, SessionID: sid, TurnID: turnID,
		Kind: ChatMsgKindUser, Role: role, MessageID: messageID, Content: "hi",
	}).Error; err != nil {
		t.Fatalf("seed anchor message: %v", err)
	}
}

func TestChatAttachmentRepo_UpsertAndLoad(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewChatAttachmentRepo(db)
	ctx := context.Background()

	const ws, sid = "ws-att", "sess-att"
	seedAnchorMessage(t, db, ws, sid, "turn-1", "user-1", "user")

	if err := repo.Upsert(ctx, serverutil.Scope{TenantID: ws}, sid, "user-1", AttachmentInput{
		ID: "card-1", Kind: "ask_card", PayloadJSON: `{"title":"Pick one"}`,
	}); err != nil {
		t.Fatalf("Upsert (display-only): %v", err)
	}
	if err := repo.Upsert(ctx, serverutil.Scope{TenantID: ws}, sid, "user-1", AttachmentInput{
		ID: "card-1-response", Kind: "ask_card_response", PayloadJSON: `{"selected":["A"]}`,
		Content: "Selected: A.",
	}); err != nil {
		t.Fatalf("Upsert (with content): %v", err)
	}

	rows, err := repo.LoadRows(ctx, serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("LoadRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
}

// A card from a tool called over MCP (a CLI provider) is anchored to a call id
// that is never a transcript row; the row records the turn it was made in so
// a client can still place it, and a later write of the same card keeps it.
func TestChatAttachmentRepo_RecordsTurnMessageID(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewChatAttachmentRepo(db)
	ctx := context.Background()
	sc := serverutil.Scope{TenantID: "ws-turn"}
	const sid = "sess-turn"

	turnOf := func(id string) string {
		t.Helper()
		rows, err := repo.LoadRows(ctx, sc, sid)
		if err != nil {
			t.Fatalf("LoadRows: %v", err)
		}
		for _, r := range rows {
			var c attachmentRowContent
			if err := json.Unmarshal([]byte(r.Content), &c); err != nil {
				t.Fatalf("decode row: %v", err)
			}
			if c.ID == id {
				return c.TurnMessageID
			}
		}
		t.Fatalf("attachment %q not stored", id)
		return ""
	}
	card := AttachmentInput{ID: "chart-1", Kind: "llmd.display_card", PayloadJSON: `{"type":"chart"}`, AfterToolCallID: "mcp-1"}

	// No user message yet: nothing to record, and the write still succeeds.
	if err := repo.Upsert(ctx, sc, sid, "mcp-0", AttachmentInput{ID: "early", Kind: "k", PayloadJSON: `{}`}); err != nil {
		t.Fatalf("Upsert with no user message: %v", err)
	}
	if got := turnOf("early"); got != "" {
		t.Fatalf("turn with no user message = %q, want empty", got)
	}

	seedAnchorMessage(t, db, sc.TenantID, sid, "turn-1", "user-1", "user")
	seedAnchorMessage(t, db, sc.TenantID, sid, "turn-2", "user-2", "user")
	if err := repo.Upsert(ctx, sc, sid, "mcp-1", card); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got := turnOf("chart-1"); got != "user-2" {
		t.Fatalf("turn = %q, want the latest user message user-2", got)
	}

	seedAnchorMessage(t, db, sc.TenantID, sid, "turn-3", "user-3", "user")
	if err := repo.Upsert(ctx, sc, sid, "mcp-1", card); err != nil {
		t.Fatalf("re-Upsert: %v", err)
	}
	if got := turnOf("chart-1"); got != "user-2" {
		t.Fatalf("turn after rewrite = %q, want it kept as user-2", got)
	}
}

func TestChatAttachmentRepo_ListInjectableContent(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewChatAttachmentRepo(db)
	ctx := context.Background()

	const ws, sid = "ws-inj", "sess-inj"
	seedAnchorMessage(t, db, ws, sid, "turn-1", "user-1", "user")

	// Display-only attachment: no Content, must be excluded from injections.
	if err := repo.Upsert(ctx, serverutil.Scope{TenantID: ws}, sid, "user-1", AttachmentInput{
		ID: "card-1", Kind: "ask_card", PayloadJSON: `{"title":"Pick one"}`,
	}); err != nil {
		t.Fatalf("Upsert display-only: %v", err)
	}
	// Provider-facing attachment: has Content, must be included.
	if err := repo.Upsert(ctx, serverutil.Scope{TenantID: ws}, sid, "user-1", AttachmentInput{
		ID: "card-1-response", Kind: "ask_card_response",
		PayloadJSON: `{"selected":["A"]}`,
		Content:     "Selected: A.",
	}); err != nil {
		t.Fatalf("Upsert with content: %v", err)
	}

	injections, err := repo.ListInjectableContent(ctx, serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("ListInjectableContent: %v", err)
	}
	if len(injections) != 1 {
		t.Fatalf("want 1 injectable row, got %d: %+v", len(injections), injections)
	}
	if injections[0].AnchorMessageID != "user-1" || injections[0].Content != "Selected: A." {
		t.Fatalf("injection mismatch: %+v", injections[0])
	}
}
