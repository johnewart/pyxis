package store

import (
	"context"
	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"
	"time"

	"gorm.io/gorm"
)

// listTestClock is an arbitrary fixed base so the fixtures' relative ordering is
// obvious and the assertions do not race a real clock.
var listTestClock = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// seedSession writes a session row with an explicit created_at (UpsertSession
// preserves a non-zero CreatedAt) and optionally a set of chat messages at the
// given offsets from listTestClock.
func seedSession(t *testing.T, db *gorm.DB, repo *SessionRepo, wsID, sid, title, surface string, createdOffset time.Duration, msgOffsets ...time.Duration) {
	t.Helper()
	ctx := context.Background()
	if err := repo.UpsertSession(ctx, SessionRow{
		TenantID:    wsID,
		SessionID:   sid,
		Title:       title,
		ChatSurface: surface,
		CreatedAt:   listTestClock.Add(createdOffset),
	}); err != nil {
		t.Fatalf("UpsertSession %s: %v", sid, err)
	}
	for i, off := range msgOffsets {
		row := ChatMessageRow{
			TenantID:  wsID,
			SessionID: sid,
			TurnID:    sid + "-turn",
			Kind:      ChatMsgKindUser,
			Role:      "user",
			Content:   "hello",
			CreatedAt: listTestClock.Add(off),
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("create message %d for %s: %v", i, sid, err)
		}
	}
}

func sessionIDs(rows []SessionSummaryRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.SessionID)
	}
	return out
}

func equalIDs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestListSessions_DefaultsToLastMessageFirst is the core of the feature: the
// default order is recency of the last *message*, which is deliberately not the
// same as updated_at — "old" below is touched last (bumping its updated_at) yet
// must still sort last because nobody has said anything in it recently.
func TestListSessions_DefaultsToLastMessageFirst(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	ws := "ws-list-1"

	seedSession(t, db, repo, ws, "old", "Old", "build", -72*time.Hour, -70*time.Hour)
	seedSession(t, db, repo, ws, "mid", "Mid", "build", -48*time.Hour, -10*time.Hour)
	seedSession(t, db, repo, ws, "new", "New", "ask", -24*time.Hour, -1*time.Hour)

	// Touch "old" last so its updated_at is the newest of the three.
	if err := repo.UpdateSession(ctx, serverutil.Scope{TenantID: ws}, "old", map[string]any{"title": "Old (renamed)"}); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	rows, total, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, ListSessionsOptions{Descending: true})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if got := sessionIDs(rows); !equalIDs(got, "new", "mid", "old") {
		t.Errorf("default order = %v, want [new mid old]", got)
	}

	// Sorting by updated_at must give a different answer, proving the two keys
	// are genuinely distinct and last_message_at is not just updated_at.
	rows, _, err = repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, ListSessionsOptions{SortBy: SessionSortUpdatedAt, Descending: true})
	if err != nil {
		t.Fatalf("ListSessions by updated_at: %v", err)
	}
	if got := sessionIDs(rows); got[0] != "old" {
		t.Errorf("updated_at order = %v, want 'old' first", got)
	}
}

// TestListSessions_LastMessageAtValue checks the derived column actually scans
// back as a time (the aggregate has no declared column type, so a bad mapping
// would silently yield a zero/garbage timestamp rather than an error).
func TestListSessions_LastMessageAtValue(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ws := "ws-list-2"

	want := listTestClock.Add(-2 * time.Hour)
	seedSession(t, db, repo, ws, "chatty", "Chatty", "build", -10*time.Hour, -9*time.Hour, -2*time.Hour, -5*time.Hour)
	seedSession(t, db, repo, ws, "silent", "Silent", "build", -8*time.Hour)

	rows, _, err := repo.ListSessions(context.Background(), serverutil.Scope{TenantID: ws}, ListSessionsOptions{Descending: true})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	byID := map[string]SessionSummaryRow{}
	for _, r := range rows {
		byID[r.SessionID] = r
	}

	chatty, ok := byID["chatty"]
	if !ok {
		t.Fatalf("chatty session missing from %v", sessionIDs(rows))
	}
	if !chatty.LastMessageAt.Valid {
		t.Fatalf("chatty.LastMessageAt invalid, want %v (derived column did not scan)", want)
	}
	if !chatty.LastMessageAt.Time.UTC().Equal(want) {
		t.Errorf("chatty.LastMessageAt = %v, want %v (MAX over messages)", chatty.LastMessageAt.Time.UTC(), want)
	}

	silent, ok := byID["silent"]
	if !ok {
		t.Fatalf("silent session missing from %v", sessionIDs(rows))
	}
	if silent.LastMessageAt.Valid {
		t.Errorf("silent.LastMessageAt = %v, want invalid for a session with no messages", silent.LastMessageAt.Time)
	}
	// A message-less session still has to be ordered (by created_at), not dropped.
	if got := sessionIDs(rows); !equalIDs(got, "chatty", "silent") {
		t.Errorf("order = %v, want [chatty silent]", got)
	}
}

func TestListSessions_Pagination(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	ws := "ws-list-3"

	// s1 newest message … s5 oldest.
	for i, sid := range []string{"s1", "s2", "s3", "s4", "s5"} {
		off := -time.Duration(i+1) * time.Hour
		seedSession(t, db, repo, ws, sid, sid, "build", -100*time.Hour, off)
	}

	page1, total, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, ListSessionsOptions{Descending: true, Limit: 2})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if got := sessionIDs(page1); !equalIDs(got, "s1", "s2") {
		t.Errorf("page 1 = %v, want [s1 s2]", got)
	}

	page2, _, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, ListSessionsOptions{Descending: true, Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if got := sessionIDs(page2); !equalIDs(got, "s3", "s4") {
		t.Errorf("page 2 = %v, want [s3 s4]", got)
	}

	last, _, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, ListSessionsOptions{Descending: true, Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if got := sessionIDs(last); !equalIDs(got, "s5") {
		t.Errorf("page 3 = %v, want [s5]", got)
	}

	// Past the end is an empty page, not an error.
	none, _, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, ListSessionsOptions{Descending: true, Limit: 2, Offset: 99})
	if err != nil {
		t.Fatalf("page past end: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("page past end = %v, want empty", sessionIDs(none))
	}
}

func TestListSessions_PageSizeClamped(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ws := "ws-list-4"

	seedSession(t, db, repo, ws, "only", "Only", "build", -time.Hour, -time.Minute)

	// Over the cap is clamped rather than rejected.
	rows, _, err := repo.ListSessions(context.Background(), serverutil.Scope{TenantID: ws}, ListSessionsOptions{Limit: MaxSessionPageSize + 1000})
	if err != nil {
		t.Fatalf("ListSessions with oversized limit: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("rows = %d, want 1", len(rows))
	}
}

func TestListSessions_SortByCreatedAtAndTitle(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	ws := "ws-list-5"

	seedSession(t, db, repo, ws, "b", "banana", "build", -3*time.Hour, -1*time.Hour)
	seedSession(t, db, repo, ws, "a", "Apple", "build", -2*time.Hour, -2*time.Hour)
	seedSession(t, db, repo, ws, "c", "cherry", "build", -1*time.Hour, -3*time.Hour)

	oldestFirst, _, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, ListSessionsOptions{SortBy: SessionSortCreatedAt})
	if err != nil {
		t.Fatalf("created_at asc: %v", err)
	}
	if got := sessionIDs(oldestFirst); !equalIDs(got, "b", "a", "c") {
		t.Errorf("created_at asc = %v, want [b a c]", got)
	}

	newestFirst, _, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, ListSessionsOptions{SortBy: SessionSortCreatedAt, Descending: true})
	if err != nil {
		t.Fatalf("created_at desc: %v", err)
	}
	if got := sessionIDs(newestFirst); !equalIDs(got, "c", "a", "b") {
		t.Errorf("created_at desc = %v, want [c a b]", got)
	}

	// Title sort is case-insensitive: "Apple" must lead, not trail, lowercase names.
	byTitle, _, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, ListSessionsOptions{SortBy: SessionSortTitle})
	if err != nil {
		t.Fatalf("title asc: %v", err)
	}
	if got := sessionIDs(byTitle); !equalIDs(got, "a", "b", "c") {
		t.Errorf("title asc = %v, want [a b c] (case-insensitive)", got)
	}
}

func TestListSessions_FiltersBySurfaceAndWorkspace(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	ws := "ws-list-6"
	other := "ws-list-6-other"

	seedSession(t, db, repo, ws, "build-1", "Build one", "build", -5*time.Hour, -4*time.Hour)
	seedSession(t, db, repo, ws, "build-2", "Build two", "build", -5*time.Hour, -2*time.Hour)
	seedSession(t, db, repo, ws, "ask-1", "Ask one", "ask", -5*time.Hour, -3*time.Hour)
	seedSession(t, db, repo, other, "other-1", "Other", "build", -5*time.Hour, -time.Minute)

	rows, total, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, ListSessionsOptions{Descending: true, ChatSurface: "build"})
	if err != nil {
		t.Fatalf("surface filter: %v", err)
	}
	// total must reflect the filter, otherwise paging over a filtered list lies.
	if total != 2 {
		t.Errorf("filtered total = %d, want 2", total)
	}
	if got := sessionIDs(rows); !equalIDs(got, "build-2", "build-1") {
		t.Errorf("filtered rows = %v, want [build-2 build-1]", got)
	}

	all, total, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, ListSessionsOptions{Descending: true})
	if err != nil {
		t.Fatalf("unfiltered: %v", err)
	}
	if total != 3 || len(all) != 3 {
		t.Errorf("unfiltered total/len = %d/%d, want 3/3 (other workspace must not leak)", total, len(all))
	}
}
