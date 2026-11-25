package server

import (
	"context"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const forkTenant = "ws-handler-fork"

var forkScope = serverutil.Scope{TenantID: forkTenant}

func forkCtx() context.Context { return forkScope.IncomingContext(context.Background()) }

// newForkServer seeds a source session "src" with two turns:
//
//	turn 1: U1, A1(tool_calls tc_1), T1, A2
//	turn 2: U2, A3
func newForkServer(t *testing.T) *LlmServer {
	t.Helper()
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{})
	repo := store.NewSessionRepo(db)
	reg := store.NewTurnRegister(db)
	ctx := context.Background()
	if err := repo.UpsertSession(ctx, store.SessionRow{TenantID: forkTenant, SessionID: "src", Title: "Source"}); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	h := store.NewHistory(db, forkScope, "src")
	seq1, err := reg.Advance(ctx, forkScope, "src", time.Now(), "turn-1")
	if err != nil {
		t.Fatalf("advance turn 1: %v", err)
	}
	h.StoreInTurn("src", seq1, &llmv1.Message{Id: "U1", Role: "user", Content: "fix it"})
	h.StoreInTurn("src", seq1, &llmv1.Message{Id: "A1", Role: "assistant", ToolCalls: []*llmv1.ToolCall{{Id: "tc_1", Name: "read"}}})
	h.StoreInTurn("src", seq1, &llmv1.Message{Id: "T1", Role: "tool", ToolCallId: "tc_1", Content: "r1"})
	h.StoreInTurn("src", seq1, &llmv1.Message{Id: "A2", Role: "assistant", Content: "done"})
	seq2, err := reg.Advance(ctx, forkScope, "src", time.Now(), "turn-2")
	if err != nil {
		t.Fatalf("advance turn 2: %v", err)
	}
	h.StoreInTurn("src", seq2, &llmv1.Message{Id: "U2", Role: "user", Content: "again"})
	h.StoreInTurn("src", seq2, &llmv1.Message{Id: "A3", Role: "assistant", Content: "partial"})
	return &LlmServer{SessionRuntimes: mgr, SessionRepo: repo, TurnRegister: reg}
}

func wantCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

func TestForkSessionHandler_forksAndExposesLineage(t *testing.T) {
	srv := newForkServer(t)

	resp, err := srv.ForkSession(forkCtx(), &llmv1.ForkSessionRequest{
		SourceSessionId: "src", UpToMessageId: "A1", NewSessionId: "fork", Title: "Branch",
	})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if resp.GetSessionId() != "fork" || resp.GetCopiedMessages() != 3 {
		t.Fatalf("resp = %+v, want fork with 3 messages (U1, A1, T1)", resp)
	}

	got, err := srv.GetSession(forkCtx(), &llmv1.GetSessionRequest{SessionId: "fork"})
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	m := got.GetMeta()
	if m.GetForkedFromSessionId() != "src" || m.GetForkedFromMessageId() != "A1" || m.GetTitle() != "Branch" {
		t.Fatalf("meta = %+v", m)
	}

	list, err := srv.ListSessions(forkCtx(), &llmv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	var found bool
	for _, s := range list.GetSessions() {
		if s.GetSessionId() == "fork" {
			found = true
			if s.GetForkedFromSessionId() != "src" || s.GetForkedFromMessageId() != "A1" {
				t.Fatalf("summary lineage = %+v", s)
			}
		}
	}
	if !found {
		t.Fatalf("fork missing from ListSessions")
	}
}

func TestForkSessionHandler_mintsSessionID(t *testing.T) {
	srv := newForkServer(t)
	resp, err := srv.ForkSession(forkCtx(), &llmv1.ForkSessionRequest{SourceSessionId: "src"})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if resp.GetSessionId() == "" || resp.GetSessionId() == "src" {
		t.Fatalf("minted session id = %q", resp.GetSessionId())
	}
	if resp.GetCopiedMessages() != 6 {
		t.Fatalf("copied = %d, want 6 (full clone)", resp.GetCopiedMessages())
	}
}

func TestForkSessionHandler_runningTurn(t *testing.T) {
	srv := newForkServer(t)
	// Simulate turn 2 still running: ChatStream holds the session's gate.
	release, err := srv.sessionTurnGate().acquire(context.Background(), "src")
	if err != nil {
		t.Fatalf("acquire gate: %v", err)
	}
	defer release()

	for _, upTo := range []string{"U2", "A3", ""} {
		_, err := srv.ForkSession(forkCtx(), &llmv1.ForkSessionRequest{SourceSessionId: "src", UpToMessageId: upTo, NewSessionId: "f-" + upTo})
		wantCode(t, err, codes.FailedPrecondition)
	}
	// A completed earlier message is still forkable mid-turn.
	if _, err := srv.ForkSession(forkCtx(), &llmv1.ForkSessionRequest{SourceSessionId: "src", UpToMessageId: "A2", NewSessionId: "ok"}); err != nil {
		t.Fatalf("fork of completed turn while running: %v", err)
	}
}

func TestForkSessionHandler_runningTurnUnknownSeqRefuses(t *testing.T) {
	srv := newForkServer(t)
	srv.TurnRegister = nil // turn_seq unknown: cannot check overlap
	release, err := srv.sessionTurnGate().acquire(context.Background(), "src")
	if err != nil {
		t.Fatalf("acquire gate: %v", err)
	}
	defer release()
	_, err = srv.ForkSession(forkCtx(), &llmv1.ForkSessionRequest{SourceSessionId: "src", UpToMessageId: "A2"})
	wantCode(t, err, codes.FailedPrecondition)
}

func TestForkSessionHandler_errorCodes(t *testing.T) {
	srv := newForkServer(t)
	ctx := forkCtx()

	_, err := srv.ForkSession(ctx, &llmv1.ForkSessionRequest{})
	wantCode(t, err, codes.InvalidArgument)

	_, err = srv.ForkSession(ctx, &llmv1.ForkSessionRequest{SourceSessionId: "src", NewSessionId: "src"})
	wantCode(t, err, codes.InvalidArgument)

	_, err = srv.ForkSession(ctx, &llmv1.ForkSessionRequest{SourceSessionId: "nope"})
	wantCode(t, err, codes.NotFound)

	_, err = srv.ForkSession(ctx, &llmv1.ForkSessionRequest{SourceSessionId: "src", UpToMessageId: "missing"})
	wantCode(t, err, codes.NotFound)

	if _, err := srv.ForkSession(ctx, &llmv1.ForkSessionRequest{SourceSessionId: "src", NewSessionId: "dup"}); err != nil {
		t.Fatalf("first fork: %v", err)
	}
	_, err = srv.ForkSession(ctx, &llmv1.ForkSessionRequest{SourceSessionId: "src", NewSessionId: "dup"})
	wantCode(t, err, codes.AlreadyExists)
}
