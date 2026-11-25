package store

import (
	"context"
	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"
)

func newDraftRepo(t *testing.T) *SessionDraftRepo {
	t.Helper()
	return NewSessionDraftRepo(NewTestSQLite(t))
}

func TestSessionDraft_roundTrips(t *testing.T) {
	r := newDraftRepo(t)
	ctx := context.Background()

	if got, err := r.Get(ctx, serverutil.Scope{TenantID: "ws"}, "sess"); err != nil || got != "" {
		t.Fatalf("a session with no draft should read as empty: %q %v", got, err)
	}
	if err := r.Upsert(ctx, serverutil.Scope{TenantID: "ws"}, "sess", "half-typed thought"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, serverutil.Scope{TenantID: "ws"}, "sess")
	if err != nil || got != "half-typed thought" {
		t.Fatalf("draft round trip: %q %v", got, err)
	}
}

// One draft per session, enforced by the schema rather than by callers: a second
// save replaces the first instead of accumulating.
func TestSessionDraft_secondSaveReplacesTheFirst(t *testing.T) {
	r := newDraftRepo(t)
	ctx := context.Background()
	for _, content := range []string{"first", "second", "third and final"} {
		if err := r.Upsert(ctx, serverutil.Scope{TenantID: "ws"}, "sess", content); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.Get(ctx, serverutil.Scope{TenantID: "ws"}, "sess")
	if err != nil || got != "third and final" {
		t.Fatalf("latest draft should win: %q %v", got, err)
	}
	var count int64
	if err := r.db.Model(&SessionDraftRow{}).Where("session_id = ?", "sess").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("a session must never hold more than one draft, found %d", count)
	}
}

// "Composer is empty" and "no draft" are the same state; storing the former
// would leave every session ever typed in with a permanent empty row.
func TestSessionDraft_blankContentClearsRatherThanStoringEmpty(t *testing.T) {
	r := newDraftRepo(t)
	ctx := context.Background()
	if err := r.Upsert(ctx, serverutil.Scope{TenantID: "ws"}, "sess", "something"); err != nil {
		t.Fatal(err)
	}
	if err := r.Upsert(ctx, serverutil.Scope{TenantID: "ws"}, "sess", "   \n\t "); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := r.db.Model(&SessionDraftRow{}).Where("session_id = ?", "sess").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("blanking a draft should delete it, %d rows left", count)
	}
}

func TestSessionDraft_clearRemovesIt(t *testing.T) {
	r := newDraftRepo(t)
	ctx := context.Background()
	if err := r.Upsert(ctx, serverutil.Scope{TenantID: "ws"}, "sess", "to be sent"); err != nil {
		t.Fatal(err)
	}
	if err := r.Clear(ctx, serverutil.Scope{TenantID: "ws"}, "sess"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Get(ctx, serverutil.Scope{TenantID: "ws"}, "sess"); got != "" {
		t.Fatalf("cleared draft should be gone, got %q", got)
	}
	// Clearing again is not an error: a send racing a reload may clear twice.
	if err := r.Clear(ctx, serverutil.Scope{TenantID: "ws"}, "sess"); err != nil {
		t.Fatalf("clearing an absent draft should be a no-op: %v", err)
	}
}

// Drafts are per session, and per workspace — one session's unsent text must
// never surface in another.
func TestSessionDraft_isScopedPerSessionAndWorkspace(t *testing.T) {
	r := newDraftRepo(t)
	ctx := context.Background()
	if err := r.Upsert(ctx, serverutil.Scope{TenantID: "ws1"}, "a", "for A"); err != nil {
		t.Fatal(err)
	}
	if err := r.Upsert(ctx, serverutil.Scope{TenantID: "ws1"}, "b", "for B"); err != nil {
		t.Fatal(err)
	}
	if err := r.Upsert(ctx, serverutil.Scope{TenantID: "ws2"}, "a", "other workspace"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ ws, sess, want string }{
		{"ws1", "a", "for A"},
		{"ws1", "b", "for B"},
		{"ws2", "a", "other workspace"},
		{"ws2", "b", ""},
	} {
		got, err := r.Get(ctx, serverutil.Scope{TenantID: tc.ws}, tc.sess)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("draft(%s,%s) = %q, want %q", tc.ws, tc.sess, got, tc.want)
		}
	}
}
