package store

import (
	"context"
	"database/sql/driver"
	"fmt"
	"github.com/johnewart/pyxis/internal/serverutil"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SessionRepo persists chat session runtime metadata (provider, model, tools).
type SessionRepo struct {
	db *gorm.DB
}

func NewSessionRepo(db *gorm.DB) *SessionRepo {
	return &SessionRepo{db: db}
}

func (r *SessionRepo) GetSession(ctx context.Context, sc serverutil.Scope, sessionID string) (*SessionRow, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("session repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return nil, fmt.Errorf("workspace_id and session_id are required")
	}
	var row SessionRow
	tx := r.db.WithContext(ctx).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).First(&row)
	if tx.Error != nil {
		return nil, tx.Error
	}
	return &row, nil
}

func (r *SessionRepo) UpsertSession(ctx context.Context, row SessionRow) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	row.UpdatedAt = time.Now().UTC()
	if row.CreatedAt.IsZero() {
		row.CreatedAt = row.UpdatedAt
	}
	// last_message_at is owned by the message write paths, never by a session save.
	return r.db.WithContext(ctx).Omit("last_message_at").Save(&row).Error
}

// refreshSessionLastMessageAt recomputes llmd_sessions.last_message_at for one
// session from its messages (an index-covered MAX for that session only). Call
// it in the same transaction as any llmd_chat_messages insert or delete.
func refreshSessionLastMessageAt(db *gorm.DB, sc serverutil.Scope, sessionID string) error {
	if db == nil || sessionID == "" {
		return nil
	}
	return db.Exec(`UPDATE llmd_sessions SET last_message_at = (
  SELECT MAX(m.created_at) FROM llmd_chat_messages m
  WHERE m.tenant_id = ? AND m.scope_id = ? AND m.session_id = ?)
WHERE tenant_id = ? AND scope_id = ? AND session_id = ?`,
		sc.TenantID, sc.ScopeID, sessionID, sc.TenantID, sc.ScopeID, sessionID).Error
}

// EnsureSessionRow inserts a new session row, or on conflict updates only the
// runtime fields (provider_id, default_model, tools_json) while preserving any
// compression settings that may have been written before the session was started.
// chat_surface, tags_json, disabled_tools_json and tool_policy are updated on
// conflict only when the incoming row carries a non-empty value (see
// keepIfBlank below).
func (r *SessionRepo) EnsureSessionRow(ctx context.Context, row SessionRow) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	now := time.Now().UTC()
	row.UpdatedAt = now
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	tbl := SessionRow{}.TableName()
	// keepIfBlank updates a column from the pending row only when the incoming
	// value is non-empty, otherwise leaving the stored value alone. `excluded` is
	// the pending row in both SQLite and Postgres.
	keepIfBlank := func(col string) clause.Expr {
		return gorm.Expr(fmt.Sprintf(
			"CASE WHEN excluded.%[1]s <> '' THEN excluded.%[1]s ELSE %[2]s.%[1]s END", col, tbl))
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "scope_id"}, {Name: "session_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"provider_id":   gorm.Expr("excluded.provider_id"),
			"default_model": gorm.Expr("excluded.default_model"),
			"tools_json":    gorm.Expr("excluded.tools_json"),
			"updated_at":    gorm.Expr("excluded.updated_at"),
			// chat_surface and tags_json were absent from this list, which meant
			// they only ever landed on the initial INSERT. Any session whose row
			// already existed — e.g. one created bare by CreateSessionRowIfMissing
			// when SetSessionMeta ran before the first EnsureSession — silently
			// discarded its surface on every subsequent ensure and kept an empty
			// chat_surface forever. They are assigned here, but conditionally: an
			// EnsureSession call that omits either field must not wipe what an
			// earlier call set, which is the same "only overwrite when provided"
			// rule SetSessionMeta applies to chat_surface.
			"chat_surface": keepIfBlank("chat_surface"),
			"tags_json":    keepIfBlank("tags_json"),
			// An EnsureSession that omits disabled_tools must not wipe a previously
			// stored set (same overwrite rule as system_prompt_sections).
			"disabled_tools_json": keepIfBlank("disabled_tools_json"),
			// Same rule, and load-bearing: a reconnect that omits tool_policy must
			// not silently downgrade a PROFILE_ONLY session to inherit-everything,
			// which would hand it every server-wide tool.
			"tool_policy": keepIfBlank("tool_policy"),
			// Same rule: an EnsureSession omitting tool_context keeps the stored map.
			"tool_context_json": keepIfBlank("tool_context_json"),
		}),
	}).Create(&row).Error
}

func (r *SessionRepo) UpdateSessionLlm(ctx context.Context, sc serverutil.Scope, sessionID, providerID, defaultModel string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return fmt.Errorf("workspace_id and session_id are required")
	}
	updates := map[string]any{
		"provider_id":   strings.TrimSpace(providerID),
		"default_model": strings.TrimSpace(defaultModel),
		"updated_at":    time.Now().UTC(),
	}
	tx := r.db.WithContext(ctx).Model(&SessionRow{}).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).Updates(updates)
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// UpdateSessionSystemPromptSections overwrites the session's persisted default
// system prompt sections wholesale (see EnsureSessionRequest.system_prompt_sections).
// Only called when a caller explicitly supplied a non-empty list — an ordinary
// idempotent EnsureSession call that omits the field must never wipe the
// existing default, so callers gate this themselves rather than relying on
// EnsureSessionRow's INSERT ... ON CONFLICT (which never touches this column).
func (r *SessionRepo) UpdateSessionSystemPromptSections(ctx context.Context, sc serverutil.Scope, sessionID, sectionsJSON string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return fmt.Errorf("workspace_id and session_id are required")
	}
	tx := r.db.WithContext(ctx).Model(&SessionRow{}).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).Updates(map[string]any{
		"system_prompt_sections_json": sectionsJSON,
		"updated_at":                  time.Now().UTC(),
	})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// UpdateSessionDisabledTools overwrites the session's persisted default tool
// exclusions wholesale (see EnsureSessionRequest.disabled_tools and
// SessionRow.DisabledToolsJSON; disabledJSON is the MarshalDisabledToolsJSON
// output, "" to clear). Only called when a caller explicitly supplied a
// non-empty list — an ordinary idempotent EnsureSession call that omits the
// field must never wipe the existing set, so the sessionmanager gates this
// itself. Returns gorm.ErrRecordNotFound when no such session row exists.
func (r *SessionRepo) UpdateSessionDisabledTools(ctx context.Context, sc serverutil.Scope, sessionID, disabledJSON string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return fmt.Errorf("workspace_id and session_id are required")
	}
	tx := r.db.WithContext(ctx).Model(&SessionRow{}).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).Updates(map[string]any{
		"disabled_tools_json": disabledJSON,
		"updated_at":          time.Now().UTC(),
	})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// UpdateSessionToolContext overwrites the session's persisted tool_context
// (MarshalToolContextJSON output). Only called when a caller explicitly
// supplied a non-empty map. Returns gorm.ErrRecordNotFound when no row exists.
func (r *SessionRepo) UpdateSessionToolContext(ctx context.Context, sc serverutil.Scope, sessionID, toolContextJSON string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return fmt.Errorf("workspace_id and session_id are required")
	}
	tx := r.db.WithContext(ctx).Model(&SessionRow{}).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).Updates(map[string]any{
		"tool_context_json": toolContextJSON,
		"updated_at":        time.Now().UTC(),
	})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// UpdateSessionToolPolicy overwrites the session's persisted tool policy (see
// EnsureSessionRequest.tool_policy and SessionRow.ToolPolicy; policy is the
// agentprofile.PolicyName spelling, "" for inherit-server-wide). Like
// UpdateSessionDisabledTools this is only called when a caller explicitly
// supplied a policy: an idempotent EnsureSession that omits it must leave the
// stored policy alone, so the sessionmanager gates the call. Returns
// gorm.ErrRecordNotFound when no such session row exists.
func (r *SessionRepo) UpdateSessionToolPolicy(ctx context.Context, sc serverutil.Scope, sessionID, policy string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return fmt.Errorf("workspace_id and session_id are required")
	}
	tx := r.db.WithContext(ctx).Model(&SessionRow{}).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).Updates(map[string]any{
		"tool_policy": strings.TrimSpace(policy),
		"updated_at":  time.Now().UTC(),
	})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *SessionRepo) SaveToolsJSON(ctx context.Context, sc serverutil.Scope, sessionID, toolsJSON string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return fmt.Errorf("workspace_id and session_id are required")
	}
	tx := r.db.WithContext(ctx).Model(&SessionRow{}).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).Updates(map[string]any{
		"tools_json": toolsJSON,
		"updated_at": time.Now().UTC(),
	})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *SessionRepo) DeleteSession(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return fmt.Errorf("workspace_id and session_id are required")
	}
	tx := r.db.WithContext(ctx).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).Delete(&SessionRow{})
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// PurgeSession removes persisted chat history, provider sync, memories, and the session row.
func (r *SessionRepo) PurgeSession(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return fmt.Errorf("workspace_id and session_id are required")
	}
	if err := RemoveAllChatForSession(r.db.WithContext(ctx), sc, sid); err != nil {
		return err
	}
	if err := NewTrackedDocumentRepo(r.db).DeleteBySession(ctx, sc, sid); err != nil {
		return err
	}
	if err := scoped(r.db.WithContext(ctx), sc).Where("session_id = ?", sid).Delete(&ProviderSessionSyncRow{}).Error; err != nil {
		return err
	}
	if err := r.PurgeDerivedRows(ctx, sc, sid); err != nil {
		return err
	}
	// Events, turn register, queue and drafts are only meaningful with the
	// session itself, so they go on purge but survive a history replace.
	for _, m := range []any{&SessionEventRow{}, &SessionTurnRow{}, &SessionQueuedMessageRow{}, &SessionDraftRow{}} {
		if err := scoped(r.db.WithContext(ctx), sc).Where("session_id = ?", sid).Delete(m).Error; err != nil {
			return err
		}
	}
	// Usage and tool-usage rows are accounting (spend history) and are kept
	// after a session is deleted.
	return r.DeleteSession(ctx, sc, sid)
}

// PurgeDerivedRows removes the per-session rows derived from chat history:
// turn metadata, tool result sidecars, session cache and turn decorations.
// It is the one list shared by PurgeSession and ReplaceSessionHistory.
func (r *SessionRepo) PurgeDerivedRows(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	for _, m := range []any{&TurnMetadataRow{}, &ToolResultSidecarRow{}, &SessionCacheRow{}, &TurnDecorationRow{}} {
		if err := scoped(r.db.WithContext(ctx), sc).Where("session_id = ?", sessionID).Delete(m).Error; err != nil {
			return err
		}
	}
	return nil
}

// CreateSessionRowIfMissing inserts a bare session row (workspace_id, session_id,
// timestamps) when one does not already exist, so metadata upserts can target it.
// A no-op when the row is already present.
func (r *SessionRepo) CreateSessionRowIfMissing(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return fmt.Errorf("workspace_id and session_id are required")
	}
	now := time.Now().UTC()
	row := SessionRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, CreatedAt: now, UpdatedAt: now}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

// UpdateSession updates arbitrary session fields (title, todos_json, attributes_json, cursor_pos, etc.)
func (r *SessionRepo) UpdateSession(ctx context.Context, sc serverutil.Scope, sessionID string, updates map[string]interface{}) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return fmt.Errorf("workspace_id and session_id are required")
	}
	// Always update the updated_at timestamp
	updates["updated_at"] = time.Now().UTC()
	tx := r.db.WithContext(ctx).Model(&SessionRow{}).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).Updates(updates)
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// NullTimestamp scans a nullable timestamp that may arrive without a declared
// column type.
//
// The SQLite driver decides whether to hand back a time.Time by looking at the
// column's *declared* type. A computed column — MAX(created_at) inside the
// derived table ListSessions joins — has no declared type, so the driver skips
// the conversion and yields the raw stored string; scanning that into a
// *time.Time fails outright. Postgres returns a real time.Time for the same
// query. This type accepts both, so the projection is dialect-portable.
type NullTimestamp struct {
	Time  time.Time
	Valid bool
}

// sqliteTimestampLayouts mirrors mattn/go-sqlite3's SQLiteTimestampFormats —
// the formats it will have used when writing the value we are now reading back
// untyped.
var sqliteTimestampLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
}

func (n *NullTimestamp) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		n.Time, n.Valid = time.Time{}, false
		return nil
	case time.Time:
		n.Time, n.Valid = v, true
		return nil
	case []byte:
		return n.scanString(string(v))
	case string:
		return n.scanString(v)
	default:
		return fmt.Errorf("cannot scan %T into NullTimestamp", src)
	}
}

// Value completes the Valuer/Scanner pair. GORM refuses to treat a struct field
// as a plain column unless both halves are implemented (it otherwise reads it as
// a relation). NullTimestamp is only ever read back from a computed column, so
// this exists for GORM's benefit rather than for writes.
func (n NullTimestamp) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Time, nil
}

func (n *NullTimestamp) scanString(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		n.Time, n.Valid = time.Time{}, false
		return nil
	}
	// SQLite stores these UTC-normalized; parse in UTC so a layout without a
	// zone does not silently pick up the process's local offset.
	for _, layout := range sqliteTimestampLayouts {
		if ts, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			n.Time, n.Valid = ts.UTC(), true
			return nil
		}
	}
	return fmt.Errorf("cannot parse timestamp %q", s)
}

// SessionSortField selects the ORDER BY column for ListSessions.
type SessionSortField int

const (
	// SessionSortLastMessageAt orders by the newest chat message in the session.
	// Sessions with no messages yet fall back to their created_at so they sort
	// by when they appeared rather than sinking to the bottom on a NULL (which
	// SQLite and Postgres order differently anyway).
	SessionSortLastMessageAt SessionSortField = iota
	SessionSortCreatedAt
	SessionSortUpdatedAt
	SessionSortTitle
)

// Page bounds for ListSessions. A request over the max is clamped, not rejected.
const (
	DefaultSessionPageSize = 50
	MaxSessionPageSize     = 500
)

// ListSessionsOptions controls ordering, paging and filtering of the session list.
type ListSessionsOptions struct {
	SortBy     SessionSortField
	Descending bool
	// Limit <= 0 means DefaultSessionPageSize; above MaxSessionPageSize is clamped.
	Limit  int
	Offset int
	// ChatSurface, when non-empty, restricts the list to one surface.
	ChatSurface string
	// Tags, when non-empty, restricts the list to sessions carrying ALL of these
	// tags (AND). A tag whose value is empty matches on key presence, any value.
	Tags map[string]string
}

// SessionSummaryRow is the ListSessions projection: the cheap summary columns of
// llmd_sessions plus the derived last-message timestamp. Deliberately not
// SessionRow — SELECT * dragged tools_json/todos_json/attributes_json
// (potentially 100KB+ per row) out of the database for every session on every
// list, and the gRPC SessionSummary uses none of them. On a few hundred sessions
// that was the difference between msec and near-second list latency.
type SessionSummaryRow struct {
	SessionID   string
	Title       string
	ChatSurface string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CursorPos   int64
	// LastMessageAt is invalid (Valid == false) for a session with no messages.
	LastMessageAt NullTimestamp
	// TagsJSON is the raw tags column; decode with DecodeSessionTags. Kept raw in
	// the projection so the scan stays a plain string copy, with the JSON parse
	// happening once per row at the caller's boundary rather than inside GORM.
	TagsJSON string
	// Fork lineage (see SessionRow.ForkedFromSessionID); empty when not forked.
	ForkedFromSessionID string
	ForkedFromMessageID string
}

// sessionOrderClause builds the ORDER BY for ListSessions. Every component is
// derived from typed options (never caller text), so this is not an injection
// surface. session_id is the tiebreaker so paging over equal sort keys is stable.
func sessionOrderClause(opts ListSessionsOptions) string {
	dir := "ASC"
	if opts.Descending {
		dir = "DESC"
	}
	switch opts.SortBy {
	case SessionSortCreatedAt:
		return fmt.Sprintf("s.created_at %s, s.session_id ASC", dir)
	case SessionSortUpdatedAt:
		return fmt.Sprintf("s.updated_at %s, s.session_id ASC", dir)
	case SessionSortTitle:
		return fmt.Sprintf("LOWER(s.title) %s, s.session_id ASC", dir)
	default:
		return fmt.Sprintf("COALESCE(s.last_message_at, s.created_at) %s, s.session_id ASC", dir)
	}
}

// ListSessions returns one page of session summaries for a workspace plus the
// total number of rows matching the filter (ignoring the page bounds).
//
// last_message_at is the stored, nullable llmd_sessions.last_message_at column.
// refreshSessionLastMessageAt keeps it current when messages are inserted or
// deleted, and the backfill data migration fills it in for old databases. The
// default order is COALESCE(s.last_message_at, s.created_at); there is no
// aggregate or join over llmd_chat_messages.
//
// A chat_surface filter is served by idx_sessions_ws_surface_updated
// (workspace_id, chat_surface, updated_at), which covers filter-plus-recency in
// one seek. A Tags filter has no index and scans this workspace's sessions,
// applying per-dialect JSON extraction per row — an accepted cost, as tag
// filtering is rare next to surface filtering (see SessionRow.TagsJSON).
//
// Ordering by created_at/updated_at is served by idx_sessions_ws_updated;
// ordering by last_message_at reads the stored column on llmd_sessions.
func (r *SessionRepo) ListSessions(ctx context.Context, sc serverutil.Scope, opts ListSessionsOptions) ([]SessionSummaryRow, int64, error) {
	if r == nil || r.db == nil {
		return nil, 0, fmt.Errorf("session repo not configured")
	}
	if sc.TenantID == "" {
		return nil, 0, fmt.Errorf("workspace_id is required")
	}
	surface := strings.TrimSpace(opts.ChatSurface)

	countQ := r.db.WithContext(ctx).Model(&SessionRow{}).Where("tenant_id = ? AND scope_id = ?", sc.TenantID, sc.ScopeID)
	if surface != "" {
		countQ = countQ.Where("chat_surface = ?", surface)
	}
	// Unaliased column here: Model(&SessionRow{}) queries the table directly.
	countQ, err := applySessionTagFilters(r.db, countQ, "tags_json", opts.Tags)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultSessionPageSize
	}
	if limit > MaxSessionPageSize {
		limit = MaxSessionPageSize
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	q := r.db.WithContext(ctx).
		Table(SessionRow{}.TableName()+" AS s").
		Select("s.session_id, s.title, s.chat_surface, s.created_at, s.updated_at, s.cursor_pos, s.tags_json, s.forked_from_session_id, s.forked_from_message_id, s.last_message_at").
		Where("s.tenant_id = ? AND s.scope_id = ?", sc.TenantID, sc.ScopeID)
	if surface != "" {
		q = q.Where("s.chat_surface = ?", surface)
	}
	q, err = applySessionTagFilters(r.db, q, "s.tags_json", opts.Tags)
	if err != nil {
		return nil, 0, err
	}

	var rows []SessionSummaryRow
	if err := q.Order(sessionOrderClause(opts)).Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
