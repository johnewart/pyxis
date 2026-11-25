package store

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync"

	"github.com/johnewart/pyxis/internal/gormlog"
	sqlite3 "github.com/mattn/go-sqlite3"
	"gorm.io/driver/postgres"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// storeDriverName is the CGO sqlite3 driver registered with a ConnectHook that applies
// pragmas (journal_mode=WAL, busy_timeout) to every pooled connection. Without this, a
// freshly-created llmd.sqlite defaults to SQLite's rollback-journal ("delete") mode, which
// costs a synchronous fsync-heavy journal file create/delete on every write transaction —
// on APFS in particular that can escalate to F_FULLFSYNC and add real, visible per-session
// latency. journal_mode is persisted in the database file header, so this only needs to
// take effect once per file, but busy_timeout is per-connection and must be set on every
// connection GORM opens from the pool, hence the ConnectHook rather than a one-shot Exec
// after Open().
const storeDriverName = "llmd_sqlite_store"

var (
	storeDriverMu         sync.Mutex
	storeDriverRegistered bool
)

func registerStoreDriver() {
	storeDriverMu.Lock()
	defer storeDriverMu.Unlock()
	if storeDriverRegistered {
		return
	}
	hook := func(conn *sqlite3.SQLiteConn) error {
		if _, err := conn.Exec("PRAGMA foreign_keys=ON;", nil); err != nil {
			return fmt.Errorf("pragma foreign_keys: %w", err)
		}
		// journal_mode=WAL is sticky in the file header, and setting it takes a lock that
		// can wait behind an in-flight writer, so only set it when the file is not
		// already WAL. In-memory databases report "memory" and stay that way (setting
		// WAL there is a silent no-op), so skip them too.
		mode, err := currentJournalMode(conn)
		if err != nil {
			return fmt.Errorf("read journal_mode: %w", err)
		}
		if mode != "wal" && mode != "memory" {
			if _, err := conn.Exec("PRAGMA journal_mode=WAL;", nil); err != nil {
				return fmt.Errorf("pragma journal_mode: %w", err)
			}
		}
		if _, err := conn.Exec("PRAGMA busy_timeout=30000;", nil); err != nil {
			return fmt.Errorf("pragma busy_timeout: %w", err)
		}
		// In WAL mode, NORMAL skips the fsync on every commit (the WAL is synced
		// at checkpoints instead). The database cannot be corrupted by it; a
		// power loss or OS crash can drop the last few commits, a crash of this
		// process cannot. FULL (the default) fsynced once per streamed token's
		// session event, and that is where the write lock time went.
		if _, err := conn.Exec("PRAGMA synchronous=NORMAL;", nil); err != nil {
			return fmt.Errorf("pragma synchronous: %w", err)
		}
		return nil
	}
	sql.Register(storeDriverName, &sqlite3.SQLiteDriver{ConnectHook: hook})
	storeDriverRegistered = true
}

func currentJournalMode(conn *sqlite3.SQLiteConn) (string, error) {
	rows, err := conn.Query("PRAGMA journal_mode;", nil)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	dest := make([]driver.Value, 1)
	if err := rows.Next(dest); err != nil {
		return "", err
	}
	switch v := dest[0].(type) {
	case string:
		return strings.ToLower(v), nil
	case []byte:
		return strings.ToLower(string(v)), nil
	default:
		return fmt.Sprint(v), nil
	}
}

// sqlitePoolSize bounds the sqlite pool and, crucially, keeps that many connections
// idle. database/sql's default of 2 idle connections meant every burst of concurrent
// readers (e.g. many session tails waking together) opened fresh connections, ran
// the ConnectHook on each, then threw them away. Keeping idle == open and never
// expiring idle connections means the hook runs once per connection for the life of
// the process. WAL allows concurrent readers; writers still serialize in SQLite.
const sqlitePoolSize = 16

// withTxLock makes every transaction on a file database start as BEGIN
// IMMEDIATE, holding the write lock from the start.
//
// With the default (deferred), a transaction that reads and then writes - most
// of this package: AppendEvent reads MAX(seq) then inserts, upserts look up the
// existing row first - fails at once with "database is locked" when another
// connection is writing. SQLite will not upgrade a read lock while a writer is
// active, and the busy_timeout the ConnectHook sets never applies to that
// upgrade. Taking the write lock at BEGIN is where busy_timeout does apply, so
// writers queue instead of failing. konstructdb.withConnParams does the same
// for konstructd's database.
//
// mattn/go-sqlite3 reads _txlock from the DSN for every pooled connection.
// In-memory databases (tests, shared cache) are left alone.
func withTxLock(dsn string) string {
	if strings.Contains(dsn, ":memory:") || strings.Contains(dsn, "mode=memory") || strings.Contains(dsn, "_txlock=") {
		return dsn
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&_txlock=immediate"
	}
	return dsn + "?_txlock=immediate"
}

func openSQLite(dsn string) (*gorm.DB, error) {
	registerStoreDriver()
	db, err := gorm.Open(gormsqlite.Dialector{DriverName: storeDriverName, DSN: withTxLock(dsn)}, &gorm.Config{
		Logger: gormlog.New(),
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(sqlitePoolSize)
	sqlDB.SetMaxIdleConns(sqlitePoolSize)
	sqlDB.SetConnMaxIdleTime(0)
	sqlDB.SetConnMaxLifetime(0)
	return db, nil
}

// Open opens a GORM DB from a DSN string (e.g. LLMD_DATABASE_URL or default llmd.sqlite).
// SQLite paths use mattn/go-sqlite3 (CGO). See docs/DEVELOPING.md for build requirements.
//
// Supported forms:
//   - postgres://... or postgresql://...
//   - sqlite://PATH  (file path after the prefix)
//   - file:path?query  (sqlite via mattn/go-sqlite3, requires CGO)
//   - a bare path (treated as sqlite file on disk)
func Open(dsn string) (*gorm.DB, error) {
	s := strings.TrimSpace(dsn)
	if s == "" {
		return nil, fmt.Errorf("empty database dsn")
	}
	switch {
	case strings.HasPrefix(s, "postgres://"), strings.HasPrefix(s, "postgresql://"):
		db, err := gorm.Open(postgres.Open(s), &gorm.Config{
			Logger: gormlog.New(),
		})
		if err != nil {
			return nil, err
		}
		return db, nil
	case strings.HasPrefix(s, "sqlite://"):
		path := strings.TrimPrefix(s, "sqlite://")
		path = strings.TrimSpace(path)
		if path == "" {
			return nil, fmt.Errorf("sqlite:// DSN needs a path")
		}
		return openSQLite(path)
	default:
		// sqlite file DSN (e.g. llmd.db or file:memdb1?mode=memory&cache=shared)
		return openSQLite(s)
	}
}
