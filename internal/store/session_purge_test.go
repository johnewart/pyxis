package store

import (
	"context"
	"testing"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

// perSessionTables are the tables keyed by (tenant, scope, session) whose rows
// must all go when a session is purged.
func perSessionTables() []any {
	return []any{
		&SessionRow{}, &ChatMessageRow{}, &TrackedDocumentVersionRow{}, &ProviderSessionSyncRow{},
		&SessionEventRow{}, &TurnMetadataRow{}, &ToolResultSidecarRow{}, &SessionCacheRow{},
		&TurnDecorationRow{}, &SessionTurnRow{}, &SessionQueuedMessageRow{}, &SessionDraftRow{},
	}
}

func seedSessionRows(t *testing.T, db *gorm.DB, sc serverutil.Scope, sid string) {
	t.Helper()
	now := time.Now().UTC()
	rows := []any{
		&SessionRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, CreatedAt: now, UpdatedAt: now},
		&ChatMessageRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, TurnID: "t", Kind: "text", Role: "user", Content: "hi", CreatedAt: now},
		&TrackedDocumentVersionRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, DocumentID: "d", ContentHash: "h", CreatedAt: now},
		&ProviderSessionSyncRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, ProviderID: "p", SystemFingerprint: "f", UpdatedAt: now},
		&SessionEventRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, Seq: 1, EventJSON: "{}", CreatedAt: now},
		&TurnMetadataRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, TurnID: "t", CreatedAt: now, UpdatedAt: now},
		&ToolResultSidecarRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, ToolCallID: "c", Original: "x", CreatedAt: now},
		&SessionCacheRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, Namespace: "n", CacheKey: "k", Value: []byte("v"), UpdatedAt: now},
		&TurnDecorationRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, Target: "turn", Kind: "k", CreatedAt: now},
		&SessionTurnRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, TurnSeq: 1, StartedAt: now},
		&SessionQueuedMessageRow{ID: "q-" + sc.TenantID + "-" + sc.ScopeID + "-" + sid, TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, Position: 1, State: string(QueuedPending), CreatedAt: now, UpdatedAt: now},
		&SessionDraftRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, Content: "draft", UpdatedAt: now},
	}
	for _, r := range rows {
		if err := db.Create(r).Error; err != nil {
			t.Fatalf("seed %T: %v", r, err)
		}
	}
}

func countSessionRows(t *testing.T, db *gorm.DB, model any, sc serverutil.Scope, sid string) int64 {
	t.Helper()
	var n int64
	if err := db.Model(model).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).Count(&n).Error; err != nil {
		t.Fatalf("count %T: %v", model, err)
	}
	return n
}

func TestPurgeSession_RemovesAllPerSessionRows(t *testing.T) {
	db := NewTestSQLite(t)
	ctx := context.Background()
	// The purged session sits in a non-empty scope (a subfolder), not at "".
	target := serverutil.Scope{TenantID: "t1", ScopeID: "sub/dir"}
	otherTenant := serverutil.Scope{TenantID: "t2", ScopeID: "sub/dir"}
	seedSessionRows(t, db, target, "victim")
	seedSessionRows(t, db, target, "sibling")
	seedSessionRows(t, db, otherTenant, "victim") // same id, other tenant

	if err := NewSessionRepo(db).PurgeSession(ctx, target, "victim"); err != nil {
		t.Fatalf("PurgeSession: %v", err)
	}

	for _, m := range perSessionTables() {
		if n := countSessionRows(t, db, m, target, "victim"); n != 0 {
			t.Errorf("%T: %d rows left for purged session", m, n)
		}
		if n := countSessionRows(t, db, m, target, "sibling"); n != 1 {
			t.Errorf("%T: sibling session has %d rows, want 1", m, n)
		}
		if n := countSessionRows(t, db, m, otherTenant, "victim"); n != 1 {
			t.Errorf("%T: other tenant has %d rows, want 1", m, n)
		}
	}
}
