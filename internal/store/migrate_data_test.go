package store

import (
	"path/filepath"
	"reflect"
	"testing"
)

// Regression: a ~200s data migration ran inside konstructd's single
// "start llmd host" step, so the startup status could not say what was slow.
// AutoMigrateSteps must report the schema migration and each pending data
// migration as its own named step, and only the pending ones.
func TestAutoMigrateSteps_namesEachPendingMigration(t *testing.T) {
	t.Parallel()
	db, err := Open(filepath.Join(t.TempDir(), "llmd.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	trace := func(name string, fn func() error) error {
		names = append(names, name)
		return fn()
	}
	if err := AutoMigrateSteps(db, trace); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"migrate schema",
		"data migration 13: backfill session last_message_at",
		"data migration 14: drop raw chunks table",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("first run steps = %q, want %q", names, want)
	}

	names = nil
	if err := AutoMigrateSteps(db, trace); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"migrate schema"}) {
		t.Fatalf("second run steps = %q, want only the schema step (data migrations are recorded)", names)
	}
}

// TestAutoMigrate_idempotentFreshSchema runs AutoMigrate on an already-migrated
// fresh DB and checks it succeeds and records no legacy data-migration versions.
func TestAutoMigrate_idempotentFreshSchema(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("second AutoMigrate: %v", err)
	}
	if err := runDataMigrations(db, nil); err != nil {
		t.Fatalf("runDataMigrations rerun: %v", err)
	}

	var versions []int
	if err := db.Model(&SchemaMigrationRow{}).Pluck("version", &versions).Error; err != nil {
		t.Fatal(err)
	}
	want := []int{schemaMigrationBackfillSessionLastMessageAt, schemaMigrationDropRawChunksAndEventLookupIndex}
	if len(versions) != len(want) || versions[0] != want[0] || versions[1] != want[1] {
		t.Fatalf("schema migration versions=%v want only %v (no legacy versions recorded)", versions, want)
	}
}
