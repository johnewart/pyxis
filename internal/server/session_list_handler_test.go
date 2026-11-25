package server

import (
	"context"
	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/store"
	"gorm.io/gorm"
)

const listWorkspace = "ws-handler-list"

// newListSessionsServer wires an LlmServer with a real SQLite-backed session
// repo. SessionRuntimes must be set: scope resolution short-circuits to "" without
// it, which the repo then rejects.
func newListSessionsServer(t *testing.T) (*LlmServer, *gorm.DB) {
	t.Helper()
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{})
	return &LlmServer{SessionRuntimes: mgr, SessionRepo: store.NewSessionRepo(db)}, db
}

func listCtx() context.Context {
	return serverutil.Scope{TenantID: listWorkspace}.IncomingContext(context.Background())
}

// seedListSession writes a session plus one message at msgAt.
func seedListSession(t *testing.T, db *gorm.DB, repo *store.SessionRepo, sid, title, surface string, createdAt, msgAt time.Time) {
	t.Helper()
	if err := repo.UpsertSession(context.Background(), store.SessionRow{
		TenantID:    listWorkspace,
		SessionID:   sid,
		Title:       title,
		ChatSurface: surface,
		CreatedAt:   createdAt,
	}); err != nil {
		t.Fatalf("UpsertSession %s: %v", sid, err)
	}
	if msgAt.IsZero() {
		return
	}
	if err := db.Create(&store.ChatMessageRow{
		TenantID:  listWorkspace,
		SessionID: sid,
		TurnID:    sid + "-t",
		Kind:      store.ChatMsgKindUser,
		Role:      "user",
		Content:   "hi",
		CreatedAt: msgAt,
	}).Error; err != nil {
		t.Fatalf("create message for %s: %v", sid, err)
	}
}

func TestListSessionsHandler_DefaultOrderAndLastMessageAt(t *testing.T) {
	srv, db := newListSessionsServer(t)
	repo := store.NewSessionRepo(db)
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

	seedListSession(t, db, repo, "a", "A", "build", base.Add(-5*time.Hour), base.Add(-4*time.Hour))
	seedListSession(t, db, repo, "b", "B", "build", base.Add(-5*time.Hour), base.Add(-1*time.Hour))
	seedListSession(t, db, repo, "quiet", "Quiet", "build", base.Add(-6*time.Hour), time.Time{})

	resp, err := srv.ListSessions(listCtx(), &llmv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if got := len(resp.GetSessions()); got != 3 {
		t.Fatalf("sessions = %d, want 3", got)
	}
	if resp.GetTotalCount() != 3 {
		t.Errorf("total_count = %d, want 3", resp.GetTotalCount())
	}
	if resp.GetHasMore() {
		t.Errorf("has_more = true, want false (single page holds everything)")
	}
	if got := resp.GetSessions()[0].GetSessionId(); got != "b" {
		t.Errorf("first session = %q, want \"b\" (newest message)", got)
	}
	if got := resp.GetSessions()[0].GetLastMessageAt(); got != base.Add(-1*time.Hour).UnixMilli() {
		t.Errorf("last_message_at = %d, want %d", got, base.Add(-1*time.Hour).UnixMilli())
	}

	// A session with no messages reports 0 rather than a bogus timestamp.
	var quiet *llmv1.SessionSummary
	for _, s := range resp.GetSessions() {
		if s.GetSessionId() == "quiet" {
			quiet = s
		}
	}
	if quiet == nil {
		t.Fatal("quiet session missing from response")
	}
	if quiet.GetLastMessageAt() != 0 {
		t.Errorf("quiet last_message_at = %d, want 0", quiet.GetLastMessageAt())
	}
	if quiet.GetCreatedAt() == 0 {
		t.Error("quiet created_at = 0, want the seeded creation time")
	}
}

func TestListSessionsHandler_Pagination(t *testing.T) {
	srv, db := newListSessionsServer(t)
	repo := store.NewSessionRepo(db)
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

	for i, sid := range []string{"s1", "s2", "s3"} {
		seedListSession(t, db, repo, sid, sid, "build",
			base.Add(-10*time.Hour), base.Add(-time.Duration(i+1)*time.Hour))
	}

	page1, err := srv.ListSessions(listCtx(), &llmv1.ListSessionsRequest{PageSize: 2})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1.GetSessions()) != 2 {
		t.Fatalf("page 1 len = %d, want 2", len(page1.GetSessions()))
	}
	if !page1.GetHasMore() {
		t.Error("page 1 has_more = false, want true")
	}
	if page1.GetNextOffset() != 2 {
		t.Errorf("page 1 next_offset = %d, want 2", page1.GetNextOffset())
	}
	if page1.GetTotalCount() != 3 {
		t.Errorf("page 1 total_count = %d, want 3", page1.GetTotalCount())
	}

	page2, err := srv.ListSessions(listCtx(), &llmv1.ListSessionsRequest{
		PageSize: 2,
		Offset:   page1.GetNextOffset(),
	})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2.GetSessions()) != 1 {
		t.Fatalf("page 2 len = %d, want 1", len(page2.GetSessions()))
	}
	if page2.GetHasMore() {
		t.Error("page 2 has_more = true, want false on the last page")
	}
	if page2.GetNextOffset() != 0 {
		t.Errorf("page 2 next_offset = %d, want 0 when exhausted", page2.GetNextOffset())
	}
	if got := page2.GetSessions()[0].GetSessionId(); got != "s3" {
		t.Errorf("page 2 session = %q, want \"s3\"", got)
	}
}

// TestListSessionsHandler_SortDefaults pins the per-key default direction:
// timestamps newest-first, titles A→Z. Getting this backwards is the kind of
// thing only an explicit test catches.
func TestListSessionsHandler_SortDefaults(t *testing.T) {
	srv, db := newListSessionsServer(t)
	repo := store.NewSessionRepo(db)
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

	seedListSession(t, db, repo, "zed", "zebra", "build", base.Add(-3*time.Hour), base.Add(-3*time.Hour))
	seedListSession(t, db, repo, "app", "Apple", "build", base.Add(-1*time.Hour), base.Add(-1*time.Hour))

	byCreated, err := srv.ListSessions(listCtx(), &llmv1.ListSessionsRequest{
		SortBy: llmv1.SessionSortField_SESSION_SORT_FIELD_CREATED_AT,
	})
	if err != nil {
		t.Fatalf("created_at: %v", err)
	}
	if got := byCreated.GetSessions()[0].GetSessionId(); got != "app" {
		t.Errorf("created_at default = %q first, want \"app\" (newest first)", got)
	}

	byTitle, err := srv.ListSessions(listCtx(), &llmv1.ListSessionsRequest{
		SortBy: llmv1.SessionSortField_SESSION_SORT_FIELD_TITLE,
	})
	if err != nil {
		t.Fatalf("title: %v", err)
	}
	if got := byTitle.GetSessions()[0].GetSessionId(); got != "app" {
		t.Errorf("title default = %q first, want \"app\" (A→Z, case-insensitive)", got)
	}

	// An explicit order must override the per-key default.
	byTitleDesc, err := srv.ListSessions(listCtx(), &llmv1.ListSessionsRequest{
		SortBy: llmv1.SessionSortField_SESSION_SORT_FIELD_TITLE,
		Order:  llmv1.SessionSortOrder_SESSION_SORT_ORDER_DESC,
	})
	if err != nil {
		t.Fatalf("title desc: %v", err)
	}
	if got := byTitleDesc.GetSessions()[0].GetSessionId(); got != "zed" {
		t.Errorf("title desc = %q first, want \"zed\"", got)
	}

	byCreatedAsc, err := srv.ListSessions(listCtx(), &llmv1.ListSessionsRequest{
		SortBy: llmv1.SessionSortField_SESSION_SORT_FIELD_CREATED_AT,
		Order:  llmv1.SessionSortOrder_SESSION_SORT_ORDER_ASC,
	})
	if err != nil {
		t.Fatalf("created_at asc: %v", err)
	}
	if got := byCreatedAsc.GetSessions()[0].GetSessionId(); got != "zed" {
		t.Errorf("created_at asc = %q first, want \"zed\"", got)
	}
}

func TestListSessionsHandler_SurfaceFilter(t *testing.T) {
	srv, db := newListSessionsServer(t)
	repo := store.NewSessionRepo(db)
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

	seedListSession(t, db, repo, "b1", "Build", "build", base, base.Add(-2*time.Hour))
	seedListSession(t, db, repo, "a1", "Ask", "ask", base, base.Add(-time.Hour))

	resp, err := srv.ListSessions(listCtx(), &llmv1.ListSessionsRequest{ChatSurface: "ask"})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(resp.GetSessions()) != 1 || resp.GetSessions()[0].GetSessionId() != "a1" {
		t.Fatalf("filtered sessions = %+v, want only a1", resp.GetSessions())
	}
	// total_count must respect the filter or paging a filtered list misreports.
	if resp.GetTotalCount() != 1 {
		t.Errorf("filtered total_count = %d, want 1", resp.GetTotalCount())
	}
}

// TestListSessionsHandler_NoRepo keeps the pre-existing contract: a server
// without a session repo returns an empty list rather than an error.
func TestListSessionsHandler_NoRepo(t *testing.T) {
	mgr, _ := sessionManagerWithSQLite(t, session.HubOptions{})
	srv := &LlmServer{SessionRuntimes: mgr}
	resp, err := srv.ListSessions(listCtx(), &llmv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions without repo: %v", err)
	}
	if len(resp.GetSessions()) != 0 {
		t.Errorf("sessions = %d, want 0", len(resp.GetSessions()))
	}
}
