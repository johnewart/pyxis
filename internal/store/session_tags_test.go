package store

import (
	"context"
	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"
)

// TestEnsureSessionRow_persistsChatSurfaceOnConflict is a regression test for the
// bug that made chat_surface look like it never persisted: EnsureSessionRow's
// ON CONFLICT clause updated only provider_id/default_model/tools_json/updated_at,
// so the surface landed on the initial INSERT and nowhere else. Any session whose
// row already existed — the normal case once CreateSessionRowIfMissing has run —
// silently dropped it.
func TestEnsureSessionRow_persistsChatSurfaceOnConflict(t *testing.T) {
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	const ws, sid = "ws-1", "sess-1"

	// The row exists first and has no surface, exactly as SetSessionMeta on an
	// unprovisioned session would leave it.
	if err := repo.CreateSessionRowIfMissing(ctx, serverutil.Scope{TenantID: ws}, sid); err != nil {
		t.Fatalf("CreateSessionRowIfMissing: %v", err)
	}
	if err := repo.EnsureSessionRow(ctx, SessionRow{
		TenantID: ws, SessionID: sid, ChatSurface: "build", ProviderID: "p1",
	}); err != nil {
		t.Fatalf("EnsureSessionRow: %v", err)
	}

	row, err := repo.GetSession(ctx, serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if row.ChatSurface != "build" {
		t.Fatalf("chat_surface not persisted through conflict: got %q, want %q", row.ChatSurface, "build")
	}

	// A later ensure that omits the surface must not wipe it.
	if err := repo.EnsureSessionRow(ctx, SessionRow{
		TenantID: ws, SessionID: sid, ProviderID: "p2",
	}); err != nil {
		t.Fatalf("EnsureSessionRow (no surface): %v", err)
	}
	row, err = repo.GetSession(ctx, serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("GetSession after blank ensure: %v", err)
	}
	if row.ChatSurface != "build" {
		t.Fatalf("blank ensure wiped chat_surface: got %q", row.ChatSurface)
	}
	if row.ProviderID != "p2" {
		t.Fatalf("provider_id should still update: got %q", row.ProviderID)
	}
}

// TestAutoMigrate_addsSessionSurfaceIndex pins the index behind the session
// list's most common filter. Without it a surface-filtered list examines every
// session in the workspace.
func TestAutoMigrate_addsSessionSurfaceIndex(t *testing.T) {
	db := NewTestSQLite(t) // runs AutoMigrate
	var names []string
	if err := db.Raw(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'llmd_sessions'`,
	).Scan(&names).Error; err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	for _, n := range names {
		if n == "idx_sessions_surface_updated" {
			return
		}
	}
	t.Fatalf("idx_sessions_surface_updated missing; have %v", names)
}

func TestSessionTags_mergeAndReplace(t *testing.T) {
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	const ws, sid = "ws-1", "sess-1"
	if err := repo.CreateSessionRowIfMissing(ctx, serverutil.Scope{TenantID: ws}, sid); err != nil {
		t.Fatalf("CreateSessionRowIfMissing: %v", err)
	}

	if _, err := repo.UpdateSessionTags(ctx, serverutil.Scope{TenantID: ws}, sid, map[string]string{"team": "infra"}); err != nil {
		t.Fatalf("UpdateSessionTags: %v", err)
	}
	// Merge must preserve a key the second caller never mentioned.
	got, err := repo.UpdateSessionTags(ctx, serverutil.Scope{TenantID: ws}, sid, map[string]string{"env": "prod"})
	if err != nil {
		t.Fatalf("UpdateSessionTags 2: %v", err)
	}
	if got["team"] != "infra" || got["env"] != "prod" {
		t.Fatalf("merge lost a tag: %#v", got)
	}

	// Empty value deletes.
	got, err = repo.UpdateSessionTags(ctx, serverutil.Scope{TenantID: ws}, sid, map[string]string{"team": ""})
	if err != nil {
		t.Fatalf("UpdateSessionTags delete: %v", err)
	}
	if _, ok := got["team"]; ok {
		t.Fatalf("empty value should delete the tag: %#v", got)
	}
	if got["env"] != "prod" {
		t.Fatalf("delete removed the wrong tag: %#v", got)
	}

	// Replace drops unmentioned keys.
	if err := repo.ReplaceSessionTags(ctx, serverutil.Scope{TenantID: ws}, sid, map[string]string{"only": "this"}); err != nil {
		t.Fatalf("ReplaceSessionTags: %v", err)
	}
	after, err := repo.GetSessionTags(ctx, serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("GetSessionTags: %v", err)
	}
	if len(after) != 1 || after["only"] != "this" {
		t.Fatalf("replace did not overwrite wholesale: %#v", after)
	}
}

func TestEncodeSessionTags_emptyIsBlankNotBraces(t *testing.T) {
	// "no tags" must have one on-disk representation so pre-column rows and
	// explicitly-cleared rows compare equal.
	for _, in := range []map[string]string{nil, {}, {"  ": "x"}} {
		enc, err := EncodeSessionTags(in)
		if err != nil {
			t.Fatalf("EncodeSessionTags(%#v): %v", in, err)
		}
		if enc != "" {
			t.Fatalf("EncodeSessionTags(%#v) = %q, want empty", in, enc)
		}
	}
}

func TestValidateSessionTagKey_rejectsQuotes(t *testing.T) {
	// A quote in a key would let it address a different JSON path than intended
	// in the SQLite filter.
	if err := ValidateSessionTagKey(`we"ird`); err == nil {
		t.Fatal("expected quoted tag key to be rejected")
	}
	if _, err := EncodeSessionTags(map[string]string{`a"b`: "v"}); err == nil {
		t.Fatal("expected EncodeSessionTags to reject a quoted key")
	}
}

func TestListSessions_filtersByTags(t *testing.T) {
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	const ws = "ws-1"

	seed := func(sid string, tags map[string]string) {
		t.Helper()
		if err := repo.CreateSessionRowIfMissing(ctx, serverutil.Scope{TenantID: ws}, sid); err != nil {
			t.Fatalf("create %s: %v", sid, err)
		}
		if len(tags) > 0 {
			if _, err := repo.UpdateSessionTags(ctx, serverutil.Scope{TenantID: ws}, sid, tags); err != nil {
				t.Fatalf("tag %s: %v", sid, err)
			}
		}
	}
	seed("a", map[string]string{"team": "infra", "env": "prod"})
	seed("b", map[string]string{"team": "infra", "env": "dev"})
	seed("c", map[string]string{"team": "web"})
	seed("d", nil) // untagged: must never match a tag filter

	ids := func(opts ListSessionsOptions) []string {
		t.Helper()
		rows, total, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, opts)
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.SessionID)
		}
		if int(total) != len(out) {
			t.Fatalf("total %d disagrees with rows %d — count and page filters diverged", total, len(out))
		}
		return out
	}

	if got := ids(ListSessionsOptions{Tags: map[string]string{"team": "infra"}}); len(got) != 2 {
		t.Fatalf("team=infra: got %v, want 2 rows", got)
	}
	// Multiple tags AND together.
	got := ids(ListSessionsOptions{Tags: map[string]string{"team": "infra", "env": "prod"}})
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("team=infra AND env=prod: got %v, want [a]", got)
	}
	// Empty value = key presence, any value.
	if got := ids(ListSessionsOptions{Tags: map[string]string{"env": ""}}); len(got) != 2 {
		t.Fatalf("env presence: got %v, want 2 rows", got)
	}
	if got := ids(ListSessionsOptions{Tags: map[string]string{"nope": "x"}}); len(got) != 0 {
		t.Fatalf("unknown tag should match nothing: got %v", got)
	}
	// Tags round-trip through the list projection.
	rows, _, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, ListSessionsOptions{Tags: map[string]string{"env": "prod"}})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 1 || DecodeSessionTags(rows[0].TagsJSON)["team"] != "infra" {
		t.Fatalf("tags missing from list projection: %#v", rows)
	}
}
