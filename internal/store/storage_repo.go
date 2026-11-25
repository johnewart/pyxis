package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

// StorageRepo reports and reclaims the database's disk use.
type StorageRepo struct {
	db *gorm.DB
}

func NewStorageRepo(db *gorm.DB) *StorageRepo { return &StorageRepo{db: db} }

// TableStorage is one table's size including its indexes.
type TableStorage struct {
	Name  string
	Bytes int64
}

// SessionStorage is one session's stored size.
type SessionStorage struct {
	SessionID     string
	ScopeID       string
	Title         string
	ChatSurface   string
	MessageCount  int64
	Bytes         int64
	LastMessageMs int64
}

// StorageStats is the result of Stats. FreeBytes is 0 where the database
// cannot report reclaimable space (Postgres).
type StorageStats struct {
	DatabaseBytes   int64
	FreeBytes       int64
	Tables          []TableStorage
	SessionCount    int64
	LargestSessions []SessionStorage
}

func (r *StorageRepo) isSQLite() bool { return r.db.Dialector.Name() == "sqlite" }

// DatabaseBytes returns the database size (file size on SQLite).
func (r *StorageRepo) DatabaseBytes(ctx context.Context) (int64, error) {
	db := r.db.WithContext(ctx)
	var n int64
	if r.isSQLite() {
		var pages, pageSize int64
		if err := db.Raw("PRAGMA page_count").Scan(&pages).Error; err != nil {
			return 0, fmt.Errorf("page_count: %w", err)
		}
		if err := db.Raw("PRAGMA page_size").Scan(&pageSize).Error; err != nil {
			return 0, fmt.Errorf("page_size: %w", err)
		}
		return pages * pageSize, nil
	}
	if err := db.Raw("SELECT pg_database_size(current_database())").Scan(&n).Error; err != nil {
		return 0, fmt.Errorf("pg_database_size: %w", err)
	}
	return n, nil
}

// Stats reports sizes. Sessions are limited to the scope's tenant.
func (r *StorageRepo) Stats(ctx context.Context, sc serverutil.Scope, limit int) (*StorageStats, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("storage repo not configured")
	}
	if sc.TenantID == "" {
		return nil, fmt.Errorf("tenant is required")
	}
	if limit <= 0 {
		limit = 20
	}
	db := r.db.WithContext(ctx)
	out := &StorageStats{}
	var err error
	if out.DatabaseBytes, err = r.DatabaseBytes(ctx); err != nil {
		return nil, err
	}
	if r.isSQLite() {
		var free, pageSize int64
		if err := db.Raw("PRAGMA freelist_count").Scan(&free).Error; err != nil {
			return nil, fmt.Errorf("freelist_count: %w", err)
		}
		if err := db.Raw("PRAGMA page_size").Scan(&pageSize).Error; err != nil {
			return nil, fmt.Errorf("page_size: %w", err)
		}
		out.FreeBytes = free * pageSize
		if out.Tables, err = r.sqliteTables(ctx, pageSize); err != nil {
			return nil, err
		}
	} else {
		if err := db.Raw(`SELECT tablename AS name, pg_total_relation_size(quote_ident(tablename)) AS bytes
FROM pg_tables WHERE schemaname = current_schema() AND tablename LIKE 'llmd\_%' ORDER BY bytes DESC`).Scan(&out.Tables).Error; err != nil {
			return nil, fmt.Errorf("table sizes: %w", err)
		}
	}
	if err := db.Raw("SELECT COUNT(*) FROM llmd_sessions WHERE tenant_id = ?", sc.TenantID).Scan(&out.SessionCount).Error; err != nil {
		return nil, fmt.Errorf("session count: %w", err)
	}
	type row struct {
		SessionID     string
		ScopeID       string
		Title         string
		ChatSurface   string
		MessageCount  int64
		Bytes         int64
		LastMessageMs int64
		LastMessageAt *time.Time
	}
	var rows []row
	// Stored bytes of a session = the length of its message contents.
	if err := db.Raw(`SELECT s.session_id AS session_id, s.scope_id AS scope_id, s.title AS title, s.chat_surface AS chat_surface,
  COUNT(m.id) AS message_count, COALESCE(SUM(LENGTH(m.content)), 0) AS bytes,
  s.last_message_at AS last_message_at
FROM llmd_sessions s
LEFT JOIN llmd_chat_messages m ON m.tenant_id = s.tenant_id AND m.scope_id = s.scope_id AND m.session_id = s.session_id
WHERE s.tenant_id = ?
GROUP BY s.tenant_id, s.scope_id, s.session_id, s.title, s.chat_surface, s.last_message_at
ORDER BY bytes DESC, s.session_id ASC
LIMIT ?`, sc.TenantID, limit).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("largest sessions: %w", err)
	}
	for _, x := range rows {
		if x.LastMessageAt != nil {
			x.LastMessageMs = x.LastMessageAt.UnixMilli()
		}
		out.LargestSessions = append(out.LargestSessions, SessionStorage{
			SessionID: x.SessionID, ScopeID: x.ScopeID, Title: x.Title, ChatSurface: x.ChatSurface,
			MessageCount: x.MessageCount, Bytes: x.Bytes, LastMessageMs: x.LastMessageMs,
		})
	}
	return out, nil
}

// sqliteTables sizes each table with its indexes from dbstat. Builds of the
// SQLite driver without dbstat fall back to the summed length of each table's
// columns, with an empty table counted as one page.
func (r *StorageRepo) sqliteTables(ctx context.Context, pageSize int64) ([]TableStorage, error) {
	db := r.db.WithContext(ctx)
	var tables []TableStorage
	err := db.Raw(`SELECT m.tbl_name AS name, SUM(d.pgsize) AS bytes
FROM dbstat d JOIN sqlite_master m ON m.name = d.name
WHERE m.type IN ('table','index') AND m.tbl_name NOT LIKE 'sqlite\_%' ESCAPE '\'
GROUP BY m.tbl_name ORDER BY bytes DESC`).Scan(&tables).Error
	if err == nil {
		return tables, nil
	}
	if !strings.Contains(err.Error(), "no such table") {
		return nil, fmt.Errorf("dbstat: %w", err)
	}
	var names []string
	if err := db.Raw(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\'`).Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	for _, name := range names {
		var cols []string
		if err := db.Raw("SELECT name FROM pragma_table_info(?)", name).Scan(&cols).Error; err != nil {
			return nil, fmt.Errorf("columns of %s: %w", name, err)
		}
		if len(cols) == 0 {
			continue
		}
		parts := make([]string, len(cols))
		for i, c := range cols {
			parts[i] = `COALESCE(LENGTH("` + c + `"), 0)`
		}
		var n int64
		q := `SELECT COALESCE(SUM(` + strings.Join(parts, "+") + `), 0) FROM "` + name + `"`
		if err := db.Raw(q).Scan(&n).Error; err != nil {
			return nil, fmt.Errorf("size of %s: %w", name, err)
		}
		if n < pageSize {
			n = pageSize
		}
		tables = append(tables, TableStorage{Name: name, Bytes: n})
	}
	for i := 1; i < len(tables); i++ {
		for j := i; j > 0 && tables[j].Bytes > tables[j-1].Bytes; j-- {
			tables[j], tables[j-1] = tables[j-1], tables[j]
		}
	}
	return tables, nil
}

// SessionRef identifies one session across tenants.
type SessionRef struct {
	Scope     serverutil.Scope
	SessionID string
}

// SessionsWithLeftoverEvents lists every session (all tenants) that has more
// than one event row, i.e. that a trim to the newest event would shrink.
// Callers trim each one separately so they can skip sessions with a running turn.
func (r *StorageRepo) SessionsWithLeftoverEvents(ctx context.Context) ([]SessionRef, error) {
	var rows []struct {
		TenantID  string
		ScopeID   string
		SessionID string
	}
	err := r.db.WithContext(ctx).Raw(`SELECT tenant_id, scope_id, session_id FROM llmd_session_events
GROUP BY tenant_id, scope_id, session_id HAVING COUNT(*) > 1`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list sessions with leftover events: %w", err)
	}
	out := make([]SessionRef, len(rows))
	for i, row := range rows {
		out[i] = SessionRef{Scope: serverutil.Scope{TenantID: row.TenantID, ScopeID: row.ScopeID}, SessionID: row.SessionID}
	}
	return out, nil
}

// Vacuum rewrites the database to return free pages to the filesystem.
func (r *StorageRepo) Vacuum(ctx context.Context) error {
	return r.db.WithContext(ctx).Exec("VACUUM").Error
}
