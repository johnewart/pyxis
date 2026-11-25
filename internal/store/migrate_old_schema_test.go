package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// The previous schema had llmd_turn_raw_chunks (nothing writes it any more) and
// idx_session_events_lookup, a duplicate of SessionEventRow's primary key.
// Opening such a database through AutoMigrate must drop both and keep all
// other data.
func TestAutoMigrate_DropsRawChunksAndDuplicateEventIndex(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "llmd.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	// Build the schema without data migrations, then add the old leftovers.
	if err := db.AutoMigrate(CoreModels...); err != nil {
		t.Fatalf("schema: %v", err)
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS llmd_turn_raw_chunks (
			id integer PRIMARY KEY AUTOINCREMENT, tenant_id text NOT NULL, scope_id text NOT NULL DEFAULT '',
			session_id text NOT NULL, turn_id text NOT NULL, chunk_index integer NOT NULL,
			raw_data text, completed numeric NOT NULL DEFAULT false, created_at datetime)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_chunks_lookup ON llmd_turn_raw_chunks(tenant_id, scope_id, session_id, turn_id)`,
		`CREATE INDEX IF NOT EXISTS idx_session_events_lookup ON llmd_session_events(tenant_id, scope_id, session_id, seq)`,
		`INSERT INTO llmd_turn_raw_chunks (tenant_id, scope_id, session_id, turn_id, chunk_index, raw_data) VALUES ('t1','sub','s1','turn1',0,'raw')`,
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("old schema %q: %v", s, err)
		}
	}
	sc := serverutil.Scope{TenantID: "t1", ScopeID: "sub"}
	ctx := context.Background()
	if err := NewSessionRepo(db).CreateSessionRowIfMissing(ctx, sc, "s1"); err != nil {
		t.Fatal(err)
	}
	ev := NewSessionEventRepo(db)
	for i := 0; i < 3; i++ {
		if _, err := ev.AppendEvent(ctx, sc, "s1", `{}`); err != nil {
			t.Fatal(err)
		}
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate old schema: %v", err)
	}

	var n int64
	if err := db.Raw(`SELECT count(*) FROM sqlite_master WHERE name IN ('llmd_turn_raw_chunks','idx_turn_chunks_lookup','idx_session_events_lookup')`).Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d of the raw-chunk table/indexes/event lookup index still exist", n)
	}
	if c, err := ev.CountEventsForSession(ctx, sc, "s1"); err != nil || c != 3 {
		t.Fatalf("events = %d, %v; want 3 intact", c, err)
	}
	if seq, err := ev.AppendEvent(ctx, sc, "s1", `{}`); err != nil || seq != 4 {
		t.Fatalf("append after migrate = %d, %v; want 4", seq, err)
	}
	var sessions int64
	if err := db.Model(&SessionRow{}).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", "t1", "sub", "s1").Count(&sessions).Error; err != nil || sessions != 1 {
		t.Fatalf("session row count = %d, %v; want 1", sessions, err)
	}
	// Re-running is a no-op.
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("second AutoMigrate: %v", err)
	}
}
