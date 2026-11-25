package server

import (
	"context"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/toolregistry"
	"gorm.io/gorm"
)

// storageTestServer is a server over a real SQLite file with the storage repo
// wired in.
func storageTestServer(t *testing.T) (*LlmServer, *gorm.DB) {
	t.Helper()
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{})
	return &LlmServer{
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		SessionRepo:     store.NewSessionRepo(db),
		SessionEvents:   store.NewSessionEventRepo(db),
		Storage:         store.NewStorageRepo(db),
	}, db
}

func seedSessionWithMessages(t *testing.T, db *gorm.DB, sc serverutil.Scope, sid, title string, msgs int, size int) {
	t.Helper()
	ctx := context.Background()
	repo := store.NewSessionRepo(db)
	if err := repo.CreateSessionRowIfMissing(ctx, sc, sid); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateSession(ctx, sc, sid, map[string]interface{}{"title": title}); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("x", size)
	for i := 0; i < msgs; i++ {
		row := &store.ChatMessageRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, TurnID: "t", Kind: "text", Role: "user", Content: body, CreatedAt: time.Now().UTC()}
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestGetStorageStats_SQLite(t *testing.T) {
	s, db := storageTestServer(t)
	sc := serverutil.Scope{TenantID: "stats_tenant", ScopeID: "sub/folder"} // not the root scope
	other := serverutil.Scope{TenantID: "other_tenant", ScopeID: "sub/folder"}
	seedSessionWithMessages(t, db, sc, "small", "Small", 1, 100)
	seedSessionWithMessages(t, db, sc, "big", "Big", 20, 20000)
	seedSessionWithMessages(t, db, sc, "mid", "Mid", 5, 5000)
	seedSessionWithMessages(t, db, other, "foreign", "Foreign", 50, 50000)
	sc2 := serverutil.Scope{TenantID: "stats_tenant", ScopeID: "other/folder"}
	seedSessionWithMessages(t, db, sc2, "second", "Second", 30, 30000)
	last := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := db.Exec("UPDATE llmd_sessions SET last_message_at = ? WHERE tenant_id = ? AND scope_id = ? AND session_id = ?", last, sc.TenantID, sc.ScopeID, "big").Error; err != nil {
		t.Fatal(err)
	}
	ctx := sc.IncomingContext(context.Background())

	res, err := s.GetStorageStats(ctx, &llmv1.GetStorageStatsRequest{LargestSessionsLimit: 3})
	if err != nil {
		t.Fatalf("GetStorageStats: %v", err)
	}
	if res.GetDatabaseBytes() <= 0 {
		t.Fatalf("database_bytes = %d, want > 0", res.GetDatabaseBytes())
	}
	var chat bool
	for _, tb := range res.GetTables() {
		if tb.GetBytes() <= 0 {
			t.Errorf("table %q has %d bytes, want > 0", tb.GetName(), tb.GetBytes())
		}
		if tb.GetName() == "llmd_chat_messages" {
			chat = true
		}
	}
	if !chat {
		t.Fatalf("llmd_chat_messages missing from tables: %v", res.GetTables())
	}
	if res.GetSessionCount() != 4 {
		t.Fatalf("session_count = %d, want 4 (this tenant only)", res.GetSessionCount())
	}
	got := res.GetLargestSessions()
	if len(got) != 3 {
		t.Fatalf("largest sessions = %d, want limit 3", len(got))
	}
	if got[0].GetSessionId() != "second" || got[1].GetSessionId() != "big" || got[2].GetSessionId() != "mid" {
		t.Fatalf("order = %q, %q, %q; want second, big, mid", got[0].GetSessionId(), got[1].GetSessionId(), got[2].GetSessionId())
	}
	wantScope := map[string]string{"second": "other/folder", "big": "sub/folder", "mid": "sub/folder"}
	for _, g := range got {
		if g.GetScopeId() != wantScope[g.GetSessionId()] {
			t.Errorf("session %q scope_id = %q, want %q", g.GetSessionId(), g.GetScopeId(), wantScope[g.GetSessionId()])
		}
	}
	if g := got[1].GetLastMessageUnixMs(); g != last.UnixMilli() {
		t.Errorf("big last_message_unix_ms = %d, want %d", g, last.UnixMilli())
	}
	got = got[1:]
	if got[0].GetBytes() < got[1].GetBytes() {
		t.Fatalf("not ordered by bytes: %d < %d", got[0].GetBytes(), got[1].GetBytes())
	}
	if got[0].GetTitle() != "Big" || got[0].GetMessageCount() != 20 {
		t.Fatalf("big session fields: %+v", got[0])
	}
	for _, g := range got {
		if g.GetSessionId() == "foreign" {
			t.Fatal("another tenant's session was listed")
		}
	}
}
