package store

import (
	"context"
	"errors"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/agentprofile"
	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

// Two tenants share the same scope ID and the same session ID. Every
// session-keyed table must keep their rows apart.
const isoSession = "shared-session"

var (
	isoA = serverutil.Scope{TenantID: "tenant-a", ScopeID: "s1"}
	isoB = serverutil.Scope{TenantID: "tenant-b", ScopeID: "s1"}
)

func int64p(v int64) *int64 { return &v }

// seedTenant writes one row into every session-keyed table for sc, tagging
// content with tag so reads can prove whose row came back.
func seedTenant(t *testing.T, db *gorm.DB, sc serverutil.Scope, tag string) {
	t.Helper()
	ctx := context.Background()
	if err := NewSessionRepo(db).CreateSessionRowIfMissing(ctx, sc, isoSession); err != nil {
		t.Fatalf("%s: create session: %v", sc, err)
	}
	if err := NewSessionRepo(db).UpdateSession(ctx, sc, isoSession, map[string]any{"title": tag}); err != nil {
		t.Fatalf("%s: title: %v", sc, err)
	}
	h := NewHistory(db, sc, isoSession)
	h.StoreInTurn(isoSession, 1, &llmv1.Message{Id: "msg-" + tag, Role: "user", Content: tag})
	if _, err := NewSessionEventRepo(db).AppendEvent(ctx, sc, isoSession, `{"tag":"`+tag+`"}`); err != nil {
		t.Fatalf("%s: event: %v", sc, err)
	}
	if err := NewSessionDraftRepo(db).Upsert(ctx, sc, isoSession, tag); err != nil {
		t.Fatalf("%s: draft: %v", sc, err)
	}
	if _, err := NewSessionQueueRepo(db).Enqueue(ctx, sc, isoSession, tag); err != nil {
		t.Fatalf("%s: queue: %v", sc, err)
	}
	if err := NewSessionCacheRepo(db).Set(ctx, sc, isoSession, "ns", "k", []byte(tag)); err != nil {
		t.Fatalf("%s: cache: %v", sc, err)
	}
	if err := NewChatAttachmentRepo(db).Upsert(ctx, sc, isoSession, "msg-"+tag, AttachmentInput{
		ID: "att-" + tag, Kind: "note", PayloadJSON: `{"tag":"` + tag + `"}`,
	}); err != nil {
		t.Fatalf("%s: attachment: %v", sc, err)
	}
	if _, _, err := NewTrackedDocumentRepo(db).AppendVersion(ctx, sc, isoSession, TrackedDocInput{
		DocumentID: "doc", Name: tag, Content: tag,
	}); err != nil {
		t.Fatalf("%s: tracked doc: %v", sc, err)
	}
	rows := []any{
		&ToolResultSidecarRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: isoSession, ToolCallID: "tc", Original: tag, OriginalTokens: 10},
		&TurnMetadataRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: isoSession, TurnID: "turn-1", MessageID: "msg-" + tag, ProviderInToks: int64p(100)},
		&TurnDecorationRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: isoSession, Target: "turn", Kind: "k", URI: tag},
	}
	for _, r := range rows {
		if err := db.Create(r).Error; err != nil {
			t.Fatalf("%s: create %T: %v", sc, r, err)
		}
	}
	if err := NewProviderSessionSyncRepo(db).Upsert(ctx, &ProviderSessionSync{
		Scope: sc, AgentID: isoSession, SessionID: isoSession, ProviderID: "p",
		BackendSessionID: tag, SystemFingerprint: "fp", SyncedMessageCount: 1,
	}); err != nil {
		t.Fatalf("%s: provider sync: %v", sc, err)
	}
}

// assertSees checks that every session-keyed read under sc returns only the
// rows tagged tag.
func assertSees(t *testing.T, db *gorm.DB, sc serverutil.Scope, tag string) {
	t.Helper()
	ctx := context.Background()
	sess, err := NewSessionRepo(db).GetSession(ctx, sc, isoSession)
	if err != nil || sess == nil || sess.Title != tag {
		t.Fatalf("%s: session = %+v, %v; want title %q", sc, sess, err, tag)
	}
	msgs := NewHistory(db, sc, isoSession).History(isoSession, 0)
	if len(msgs) != 1 || msgs[0].GetContent() != tag {
		t.Fatalf("%s: messages = %v; want one %q", sc, msgs, tag)
	}
	evs, err := NewSessionEventRepo(db).GetAllEvents(ctx, sc, isoSession)
	if err != nil || len(evs) != 1 || evs[0].EventJSON != `{"tag":"`+tag+`"}` {
		t.Fatalf("%s: events = %+v, %v", sc, evs, err)
	}
	if d, err := NewSessionDraftRepo(db).Get(ctx, sc, isoSession); err != nil || d != tag {
		t.Fatalf("%s: draft = %q, %v; want %q", sc, d, err, tag)
	}
	q, err := NewSessionQueueRepo(db).List(ctx, sc, isoSession)
	if err != nil || len(q) != 1 || q[0].Content != tag {
		t.Fatalf("%s: queue = %+v, %v", sc, q, err)
	}
	if v, ok, err := NewSessionCacheRepo(db).Get(ctx, sc, isoSession, "ns", "k"); err != nil || !ok || string(v) != tag {
		t.Fatalf("%s: cache = %q %v %v", sc, v, ok, err)
	}
	atts, err := NewChatAttachmentRepo(db).LoadRows(ctx, sc, isoSession)
	if err != nil || len(atts) != 1 {
		t.Fatalf("%s: attachments = %+v, %v; want 1", sc, atts, err)
	}
	docs, err := NewTrackedDocumentRepo(db).ListSessionVersions(ctx, sc, isoSession)
	if err != nil || len(docs) != 1 || docs[0].Name != tag {
		t.Fatalf("%s: tracked docs = %+v, %v", sc, docs, err)
	}
	side, err := NewToolResultSidecarRepo(db).GetByToolCallID(ctx, sc, isoSession, "tc")
	if err != nil || side.Original != tag {
		t.Fatalf("%s: sidecar = %+v, %v", sc, side, err)
	}
	metas, err := NewTurnMetadataRepo(db).ListBySession(ctx, sc, isoSession)
	if err != nil || len(metas) != 1 || metas[0].MessageID != "msg-"+tag {
		t.Fatalf("%s: turn metadata = %+v, %v", sc, metas, err)
	}
	decos, err := NewTurnDecorationRepo(db).ListBySession(ctx, sc, isoSession)
	if err != nil || len(decos) != 1 || decos[0].URI != tag {
		t.Fatalf("%s: decorations = %+v, %v", sc, decos, err)
	}
	ps, err := NewProviderSessionSyncRepo(db).Get(ctx, sc, isoSession, isoSession, "p")
	if err != nil || ps == nil || ps.BackendSessionID != tag {
		t.Fatalf("%s: provider sync = %+v, %v", sc, ps, err)
	}
}

func TestTenantIsolation_sameSessionIDAcrossTenants(t *testing.T) {
	db := NewTestSQLite(t)
	seedTenant(t, db, isoA, "A")
	seedTenant(t, db, isoB, "B")
	assertSees(t, db, isoA, "A")
	assertSees(t, db, isoB, "B")
}

func TestTenantIsolation_exactScopeMatch(t *testing.T) {
	db := NewTestSQLite(t)
	wide := serverutil.Scope{TenantID: "tenant-a"}
	seedTenant(t, db, wide, "wide")
	seedTenant(t, db, isoA, "A")
	assertSees(t, db, wide, "wide")
	assertSees(t, db, isoA, "A")
}

func TestTenantIsolation_turnRegisterIndependent(t *testing.T) {
	db := NewTestSQLite(t)
	ctx := context.Background()
	reg := NewTurnRegister(db)
	for _, sc := range []serverutil.Scope{isoA, isoB} {
		seq, err := reg.Advance(ctx, sc, isoSession, time.Now(), "")
		if err != nil || seq != 1 {
			t.Fatalf("%s: first turn = %d, %v; want 1", sc, seq, err)
		}
	}
	// A fresh register must bootstrap each tenant from its own rows.
	reg2 := NewTurnRegister(db)
	if _, err := reg2.Advance(ctx, isoA, isoSession, time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := reg2.Bootstrap(ctx, isoB, isoSession); got != 1 {
		t.Fatalf("tenant B turn after A advanced = %d, want 1", got)
	}
	if got, _ := reg2.Bootstrap(ctx, isoA, isoSession); got != 2 {
		t.Fatalf("tenant A turn = %d, want 2", got)
	}
}

func TestTenantIsolation_purgeAndForkLeaveOtherTenant(t *testing.T) {
	db := NewTestSQLite(t)
	ctx := context.Background()
	seedTenant(t, db, isoA, "A")
	seedTenant(t, db, isoB, "B")
	repo := NewSessionRepo(db)

	if _, err := repo.ForkSession(ctx, isoA, isoSession, "forked", "", ForkOptions{}); err != nil {
		t.Fatalf("fork A: %v", err)
	}
	if s, err := repo.GetSession(ctx, isoB, "forked"); err == nil && s != nil {
		t.Fatalf("fork of A leaked into B: %+v", s)
	}
	assertSees(t, db, isoB, "B")

	if err := repo.PurgeSession(ctx, isoA, isoSession); err != nil {
		t.Fatalf("purge A: %v", err)
	}
	assertSees(t, db, isoB, "B")
	if s, err := repo.GetSession(ctx, isoA, isoSession); err == nil && s != nil {
		t.Fatalf("A session survived purge: %+v", s)
	}
}

func TestTenantIsolation_agentProfileNameUniqueness(t *testing.T) {
	db := NewTestSQLite(t)
	ctx := context.Background()
	repo := NewAgentProfileRepo(db)
	wideA := serverutil.Scope{TenantID: "tenant-a"}
	for _, sc := range []serverutil.Scope{isoA, isoB, wideA} {
		if err := repo.Create(ctx, sc, &agentprofile.Profile{Name: "reviewer"}); err != nil {
			t.Fatalf("%s: first create: %v", sc, err)
		}
	}
	for _, sc := range []serverutil.Scope{isoA, wideA} {
		err := repo.Create(ctx, sc, &agentprofile.Profile{Name: "reviewer"})
		if !errors.Is(err, ErrAgentProfileNameTaken) {
			t.Fatalf("%s: duplicate create err = %v, want ErrAgentProfileNameTaken", sc, err)
		}
	}
	// The unique index is the backstop when the pre-check is bypassed.
	if err := db.Create(&AgentProfileRow{ID: "raw-dup", TenantID: "tenant-a", ScopeID: "", Name: "reviewer"}).Error; err == nil {
		t.Fatal("unique index allowed a duplicate name in the tenant-wide scope")
	}
	list, err := repo.List(ctx, isoA)
	if err != nil || len(list) != 1 {
		t.Fatalf("list A = %d, %v; want 1", len(list), err)
	}
}

func TestTenantIsolation_tenantTotalsExcludeOtherTenant(t *testing.T) {
	db := NewTestSQLite(t)
	ctx := context.Background()
	seedTenant(t, db, isoA, "A")
	seedTenant(t, db, serverutil.Scope{TenantID: "tenant-a", ScopeID: "s2"}, "A2")
	seedTenant(t, db, isoB, "B")
	meta := NewTurnMetadataRepo(db)
	tot, err := meta.GetTenantTotals(ctx, "tenant-a")
	if err != nil || tot.TotalProviderIn != 200 {
		t.Fatalf("tenant A totals = %+v, %v; want provider_in 200", tot, err)
	}
	scope, err := meta.GetScopeTotals(ctx, isoA)
	if err != nil || scope.TotalProviderIn != 100 {
		t.Fatalf("scope A totals = %+v, %v; want provider_in 100", scope, err)
	}
	sav, err := NewToolResultSidecarRepo(db).SumSavingsTenant(ctx, "tenant-a")
	if err != nil || sav.OriginalTokens != 20 {
		t.Fatalf("tenant A savings = %+v, %v; want original 20", sav, err)
	}
}
