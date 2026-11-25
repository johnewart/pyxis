package store

import (
	"context"
	"errors"
	"github.com/johnewart/pyxis/internal/serverutil"
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// TestEnsureSessionRow_preservesDisabledToolsOnConflict mirrors the chat_surface
// regression test: disabled_tools_json must land through the ON CONFLICT path,
// survive a later ensure that omits it, and be replaced by one that supplies it.
func TestEnsureSessionRow_preservesDisabledToolsOnConflict(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	const ws, sid = "ws-dt", "sess-dt"

	// 1. Row exists bare first; the conflict path must write the column.
	if err := repo.CreateSessionRowIfMissing(ctx, serverutil.Scope{TenantID: ws}, sid); err != nil {
		t.Fatalf("CreateSessionRowIfMissing: %v", err)
	}
	if err := repo.EnsureSessionRow(ctx, SessionRow{
		TenantID: ws, SessionID: sid, ProviderID: "p1", DisabledToolsJSON: `["probe_tool"]`,
	}); err != nil {
		t.Fatalf("EnsureSessionRow: %v", err)
	}
	row, err := repo.GetSession(ctx, serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if row.DisabledToolsJSON != `["probe_tool"]` {
		t.Fatalf("disabled_tools_json not persisted through conflict: got %q", row.DisabledToolsJSON)
	}

	// 2. A later ensure that omits the field must not wipe it.
	if err := repo.EnsureSessionRow(ctx, SessionRow{
		TenantID: ws, SessionID: sid, ProviderID: "p2",
	}); err != nil {
		t.Fatalf("EnsureSessionRow (blank): %v", err)
	}
	row, err = repo.GetSession(ctx, serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("GetSession after blank ensure: %v", err)
	}
	if row.DisabledToolsJSON != `["probe_tool"]` {
		t.Fatalf("blank ensure wiped disabled_tools_json: got %q", row.DisabledToolsJSON)
	}
	if row.ProviderID != "p2" {
		t.Fatalf("provider_id should still update: got %q", row.ProviderID)
	}

	// 3. A non-blank value replaces the stored one.
	if err := repo.EnsureSessionRow(ctx, SessionRow{
		TenantID: ws, SessionID: sid, ProviderID: "p2", DisabledToolsJSON: `["other"]`,
	}); err != nil {
		t.Fatalf("EnsureSessionRow (replace): %v", err)
	}
	row, err = repo.GetSession(ctx, serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("GetSession after replace: %v", err)
	}
	if row.DisabledToolsJSON != `["other"]` {
		t.Fatalf("non-blank ensure did not replace disabled_tools_json: got %q", row.DisabledToolsJSON)
	}
}

// TestUpdateSessionDisabledTools_WritesAndNotFound proves the wholesale update:
// it sets the column, "" clears it, and an unknown session maps to
// gorm.ErrRecordNotFound.
func TestUpdateSessionDisabledTools_WritesAndNotFound(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	const ws, sid = "ws-upd", "sess-upd"

	if err := repo.EnsureSessionRow(ctx, SessionRow{TenantID: ws, SessionID: sid, ProviderID: "p1"}); err != nil {
		t.Fatalf("EnsureSessionRow: %v", err)
	}

	encoded, err := MarshalDisabledToolsJSON([]string{"search", "run_command"})
	if err != nil {
		t.Fatalf("MarshalDisabledToolsJSON: %v", err)
	}
	if err := repo.UpdateSessionDisabledTools(ctx, serverutil.Scope{TenantID: ws}, sid, encoded); err != nil {
		t.Fatalf("UpdateSessionDisabledTools: %v", err)
	}
	row, err := repo.GetSession(ctx, serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	got, err := UnmarshalDisabledToolsJSON(row.DisabledToolsJSON)
	if err != nil {
		t.Fatalf("UnmarshalDisabledToolsJSON(%q): %v", row.DisabledToolsJSON, err)
	}
	if want := []string{"run_command", "search"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("disabled tools after update: got %v, want %v", got, want)
	}

	// Writing "" clears the set.
	if err := repo.UpdateSessionDisabledTools(ctx, serverutil.Scope{TenantID: ws}, sid, ""); err != nil {
		t.Fatalf("UpdateSessionDisabledTools(clear): %v", err)
	}
	row, err = repo.GetSession(ctx, serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("GetSession after clear: %v", err)
	}
	if row.DisabledToolsJSON != "" {
		t.Fatalf("clear did not empty disabled_tools_json: got %q", row.DisabledToolsJSON)
	}

	// Unknown session → gorm.ErrRecordNotFound.
	err = repo.UpdateSessionDisabledTools(ctx, serverutil.Scope{TenantID: ws}, "no-such-session", `["x"]`)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unknown session: got %v, want gorm.ErrRecordNotFound", err)
	}
}

// TestEnsureSessionRow_PreservesCompressionSettings proves the bug fix: when a
// compression stub row is written before the first turn, the subsequent
// EnsureSessionRow (which creates the real session row) must NOT clobber the
// compression columns back to NULL.
func TestEnsureSessionRow_PreservesCompressionSettings(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	uid := "user-1"
	sid := "sess-1"

	// 1. Simulate UpdateSessionCompressionSettings writing a stub before any turn.
	stub := SessionRow{
		TenantID:                       uid,
		SessionID:                      sid,
		ToolResultCompressionEnabled:   proto.Bool(true),
		ToolResultMinifyDedup:          proto.Bool(true),
		ToolResultCompressionThreshold: proto.Int64(200),
		ToolResultAgeKeep:              proto.Int64(4),
	}
	if err := repo.UpsertSession(ctx, stub); err != nil {
		t.Fatalf("UpsertSession stub: %v", err)
	}

	// 2. Simulate the first turn's EnsureSession creating the real runtime row.
	if err := repo.EnsureSessionRow(ctx, SessionRow{
		TenantID:     uid,
		SessionID:    sid,
		ProviderID:   "anthropic",
		DefaultModel: "claude-sonnet-4",
		ToolsJSON:    "[]",
	}); err != nil {
		t.Fatalf("EnsureSessionRow: %v", err)
	}

	// 3. Compression settings must survive, runtime fields must be set.
	got, err := repo.GetSession(ctx, serverutil.Scope{TenantID: uid}, sid)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ToolResultCompressionEnabled == nil || !*got.ToolResultCompressionEnabled {
		t.Errorf("compression enabled not preserved: got %v", got.ToolResultCompressionEnabled)
	}
	if got.ToolResultMinifyDedup == nil || !*got.ToolResultMinifyDedup {
		t.Errorf("compression minify-dedup not preserved: got %v", got.ToolResultMinifyDedup)
	}
	if got.ToolResultCompressionThreshold == nil || *got.ToolResultCompressionThreshold != 200 {
		t.Errorf("compression threshold not preserved: got %v", got.ToolResultCompressionThreshold)
	}
	if got.ToolResultAgeKeep == nil || *got.ToolResultAgeKeep != 4 {
		t.Errorf("age-keep not preserved: got %v", got.ToolResultAgeKeep)
	}
	if got.ProviderID != "anthropic" {
		t.Errorf("provider not set: got %q", got.ProviderID)
	}
	if got.DefaultModel != "claude-sonnet-4" {
		t.Errorf("default model not set: got %q", got.DefaultModel)
	}
}

// TestEnsureSessionRow_UpdatesRuntimeFieldsOnConflict proves EnsureSessionRow
// refreshes provider/model/tools when called repeatedly for the same session.
func TestEnsureSessionRow_UpdatesRuntimeFieldsOnConflict(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	uid := "user-2"
	sid := "sess-2"

	if err := repo.EnsureSessionRow(ctx, SessionRow{
		TenantID: uid, SessionID: sid, ProviderID: "p1", DefaultModel: "m1", ToolsJSON: "[]",
	}); err != nil {
		t.Fatalf("first EnsureSessionRow: %v", err)
	}
	if err := repo.EnsureSessionRow(ctx, SessionRow{
		TenantID: uid, SessionID: sid, ProviderID: "p2", DefaultModel: "m2", ToolsJSON: "[1]",
	}); err != nil {
		t.Fatalf("second EnsureSessionRow: %v", err)
	}

	got, err := repo.GetSession(ctx, serverutil.Scope{TenantID: uid}, sid)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ProviderID != "p2" || got.DefaultModel != "m2" || got.ToolsJSON != "[1]" {
		t.Errorf("runtime fields not updated on conflict: %+v", got)
	}
}
