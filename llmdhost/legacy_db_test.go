package llmdhost

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnewart/pyxis/internal/store"
)

// seedLegacyLlmdDB writes a minimal pre-tenant llmd database: llmd_sessions
// keyed by workspace_id, exactly the shape that made AutoMigrate die with
// "Cannot add a NOT NULL column with default value NULL".
func seedLegacyLlmdDB(t *testing.T, path string) {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE llmd_sessions (workspace_id TEXT, session_id TEXT, title TEXT, PRIMARY KEY (workspace_id, session_id))`,
		`INSERT INTO llmd_sessions (workspace_id, session_id, title) VALUES ('ws1', 's1', 'old chat')`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("seed legacy db: %v", err)
		}
	}
	if err := closeGorm(db); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}
}

func TestNew_legacyDatabaseIsRefusedThenBackedUp(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "llmd.sqlite")
	seedLegacyLlmdDB(t, dbPath)
	cfg := Config{
		DatabaseDSN:         dbPath,
		KnowledgeSQLitePath: filepath.Join(dir, "llmd-knowledge.sqlite"),
		RuntimeIdleEvict:    -1,
	}

	_, err := New(cfg)
	var legacy *LegacyDatabaseError
	if !errors.As(err, &legacy) {
		t.Fatalf("New on legacy db: err = %v, want *LegacyDatabaseError", err)
	}
	if len(legacy.Databases) != 1 || legacy.Databases[0].Kind != "llmd" || legacy.Databases[0].Path != dbPath {
		t.Fatalf("legacy databases = %+v", legacy.Databases)
	}

	// Refusal must leave the old data untouched (no partial migration).
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("legacy db should still exist after refusal: %v", err)
	}

	backupDir := filepath.Join(dir, "backups")
	moved, err := BackupLegacyDatabases(legacy, backupDir)
	if err != nil {
		t.Fatalf("BackupLegacyDatabases: %v", err)
	}
	if len(moved) == 0 || moved[0].From != dbPath {
		t.Fatalf("moved = %+v, want the llmd db first", moved)
	}
	if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original path should be free after backup, stat err = %v", err)
	}
	// The backup is a readable copy of the old data.
	old, err := store.Open(moved[0].To)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	var title string
	if err := old.Raw(`SELECT title FROM llmd_sessions WHERE session_id = 's1'`).Scan(&title).Error; err != nil || title != "old chat" {
		t.Fatalf("backup contents: title=%q err=%v", title, err)
	}
	_ = closeGorm(old)

	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New after backup: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// The old SQLite knowledge database (any schema) is derived data: New does
// not refuse it, it deletes it and opens the lexical index beside it.
func TestNew_legacyKnowledgeDatabaseIsDiscarded(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "llmd.sqlite")
	kPath := filepath.Join(dir, "llmd-knowledge.sqlite")

	kdb, err := store.Open(kPath)
	if err != nil {
		t.Fatalf("open legacy knowledge db: %v", err)
	}
	if err := kdb.Exec(`CREATE TABLE documents (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL)`).Error; err != nil {
		t.Fatalf("seed legacy knowledge db: %v", err)
	}
	_ = closeGorm(kdb)

	h, err := New(Config{DatabaseDSN: dbPath, KnowledgeSQLitePath: kPath, RuntimeIdleEvict: -1})
	if err != nil {
		t.Fatalf("New with a legacy knowledge db: %v", err)
	}
	defer h.Close()
	if _, err := os.Stat(kPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy knowledge db survived: %v", err)
	}
	if want := filepath.Join(dir, "llmd-knowledge.pebble"); h.KnowledgeDSN() != want {
		t.Fatalf("knowledge dir = %q, want %q", h.KnowledgeDSN(), want)
	}
	if h.KnowledgeStore() == nil {
		t.Fatal("KnowledgeStore not exposed")
	}
}

func TestNew_freshDatabaseIsNotLegacy(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		DatabaseDSN:         filepath.Join(dir, "llmd.sqlite"),
		KnowledgeSQLitePath: filepath.Join(dir, "llmd-knowledge.sqlite"),
		RuntimeIdleEvict:    -1,
	}
	for i := 0; i < 2; i++ { // second open = existing, already-current schema
		h, err := New(cfg)
		if err != nil {
			t.Fatalf("New #%d: %v", i+1, err)
		}
		if err := h.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i+1, err)
		}
	}
}

func TestBackupLegacyDatabases_refusesNonFileDatabase(t *testing.T) {
	lerr := &LegacyDatabaseError{Databases: []LegacyDatabase{{Kind: "llmd", DSN: "postgres://x"}}}
	if _, err := BackupLegacyDatabases(lerr, t.TempDir()); err == nil {
		t.Fatalf("expected refusal for a non-file database")
	}
}

func TestSqliteFilePath(t *testing.T) {
	abs := func(p string) string { a, _ := filepath.Abs(p); return a }
	cases := map[string]string{
		"llmd.sqlite":                         abs("llmd.sqlite"),
		"sqlite://data/llmd.sqlite":           abs("data/llmd.sqlite"),
		"file:llmd.sqlite?_busy_timeout=5000": abs("llmd.sqlite"),
		"file:memdb1?mode=memory&cache=shared": "",
		":memory:":                            "",
		"postgres://u@h/db":                   "",
	}
	for in, want := range cases {
		if got := sqliteFilePath(in); got != want {
			t.Errorf("sqliteFilePath(%q) = %q, want %q", in, got, want)
		}
	}
}
