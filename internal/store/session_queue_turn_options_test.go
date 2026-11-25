package store

import (
	"context"
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// An existing database has llmd_session_queue WITHOUT disable_tools (and
// pending rows in it). AutoMigrate must add the column in place, old rows must
// read back as tools-enabled, and new entries must round-trip the flag.
func TestSessionQueue_disableToolsColumnUpgradesOldSchema(t *testing.T) {
	db := NewTestSQLite(t)
	sc := serverutil.Scope{TenantID: "ws-queue-turnopts-upgrade"}
	ctx := context.Background()

	if err := db.Exec(`ALTER TABLE llmd_session_queue DROP COLUMN disable_tools`).Error; err != nil {
		t.Fatalf("drop column to simulate old schema: %v", err)
	}
	if db.Migrator().HasColumn(&SessionQueuedMessageRow{}, "disable_tools") {
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
	if !db.Migrator().HasColumn(&SessionQueuedMessageRow{}, "disable_tools") {
		t.Fatal("AutoMigrate did not add disable_tools")
	}

	repo := NewSessionQueueRepo(db)
	if _, err := repo.EnqueueWithAttachments(ctx, sc, "sess-old", "no tools please", nil, QueuedTurnOptions{DisableTools: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.EnqueueFront(ctx, sc, "sess-old", "front no tools", nil, QueuedTurnOptions{DisableTools: true}); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.List(ctx, sc, "sess-old")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	want := map[string]bool{"queued before upgrade": false, "no tools please": true, "front no tools": true}
	for _, r := range rows {
		if got := r.TurnOptions().DisableTools; got != want[r.Content] {
			t.Errorf("%q: DisableTools = %v, want %v", r.Content, got, want[r.Content])
		}
	}
}
