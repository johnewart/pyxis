package store

import (
	"context"
	"errors"
	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

const forkTestWS = "ws-fork"

// seedForkSource builds:
//
//	U1 user
//	A1 assistant  tool_calls=[tc_1, tc_2]   (turn 1)
//	T1 tool       tc_1
//	T2 tool       tc_2
//	A2 assistant  "done"
//	U2 user                                 (turn 2)
//	A3 assistant  "second answer"
func seedForkSource(t *testing.T, repo *SessionRepo, h *History) {
	t.Helper()
	ctx := context.Background()
	if err := repo.UpsertSession(ctx, SessionRow{
		TenantID: forkTestWS, SessionID: "src", Title: "Source", ProviderID: "anthropic",
		DefaultModel: "m", ToolPolicy: "profile_only", CursorPos: 7,
	}); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	h.StoreInTurn("src", 1, &llmv1.Message{Id: "U1", Role: "user", Content: "fix the bug"})
	h.StoreInTurn("src", 1, &llmv1.Message{Id: "A1", Role: "assistant", ThinkingContent: "hmm", Content: "looking",
		ToolCalls: []*llmv1.ToolCall{{Id: "tc_1", Name: "read"}, {Id: "tc_2", Name: "grep"}}})
	h.StoreInTurn("src", 1, &llmv1.Message{Id: "T1", Role: "tool", ToolCallId: "tc_1", Content: "r1"})
	h.StoreInTurn("src", 1, &llmv1.Message{Id: "T2", Role: "tool", ToolCallId: "tc_2", Content: "r2"})
	h.StoreInTurn("src", 1, &llmv1.Message{Id: "A2", Role: "assistant", Content: "done"})
	h.StoreInTurn("src", 2, &llmv1.Message{Id: "U2", Role: "user", Content: "again"})
	h.StoreInTurn("src", 2, &llmv1.Message{Id: "A3", Role: "assistant", Content: "second answer"})

	db := repo.db
	mustCreate := func(v any) {
		t.Helper()
		if err := db.Create(v).Error; err != nil {
			t.Fatalf("seed %T: %v", v, err)
		}
	}
	mustCreate(&SessionTurnRow{TenantID: forkTestWS, SessionID: "src", TurnSeq: 1, ExternalTurnID: "turn-1"})
	mustCreate(&SessionTurnRow{TenantID: forkTestWS, SessionID: "src", TurnSeq: 2, ExternalTurnID: "turn-2"})
	mustCreate(&TurnMetadataRow{TenantID: forkTestWS, SessionID: "src", TurnID: "turn-1", MessageID: "A2"})
	mustCreate(&TurnMetadataRow{TenantID: forkTestWS, SessionID: "src", TurnID: "turn-2", MessageID: "A3"})
	mustCreate(&ToolResultSidecarRow{TenantID: forkTestWS, SessionID: "src", ToolCallID: "tc_1", Original: "full r1"})
	mustCreate(&TrackedDocumentVersionRow{TenantID: forkTestWS, SessionID: "src", DocumentID: "d", AnchorMessageID: "U1", ContentHash: "h1"})
	mustCreate(&TrackedDocumentVersionRow{TenantID: forkTestWS, SessionID: "src", DocumentID: "d", Seq: 1, AnchorMessageID: "U2", ContentHash: "h2"})
}

func newForkFixture(t *testing.T) (*SessionRepo, *History) {
	t.Helper()
	db := NewTestSQLite(t)
	return NewSessionRepo(db), NewHistory(db, serverutil.Scope{TenantID: forkTestWS}, "src")
}

func messageIDsOf(t *testing.T, h *History, sessionID string) []string {
	t.Helper()
	var ids []string
	for _, m := range h.History(sessionID, 0) {
		ids = append(ids, m.GetId())
	}
	return ids
}

func countRows(t *testing.T, repo *SessionRepo, model any, sessionID string) int64 {
	t.Helper()
	var n int64
	if err := repo.db.Model(model).Where("session_id = ?", sessionID).Count(&n).Error; err != nil {
		t.Fatalf("count %T: %v", model, err)
	}
	return n
}

func TestForkSession_atAssistantWithToolCallsIncludesResults(t *testing.T) {
	repo, h := newForkFixture(t)
	seedForkSource(t, repo, h)
	ctx := context.Background()

	res, err := repo.ForkSession(ctx, serverutil.Scope{TenantID: forkTestWS}, "src", "dst", "A1", ForkOptions{})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if got, want := messageIDsOf(t, h, "dst"), []string{"U1", "A1", "T1", "T2"}; !equalIDs(got, want...) {
		t.Fatalf("fork history = %v, want %v", got, want)
	}
	if res.MaxTurnSeq != 1 {
		t.Fatalf("MaxTurnSeq = %d, want 1", res.MaxTurnSeq)
	}
	row, err := repo.GetSession(ctx, serverutil.Scope{TenantID: forkTestWS}, "dst")
	if err != nil {
		t.Fatalf("get fork session: %v", err)
	}
	if row.ForkedFromSessionID != "src" || row.ForkedFromMessageID != "A1" {
		t.Fatalf("forked_from = %q/%q", row.ForkedFromSessionID, row.ForkedFromMessageID)
	}
	if row.ProviderID != "anthropic" || row.ToolPolicy != "profile_only" || row.Title != "Source" {
		t.Fatalf("settings not copied: %+v", row)
	}
	if row.CursorPos != 0 {
		t.Fatalf("cursor_pos = %d, want reset to 0", row.CursorPos)
	}
	// Side tables cut at turn 1 / copied tool calls / copied messages.
	if n := countRows(t, repo, &SessionTurnRow{}, "dst"); n != 1 {
		t.Fatalf("session turns = %d, want 1", n)
	}
	if n := countRows(t, repo, &ToolResultSidecarRow{}, "dst"); n != 1 {
		t.Fatalf("sidecars = %d, want 1", n)
	}
	if n := countRows(t, repo, &TurnMetadataRow{}, "dst"); n != 0 {
		t.Fatalf("turn metadata = %d, want 0 (A2 not copied)", n)
	}
	if n := countRows(t, repo, &TrackedDocumentVersionRow{}, "dst"); n != 1 {
		t.Fatalf("tracked doc versions = %d, want 1", n)
	}
}

func TestForkSession_atLastMessageOfTurnCopiesTurnData(t *testing.T) {
	repo, h := newForkFixture(t)
	seedForkSource(t, repo, h)

	if _, err := repo.ForkSession(context.Background(), serverutil.Scope{TenantID: forkTestWS}, "src", "dst", "A2", ForkOptions{}); err != nil {
		t.Fatalf("fork: %v", err)
	}
	if got, want := messageIDsOf(t, h, "dst"), []string{"U1", "A1", "T1", "T2", "A2"}; !equalIDs(got, want...) {
		t.Fatalf("fork history = %v, want %v", got, want)
	}
	if n := countRows(t, repo, &TurnMetadataRow{}, "dst"); n != 1 {
		t.Fatalf("turn metadata = %d, want 1", n)
	}
}

func TestForkSession_lastMessageAndEmptyCopyEverything(t *testing.T) {
	for name, upTo := range map[string]string{"last": "A3", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			repo, h := newForkFixture(t)
			seedForkSource(t, repo, h)
			if _, err := repo.ForkSession(context.Background(), serverutil.Scope{TenantID: forkTestWS}, "src", "dst", upTo, ForkOptions{}); err != nil {
				t.Fatalf("fork(%q): %v", upTo, err)
			}
			if got, want := messageIDsOf(t, h, "dst"), messageIDsOf(t, h, "src"); !equalIDs(got, want...) {
				t.Fatalf("fork(%q) history = %v, want %v", upTo, got, want)
			}
			if n := countRows(t, repo, &SessionTurnRow{}, "dst"); n != 2 {
				t.Fatalf("fork(%q) session turns = %d, want 2", upTo, n)
			}
		})
	}
}

func TestForkSession_sourceUnchanged(t *testing.T) {
	repo, h := newForkFixture(t)
	seedForkSource(t, repo, h)
	before := messageIDsOf(t, h, "src")
	beforeRows := countRows(t, repo, &ChatMessageRow{}, "src")

	if _, err := repo.ForkSession(context.Background(), serverutil.Scope{TenantID: forkTestWS}, "src", "dst", "U2", ForkOptions{}); err != nil {
		t.Fatalf("fork: %v", err)
	}
	if got := messageIDsOf(t, h, "src"); !equalIDs(got, before...) {
		t.Fatalf("source history changed: %v -> %v", before, got)
	}
	if n := countRows(t, repo, &ChatMessageRow{}, "src"); n != beforeRows {
		t.Fatalf("source rows changed: %d -> %d", beforeRows, n)
	}
	src, err := repo.GetSession(context.Background(), serverutil.Scope{TenantID: forkTestWS}, "src")
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if src.CursorPos != 7 || src.ForkedFromSessionID != "" {
		t.Fatalf("source session row mutated: %+v", src)
	}
}

func TestForkSession_errors(t *testing.T) {
	repo, h := newForkFixture(t)
	seedForkSource(t, repo, h)
	ctx := context.Background()

	if _, err := repo.ForkSession(ctx, serverutil.Scope{TenantID: forkTestWS}, "src", "dst", "nope", ForkOptions{}); !errors.Is(err, ErrForkMessageNotFound) {
		t.Fatalf("unknown message: err = %v", err)
	}
	if _, err := repo.ForkSession(ctx, serverutil.Scope{TenantID: forkTestWS}, "missing", "dst", "", ForkOptions{}); !errors.Is(err, ErrForkSourceNotFound) {
		t.Fatalf("unknown source: err = %v", err)
	}
	// Failed forks above must not have left a partial target behind.
	if n := countRows(t, repo, &SessionRow{}, "dst"); n != 0 {
		t.Fatalf("partial target session left behind: %d rows", n)
	}
	if _, err := repo.ForkSession(ctx, serverutil.Scope{TenantID: forkTestWS}, "src", "dst", "", ForkOptions{}); err != nil {
		t.Fatalf("fork: %v", err)
	}
	if _, err := repo.ForkSession(ctx, serverutil.Scope{TenantID: forkTestWS}, "src", "dst", "", ForkOptions{}); !errors.Is(err, ErrForkTargetExists) {
		t.Fatalf("existing target: err = %v", err)
	}
}

// Turn 2 (U2, A3) is treated as still running: forking anywhere inside it is
// refused with nothing written, while forking within completed turn 1 works.
func TestForkSession_inProgressTurn(t *testing.T) {
	repo, h := newForkFixture(t)
	seedForkSource(t, repo, h)
	ctx := context.Background()
	sc := serverutil.Scope{TenantID: forkTestWS}
	running := ForkOptions{InProgressTurnSeq: 2}

	for _, upTo := range []string{"U2", "A3", ""} {
		if _, err := repo.ForkSession(ctx, sc, "src", "dst", upTo, running); !errors.Is(err, ErrForkTurnInProgress) {
			t.Fatalf("fork(%q) during running turn: err = %v, want ErrForkTurnInProgress", upTo, err)
		}
	}
	if n := countRows(t, repo, &SessionRow{}, "dst"); n != 0 {
		t.Fatalf("rejected fork left a target session behind")
	}
	if _, err := repo.ForkSession(ctx, sc, "src", "dst", "A2", running); err != nil {
		t.Fatalf("fork within completed turn: %v", err)
	}
}

func TestForkSession_titleOverride(t *testing.T) {
	repo, h := newForkFixture(t)
	seedForkSource(t, repo, h)
	ctx := context.Background()
	sc := serverutil.Scope{TenantID: forkTestWS}

	if _, err := repo.ForkSession(ctx, sc, "src", "dst", "A2", ForkOptions{Title: "  Branch  "}); err != nil {
		t.Fatalf("fork: %v", err)
	}
	row, err := repo.GetSession(ctx, sc, "dst")
	if err != nil {
		t.Fatalf("get fork: %v", err)
	}
	if row.Title != "Branch" {
		t.Fatalf("title = %q, want %q", row.Title, "Branch")
	}
}
