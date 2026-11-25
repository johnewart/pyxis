package store

import (
	"context"
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// An existing database has llmd_session_queue WITHOUT attachments_json (and
// pending rows in it). AutoMigrate must add the column in place, keep those
// rows readable (no attachments), and accept new entries that carry some.
func TestSessionQueue_attachmentsColumnUpgradesOldSchema(t *testing.T) {
	db := NewTestSQLite(t)
	sc := serverutil.Scope{TenantID: "ws-queue-upgrade"}
	ctx := context.Background()

	// Reproduce the previous schema: the table as it shipped, minus the new column.
	if err := db.Exec(`ALTER TABLE llmd_session_queue DROP COLUMN attachments_json`).Error; err != nil {
		t.Fatalf("drop column to simulate old schema: %v", err)
	}
	if db.Migrator().HasColumn(&SessionQueuedMessageRow{}, "attachments_json") {
		t.Fatal("old-schema setup failed: column still present")
	}
	if err := db.Exec(
		`INSERT INTO llmd_session_queue (id, tenant_id, scope_id, session_id, position, content, state, created_at, updated_at)
		 VALUES ('old-1', ?, '', 'sess-old', 1, 'queued before upgrade', 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		sc.TenantID).Error; err != nil {
		t.Fatalf("insert old row: %v", err)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate over old schema: %v", err)
	}
	if !db.Migrator().HasColumn(&SessionQueuedMessageRow{}, "attachments_json") {
		t.Fatal("AutoMigrate did not add attachments_json")
	}

	repo := NewSessionQueueRepo(db)
	rows, err := repo.List(ctx, sc, "sess-old")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Content != "queued before upgrade" {
		t.Fatalf("old row not readable after upgrade: %+v", rows)
	}
	if atts, err := rows[0].Attachments(); err != nil || atts != nil {
		t.Fatalf("old row attachments = %v, %v; want nil, nil", atts, err)
	}

	in := []QueuedAttachment{{ID: "a1", Kind: "composer_text", PayloadJSON: `{"name":"n"}`, Content: "ctx"}}
	if _, err := repo.EnqueueWithAttachments(ctx, sc, "sess-old", "with attachment", in, QueuedTurnOptions{}); err != nil {
		t.Fatal(err)
	}
	rows, err = repo.List(ctx, sc, "sess-old")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	got, err := rows[1].Attachments()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != in[0] {
		t.Fatalf("attachments round-trip = %+v, want %+v", got, in)
	}
}
