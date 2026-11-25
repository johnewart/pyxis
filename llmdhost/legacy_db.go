package llmdhost

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
)

// LegacyDatabase is one database found in a pre-tenant (workspace_id-keyed)
// schema that this llmd can no longer open.
type LegacyDatabase struct {
	// Kind is "llmd" (sessions, history, providers) or "knowledge" (code index).
	Kind string
	// DSN is the database DSN as configured.
	DSN string
	// Path is the on-disk SQLite file, or "" when the database is not a plain
	// file (e.g. Postgres) and so cannot be backed up automatically.
	Path string
}

// LegacyDatabaseError is returned by New when an existing database predates
// tenant/scope partitioning. Nothing has been migrated or modified when this is
// returned, and both databases are closed.
//
// llmd does not migrate this data forward (pre-1.0: the old rows carry no
// tenant, so there is no correct automatic mapping). The intended host flow:
//
//	h, err := llmdhost.New(cfg)
//	var legacy *llmdhost.LegacyDatabaseError
//	if errors.As(err, &legacy) {
//	    // Show legacy.Databases and the backup location; ask the user to confirm.
//	    moved, berr := llmdhost.BackupLegacyDatabases(legacy, backupDir)
//	    // Tell the user where `moved` went, then:
//	    h, err = llmdhost.New(cfg) // starts with fresh databases
//	}
type LegacyDatabaseError struct {
	Databases []LegacyDatabase
}

func (e *LegacyDatabaseError) Error() string {
	var parts []string
	for _, d := range e.Databases {
		loc := d.Path
		if loc == "" {
			loc = d.DSN
		}
		parts = append(parts, fmt.Sprintf("%s database %q", d.Kind, loc))
	}
	return fmt.Sprintf("%s uses the pre-tenant schema (workspace_id) and cannot be opened by this llmd; "+
		"back it up and start fresh (llmdhost.BackupLegacyDatabases), or move it aside manually",
		strings.Join(parts, " and "))
}

// BackedUpFile records one file moved by BackupLegacyDatabases.
type BackedUpFile struct {
	From string
	To   string
}

// BackupLegacyDatabases moves every legacy database file named in lerr (and its
// SQLite -wal / -shm / -journal siblings) into a new timestamped directory under
// backupDir, so the next New starts with fresh databases. Files are moved, not
// deleted; the returned list says exactly where each one went.
//
// It refuses (without moving anything) if any listed database is not a plain
// file, since that cannot be backed up by moving a file.
func BackupLegacyDatabases(lerr *LegacyDatabaseError, backupDir string) ([]BackedUpFile, error) {
	if lerr == nil || len(lerr.Databases) == 0 {
		return nil, nil
	}
	backupDir = strings.TrimSpace(backupDir)
	if backupDir == "" {
		return nil, errors.New("backup directory is required")
	}
	for _, d := range lerr.Databases {
		if d.Path == "" {
			return nil, fmt.Errorf("%s database %q is not a local SQLite file; back it up manually", d.Kind, d.DSN)
		}
	}
	dest := filepath.Join(backupDir, "llmd-pre-tenant-"+time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return nil, fmt.Errorf("create backup directory %q: %w", dest, err)
	}
	var moved []BackedUpFile
	for _, d := range lerr.Databases {
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			src := d.Path + suffix
			if _, err := os.Stat(src); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return moved, fmt.Errorf("stat %q: %w", src, err)
			}
			to := filepath.Join(dest, filepath.Base(src))
			if err := moveFile(src, to); err != nil {
				return moved, fmt.Errorf("move %q to %q: %w", src, to, err)
			}
			moved = append(moved, BackedUpFile{From: src, To: to})
		}
	}
	return moved, nil
}

// moveFile renames src to dst, falling back to copy+remove across filesystems.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		in.Close()
		return err
	}
	_, cpErr := io.Copy(out, in)
	in.Close()
	if cerr := out.Close(); cpErr == nil {
		cpErr = cerr
	}
	if cpErr != nil {
		_ = os.Remove(dst)
		return cpErr
	}
	return os.Remove(src)
}

// hasLegacyColumn reports whether table exists and still has a workspace_id
// column, i.e. predates tenant/scope partitioning.
func hasLegacyColumn(db *gorm.DB, table string) bool {
	m := db.Migrator()
	return m.HasTable(table) && m.HasColumn(table, "workspace_id")
}

// sqliteFilePath resolves the on-disk file for a SQLite DSN, or "" for
// in-memory or non-SQLite (Postgres) DSNs.
func sqliteFilePath(dsn string) string {
	s := strings.TrimSpace(dsn)
	if s == "" || strings.HasPrefix(s, "postgres://") || strings.HasPrefix(s, "postgresql://") {
		return ""
	}
	s = strings.TrimPrefix(s, "sqlite://")
	s = strings.TrimPrefix(s, "file:")
	query := ""
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s, query = s[:i], s[i+1:]
	}
	if s == "" || s == ":memory:" || strings.Contains(query, "mode=memory") {
		return ""
	}
	if abs, err := filepath.Abs(s); err == nil {
		return abs
	}
	return s
}

func closeGorm(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
