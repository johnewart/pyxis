package store

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

// NewTestSQLite opens an isolated SQLite database in the test's temp dir and runs AutoMigrate.
// Use for tests that need the same persistence stack as llmd (agents, chat history, user providers).
//
// A real file, not a shared-cache in-memory database: shared cache answers a
// concurrent write with "database table is locked" at once instead of waiting
// on busy_timeout, so tests saw lock failures production never has (and missed
// the deferred-transaction ones it does have). A file gets the production
// settings: WAL, busy_timeout, _txlock=immediate.
func NewTestSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "llmd.sqlite"))
	if err != nil {
		t.Fatalf("open test sqlite: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("migrate test sqlite: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err != nil {
			t.Errorf("test sqlite handle: %v", err)
			return
		}
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close test sqlite: %v", err)
		}
	})
	return db
}
