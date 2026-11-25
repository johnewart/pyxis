package store

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
)

// TestHistoryWithUsage_MatchesByMessageIDAndCarriesModelTime proves history
// attaches each model call's tokens and time to the assistant message it
// produced. ChatStream writes the metadata row under the provider's turn id,
// which never equals the chat rows' turn id, so the match has to be on the
// message id.
func TestHistoryWithUsage_MatchesByMessageIDAndCarriesModelTime(t *testing.T) {
	ctx := context.Background()
	db := NewTestSQLite(t)
	sc := serverutil.Scope{TenantID: "tenant-1", ScopeID: "project-1"}
	const sid = "sess-usage"
	h := NewHistory(db, sc, sid)
	h.StoreInTurn(sid, 1, &llmv1.Message{Role: "user", Id: "u1", Content: "hi"})
	h.StoreInTurn(sid, 1, &llmv1.Message{Role: "assistant", Id: "a1", Content: "first"})
	h.StoreInTurn(sid, 2, &llmv1.Message{Role: "assistant", Id: "a2", Content: "second"})

	meta := NewTurnMetadataRepo(db)
	rows := []TurnMetadataRow{
		{
			TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid,
			TurnID: "provider-turn-1", MessageID: "a1",
			ProviderInToks: int64p(1200), ProviderOutToks: int64p(80),
			ProviderCacheReadToks: int64p(5000), ProviderCacheWriteToks: int64p(300),
			ModelDurationMs: int64p(14200),
		},
		{
			// Written before model time was recorded: tokens only.
			TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid,
			TurnID: "provider-turn-2", MessageID: "a2",
			ProviderInToks: int64p(10), ProviderOutToks: int64p(2),
		},
	}
	for _, r := range rows {
		if err := meta.Upsert(ctx, r); err != nil {
			t.Fatalf("upsert metadata %q: %v", r.MessageID, err)
		}
	}

	msgs := h.HistoryWithUsage(ctx, sid, 0, meta)
	byID := map[string]*llmv1.Message{}
	for _, m := range msgs {
		byID[m.GetId()] = m
	}

	a1 := byID["a1"]
	if a1 == nil {
		t.Fatalf("assistant message a1 missing from history: %v", msgs)
	}
	u := a1.GetUsage()
	if u == nil {
		t.Fatal("a1: usage not attached")
	}
	if u.GetInputTokens() != 1200 || u.GetOutputTokens() != 80 || u.GetCacheReadTokens() != 5000 || u.GetCacheWriteTokens() != 300 {
		t.Fatalf("a1: wrong usage %+v", u)
	}
	if a1.ModelDurationMs == nil || *a1.ModelDurationMs != 14200 {
		t.Fatalf("a1: want model_duration_ms 14200, got %v", a1.ModelDurationMs)
	}

	a2 := byID["a2"]
	if a2 == nil {
		t.Fatal("assistant message a2 missing from history")
	}
	if a2.GetUsage().GetInputTokens() != 10 {
		t.Fatalf("a2: wrong usage %+v", a2.GetUsage())
	}
	if a2.ModelDurationMs != nil {
		t.Fatalf("a2: model time was never recorded, want unset, got %d", *a2.ModelDurationMs)
	}

	if u1 := byID["u1"]; u1 == nil || u1.GetUsage() != nil {
		t.Fatalf("u1: user messages carry no usage, got %+v", u1)
	}

	entries, err := meta.ListMessageUsage(ctx, sc, sid)
	if err != nil {
		t.Fatalf("list message usage: %v", err)
	}
	got := map[string]MessageUsageEntry{}
	for _, e := range entries {
		got[e.MessageID] = e
	}
	if e := got["a1"]; e.ModelDurationMs == nil || *e.ModelDurationMs != 14200 {
		t.Fatalf("ListMessageUsage a1: want model time 14200, got %v", e.ModelDurationMs)
	}
	if e, ok := got["a2"]; !ok || e.ModelDurationMs != nil {
		t.Fatalf("ListMessageUsage a2: want entry with no model time, got %+v (present=%v)", e, ok)
	}
}

// TestTurnMetadata_ModelDurationColumnAddedToExistingDatabase starts from the
// table as it was before model_duration_ms existed, with a row in it, and
// checks AutoMigrate adds the column and the old row still reads back.
func TestTurnMetadata_ModelDurationColumnAddedToExistingDatabase(t *testing.T) {
	ctx := context.Background()
	db := NewTestSQLite(t)
	m := db.Migrator()
	if err := m.DropColumn(&TurnMetadataRow{}, "model_duration_ms"); err != nil {
		t.Fatalf("put table back in its previous shape: %v", err)
	}
	if m.HasColumn(&TurnMetadataRow{}, "model_duration_ms") {
		t.Fatal("model_duration_ms still present after drop")
	}
	if err := db.Exec(`INSERT INTO llmd_turn_metadata
		(tenant_id, scope_id, session_id, turn_id, message_id, provider_id, model_id,
		 provider_in_toks, provider_out_toks, compression_meta, created_at, updated_at)
		VALUES ('tenant-1', 'project-1', 'sess-old', 'turn-old', 'a-old', '', '', 7, 3, '',
		        CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert old-shape row: %v", err)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("migrate old-shape database: %v", err)
	}
	if !m.HasColumn(&TurnMetadataRow{}, "model_duration_ms") {
		t.Fatal("AutoMigrate did not add model_duration_ms")
	}

	sc := serverutil.Scope{TenantID: "tenant-1", ScopeID: "project-1"}
	entries, err := NewTurnMetadataRepo(db).ListMessageUsage(ctx, sc, "sess-old")
	if err != nil {
		t.Fatalf("list usage after migrate: %v", err)
	}
	if len(entries) != 1 || entries[0].MessageID != "a-old" || entries[0].ProviderInToks != 7 {
		t.Fatalf("old row not readable after migrate: %+v", entries)
	}
	if entries[0].ModelDurationMs != nil {
		t.Fatalf("old row should have no model time, got %d", *entries[0].ModelDurationMs)
	}
}
