package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	schemaMigrationCollapseAgentIDToSessionID = 1
	schemaMigrationSessionFirstStorage        = 2
	schemaMigrationSessionNativeCollapse      = 3
	schemaMigrationProviderSessionSyncPK      = 4
	schemaMigrationChatSessionLookupIndex     = 5
	// 6 was schemaMigrationDropRegistryConnectionYAML, retired along with
	// GlobalProviderRow/the dead global model registry (see migrate.go's
	// legacy-table drop loop). Version numbers are historical record in
	// llmd_schema_migrations; kept reserved rather than reused.
	schemaMigrationDropLegacyUserAgentColumns       = 7
	schemaMigrationSessionListOrderIndex            = 8
	schemaMigrationSessionTurnsLookupIndex          = 9
	schemaMigrationBackfillSessionGraphUsageTurnSeq = 10
	schemaMigrationChatMessagesRecencyIndex         = 11
	schemaMigrationSessionSurfaceIndex              = 12
	schemaMigrationBackfillSessionLastMessageAt     = 13
	schemaMigrationDropRawChunksAndEventLookupIndex = 14
)

// staleAgentSessionFilter matches chat rows that should use agent_id = session_id.
// Utility one-shots omit session_id and are not persisted; rows with empty session_id are excluded.
const staleAgentSessionFilter = "session_id != '' AND agent_id != '' AND agent_id != session_id"

func runDataMigrations(db *gorm.DB, trace StepFunc) error {
	if db == nil {
		return nil
	}
	// Data migrations run once each, recorded in llmd_schema_migrations. Add
	// future ones here with new version ids. name is what a host's startup
	// tracing shows while the migration runs.
	steps := []struct {
		version int
		name    string
		fn      func(*gorm.DB) error
	}{
		{schemaMigrationBackfillSessionLastMessageAt, "backfill session last_message_at", migrateBackfillSessionLastMessageAt},
		{schemaMigrationDropRawChunksAndEventLookupIndex, "drop raw chunks table", migrateDropRawChunksAndEventLookupIndex},
	}
	for _, step := range steps {
		applied, err := isSchemaMigrationApplied(db, step.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		name := fmt.Sprintf("data migration %d: %s", step.version, step.name)
		if err := trace.Run(name, func() error {
			if err := step.fn(db); err != nil {
				return err
			}
			return recordSchemaMigration(db, step.version)
		}); err != nil {
			return err
		}
	}
	return nil
}

// legacyChatSchemaPresent reports whether the pre-consolidation chat schema is
// still in place (llmd_chat_messages carries the old agent_id column). It is the
// unambiguous marker that legacy data migrations are meaningful for this DB.
func legacyChatSchemaPresent(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	return db.Migrator().HasColumn(&ChatMessageRow{}, "agent_id")
}

func isSchemaMigrationApplied(db *gorm.DB, version int) (bool, error) {
	var count int64
	if err := db.Model(&SchemaMigrationRow{}).Where("version = ?", version).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func recordSchemaMigration(db *gorm.DB, version int) error {
	return db.Create(&SchemaMigrationRow{
		Version:   version,
		AppliedAt: time.Now().UTC(),
	}).Error
}

// migrateBackfillSessionLastMessageAt (data migration id 13) fills
// llmd_sessions.last_message_at from each session's newest message. It only
// touches NULL rows, so it is safe to run more than once.
func migrateBackfillSessionLastMessageAt(db *gorm.DB) error {
	return db.Exec(`UPDATE llmd_sessions SET last_message_at = (
  SELECT MAX(m.created_at) FROM llmd_chat_messages m
  WHERE m.tenant_id = llmd_sessions.tenant_id
    AND m.scope_id = llmd_sessions.scope_id
    AND m.session_id = llmd_sessions.session_id)
WHERE last_message_at IS NULL`).Error
}

// migrateDropRawChunksAndEventLookupIndex (data migration id 14) drops
// llmd_turn_raw_chunks (nothing writes it any more) and
// idx_session_events_lookup (identical to llmd_session_events' primary key).
func migrateDropRawChunksAndEventLookupIndex(db *gorm.DB) error {
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_turn_chunks_lookup`,
		`DROP TABLE IF EXISTS llmd_turn_raw_chunks`,
		`DROP INDEX IF EXISTS idx_session_events_lookup`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

// migrateBackfillSessionGraphUsageTurnSeq is data migration id 10. It used to
// delegate to a graph-owned backfill hook; SessionUsageRow is llmd-owned now
// and every row has always carried turn_seq, so there is nothing left to
// backfill. Kept as a recorded no-op step so llmd_schema_migrations ids never
// shift under an existing database.
func migrateBackfillSessionGraphUsageTurnSeq(db *gorm.DB) error {
	return nil
}

func migrateCollapseAgentIDToSessionID(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// Memories map old agent_id via chat rows that still carry the stale agent_id.
		if err := migrateAgentMemoryAgentIDs(tx); err != nil {
			return err
		}
		if err := migrateChatMessageAgentIDs(tx); err != nil {
			return err
		}
		return migrateProviderSessionSyncAgentIDs(tx)
	})
}

func migrateChatMessageAgentIDs(db *gorm.DB) error {
	res := db.Model(&ChatMessageRow{}).
		Where(staleAgentSessionFilter).
		Update("agent_id", gorm.Expr("session_id"))
	return res.Error
}

func migrateProviderSessionSyncAgentIDs(db *gorm.DB) error {
	var stale []ProviderSessionSyncRow
	if err := db.Where(staleAgentSessionFilter).Find(&stale).Error; err != nil {
		return err
	}
	for _, old := range stale {
		targetAgentID := strings.TrimSpace(old.SessionID)
		if targetAgentID == "" {
			continue
		}
		var existing ProviderSessionSyncRow
		err := db.Where(
			"user_id = ? AND agent_id = ? AND session_id = ? AND provider_id = ?",
			old.TenantID, targetAgentID, targetAgentID, old.ProviderID,
		).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			newRow := old
			newRow.AgentID = targetAgentID
			if err := db.Delete(&old).Error; err != nil {
				return err
			}
			if err := db.Create(&newRow).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		merged := mergeProviderSessionSyncRows(existing, old)
		merged.AgentID = targetAgentID
		if err := db.Delete(&old).Error; err != nil {
			return err
		}
		if err := db.Save(&merged).Error; err != nil {
			return err
		}
	}
	return nil
}

func mergeProviderSessionSyncRows(dst, src ProviderSessionSyncRow) ProviderSessionSyncRow {
	winner, loser := dst, src
	if src.SyncedMessageCount > dst.SyncedMessageCount {
		winner, loser = src, dst
	}
	out := winner
	if strings.TrimSpace(out.BackendSessionID) == "" && strings.TrimSpace(loser.BackendSessionID) != "" {
		out.BackendSessionID = loser.BackendSessionID
	}
	if strings.TrimSpace(out.SystemFingerprint) == "" && strings.TrimSpace(loser.SystemFingerprint) != "" {
		out.SystemFingerprint = loser.SystemFingerprint
	}
	out.UpdatedAt = time.Now().UTC()
	return out
}

func migrateAgentMemoryAgentIDs(db *gorm.DB) error {
	// The legacy table may already be gone (older builds dropped it before the
	// data migrations ran, leaving DBs where this step never got recorded as
	// applied). Nothing to map in that case — skip so the step can complete and
	// be recorded instead of failing on every boot.
	if !db.Migrator().HasTable("llmd_agent_memories") {
		return nil
	}
	// Best-effort: map memories from old random agent ids to the session_id on chat rows.
	return db.Exec(`
UPDATE llmd_agent_memories
SET agent_id = (
  SELECT m.session_id
  FROM llmd_chat_messages m
  WHERE m.user_id = llmd_agent_memories.user_id
    AND m.agent_id = llmd_agent_memories.agent_id
    AND m.session_id != ''
  ORDER BY m.created_at DESC
  LIMIT 1
)
WHERE agent_id != (
  SELECT m2.session_id
  FROM llmd_chat_messages m2
  WHERE m2.user_id = llmd_agent_memories.user_id
    AND m2.agent_id = llmd_agent_memories.agent_id
    AND m2.session_id != ''
  ORDER BY m2.created_at DESC
  LIMIT 1
)
AND EXISTS (
  SELECT 1 FROM llmd_chat_messages m3
  WHERE m3.user_id = llmd_agent_memories.user_id
    AND m3.agent_id = llmd_agent_memories.agent_id
    AND m3.session_id != ''
)`).Error
}

func migrateSessionFirstStorage(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := backfillSessionRows(tx); err != nil {
			return err
		}
		return rebuildProviderSessionSyncSessionPK(tx)
	})
}

func backfillSessionRows(db *gorm.DB) error {
	// Same guard as migrateAgentMemoryAgentIDs: llmd_agents may already have been
	// dropped by an older build, and there is nothing to backfill from if so.
	if !db.Migrator().HasTable("llmd_agents") {
		return nil
	}
	return db.Exec(`
INSERT OR IGNORE INTO llmd_sessions (user_id, session_id, provider_id, default_model, tools_json, created_at, updated_at)
SELECT a.user_id, a.id, a.provider_id, a.default_model, a.tools_json, a.created_at, a.updated_at
FROM llmd_agents a
WHERE EXISTS (
  SELECT 1 FROM llmd_chat_messages m
  WHERE m.user_id = a.user_id AND m.session_id = a.id AND m.session_id != ''
)`).Error
}

type providerSessionSyncKey struct {
	WorkspaceID string
	SessionID   string
	ProviderID  string
}

func dedupeProviderSessionSyncRows(rows []ProviderSessionSyncRow) map[providerSessionSyncKey]ProviderSessionSyncRow {
	merged := make(map[providerSessionSyncKey]ProviderSessionSyncRow, len(rows))
	for _, row := range rows {
		sid := strings.TrimSpace(row.SessionID)
		if sid == "" {
			continue
		}
		key := providerSessionSyncKey{WorkspaceID: row.TenantID, SessionID: sid, ProviderID: row.ProviderID}
		if existing, ok := merged[key]; ok {
			merged[key] = mergeProviderSessionSyncRows(existing, row)
			continue
		}
		row.SessionID = sid
		row.AgentID = sid
		merged[key] = row
	}
	return merged
}

func rebuildProviderSessionSyncSessionPK(db *gorm.DB) error {
	var rows []ProviderSessionSyncRow
	if err := db.Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	merged := dedupeProviderSessionSyncRows(rows)
	if err := db.Where("1 = 1").Delete(&ProviderSessionSyncRow{}).Error; err != nil {
		return err
	}
	for _, row := range merged {
		row.AgentID = row.SessionID
		if err := db.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateSessionNativeCollapse(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := backfillSessionRows(tx); err != nil {
			return err
		}
		// llmd_agents may already have been dropped by an older build; the rows
		// this would delete are gone with it.
		if !tx.Migrator().HasTable("llmd_agents") {
			return nil
		}
		return tx.Exec(`
DELETE FROM llmd_agents
WHERE EXISTS (
  SELECT 1 FROM llmd_chat_messages m
  WHERE m.user_id = llmd_agents.user_id
    AND m.session_id = llmd_agents.id
    AND m.session_id != ''
)`).Error
	})
}

// migrateChatSessionLookupIndex adds a session_id-leading index for client binding lookups.
func migrateChatSessionLookupIndex(db *gorm.DB) error {
	return db.Exec(`
CREATE INDEX IF NOT EXISTS idx_chat_session_lookup
  ON llmd_chat_messages (session_id, user_id)`).Error
}

// migrateSessionListOrderIndex adds a (workspace_id, updated_at) index so the
// session list can filter by workspace and order by recency without a scan.
func migrateSessionListOrderIndex(db *gorm.DB) error {
	return db.Exec(`
CREATE INDEX IF NOT EXISTS idx_sessions_scope_updated
  ON llmd_sessions (tenant_id, scope_id, updated_at)`).Error
}

// migrateSessionSurfaceIndex adds (workspace_id, chat_surface, updated_at) so a
// surface-filtered session list seeks instead of scanning.
//
// chat_surface is the most frequently used session-list filter, but no index led
// with it: idx_sessions_ws_updated is (workspace_id, updated_at), which serves
// the unfiltered recency list and can satisfy the workspace predicate, yet still
// has to examine every session in the workspace to test the surface. Ordering by
// updated_at is a common pairing with the filter, so it is the third column and
// the index covers filter-plus-order in one seek. It does not help the
// last_message_at ordering, which reads the stored llmd_sessions.last_message_at
// column.
func migrateSessionSurfaceIndex(db *gorm.DB) error {
	return db.Exec(`
CREATE INDEX IF NOT EXISTS idx_sessions_scope_surface_updated
  ON llmd_sessions (tenant_id, scope_id, chat_surface, updated_at)`).Error
}

// migrateChatMessagesRecencyIndex adds (workspace_id, session_id, created_at) so
// ListSessions' last-message-at aggregate (MAX(created_at) grouped by session)
// is served from the index instead of scanning every message row in the
// workspace. idx_chat_sess is (workspace_id, session_id, turn_id) and so cannot
// cover the aggregate; this index leaves it in place for the existing lookups.
func migrateChatMessagesRecencyIndex(db *gorm.DB) error {
	return db.Exec(`
CREATE INDEX IF NOT EXISTS idx_chat_messages_recency
  ON llmd_chat_messages (tenant_id, scope_id, session_id, created_at)`).Error
}

// migrateSessionTurnsLookupIndex adds a session_id-leading index for
// llmd_session_turns (the PK already leads with session_id, but an explicit
// index keeps TurnRegister.Bootstrap's MAX(turn_seq) lookup cheap regardless
// of dialect-specific PK indexing behavior).
func migrateSessionTurnsLookupIndex(db *gorm.DB) error {
	return db.Exec(`
CREATE INDEX IF NOT EXISTS idx_session_turns_lookup
  ON llmd_session_turns (tenant_id, scope_id, session_id, turn_seq)`).Error
}

// migrateDropLegacyUserAgentColumns removes the orphaned legacy user_id / agent_id
// columns left behind on session-native tables after the workspace_id consolidation.
// AutoMigrate added the new nullable workspace_id column but cannot drop the old
// (often NOT NULL) columns, so inserts that only populate workspace_id fail the
// stale user_id NOT NULL constraint. Drop them if present. ProviderSessionSyncRow
// and llmd_agents legitimately still carry user_id/agent_id and are excluded.
func migrateDropLegacyUserAgentColumns(db *gorm.DB) error {
	// The legacy user_id column is frequently part of a composite PRIMARY KEY
	// (e.g. llmd_sessions PK was (user_id, session_id)), which GORM's SQLite
	// DropColumn cannot rebuild (it copies the stale PK referencing user_id and
	// fails with "no such column: user_id"). Since beta session data is
	// discardable, drop and recreate any affected table from the current model
	// when the legacy user_id column is still present.
	models := []interface{}{
		&SessionRow{},
		&ChatMessageRow{},
		&TurnMetadataRow{},
		&ToolResultSidecarRow{},
		&SessionCacheRow{},
	}
	for _, m := range models {
		if !db.Migrator().HasColumn(m, "user_id") {
			continue
		}
		if err := db.Migrator().DropTable(m); err != nil {
			return err
		}
		if err := db.AutoMigrate(m); err != nil {
			return err
		}
	}
	return nil
}
