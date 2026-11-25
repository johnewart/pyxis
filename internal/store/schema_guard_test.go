package store

import (
	"strings"
	"testing"

	"gorm.io/gorm"
)

// TestSchemaGuard_noLegacyWorkspaceNames fails if any core table carries a
// workspace_id/user_id column, a keyed agent_id column, or an index whose name
// mentions ws_/workspace. It also asserts the four tenant-led lookup indexes.
func TestSchemaGuard_noLegacyWorkspaceNames(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	m := db.Migrator()

	type idxInfo struct {
		table   string
		columns []string
	}
	indexes := map[string]idxInfo{}

	for _, model := range CoreModels {
		stmtTable := tableNameOf(t, db, model)
		cols, err := m.ColumnTypes(model)
		if err != nil {
			t.Fatalf("%s: column types: %v", stmtTable, err)
		}
		for _, c := range cols {
			name := strings.ToLower(c.Name())
			switch name {
			case "workspace_id", "user_id":
				t.Errorf("%s: legacy column %q present", stmtTable, name)
			case "agent_id":
				if pk, ok := c.PrimaryKey(); ok && pk {
					t.Errorf("%s: agent_id is part of the primary key", stmtTable)
				}
			}
		}
		idxs, err := m.GetIndexes(model)
		if err != nil {
			t.Fatalf("%s: get indexes: %v", stmtTable, err)
		}
		for _, ix := range idxs {
			n := strings.ToLower(ix.Name())
			if strings.Contains(n, "ws_") || strings.Contains(n, "workspace") {
				t.Errorf("%s: legacy index name %q", stmtTable, ix.Name())
			}
			for _, col := range ix.Columns() {
				if strings.EqualFold(col, "agent_id") {
					if u, ok := ix.Unique(); ok && u {
						t.Errorf("%s: agent_id is part of unique key %q", stmtTable, ix.Name())
					}
				}
			}
			indexes[ix.Name()] = idxInfo{table: stmtTable, columns: ix.Columns()}
		}
	}

	want := map[string][]string{
		"idx_sessions_updated":         {"tenant_id", "scope_id", "updated_at"},
		"idx_sessions_surface_updated": {"tenant_id", "scope_id", "chat_surface", "updated_at"},
		"idx_chat_messages_recency":    {"tenant_id", "scope_id", "session_id", "created_at"},
		"idx_session_turns_lookup":     {"tenant_id", "scope_id", "session_id", "turn_seq"},
		"idx_chat_messages_kind":       {"tenant_id", "kind", "scope_id"},
	}
	for name, cols := range want {
		got, ok := indexes[name]
		if !ok {
			t.Errorf("required index %q missing", name)
			continue
		}
		if strings.Join(got.columns, ",") != strings.Join(cols, ",") {
			t.Errorf("index %q columns=%v want %v", name, got.columns, cols)
		}
	}
}

func tableNameOf(t *testing.T, db *gorm.DB, model any) string {
	t.Helper()
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(model); err != nil {
		t.Fatalf("parse model %T: %v", model, err)
	}
	return stmt.Schema.Table
}
