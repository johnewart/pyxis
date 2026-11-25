package server

import (
	"context"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/store"
)

// The set_status headline is turn-scoped: ChatStream clears it when a user turn
// starts (before UserTurnCommitted, so nothing stale can be seeded after it)
// and again when the turn ends, however it ends. Each clear is announced to
// every listener as an empty EvSessionStatus, both live (hub) and in the
// replay log.
func TestLlmServer_ChatStream_clears_turn_status_at_turn_start_and_end(t *testing.T) {
	ctx, cancel := context.WithTimeout(testScope.OutgoingContext(context.Background()), 10*time.Second)
	defer cancel()
	bg := context.Background()

	be := &cancelSilentBackend{turnID: "t-status", started: make(chan struct{})}
	env := newCancelEnv(t, ctx, "sess-status", be, false)
	cache := store.NewSessionCacheRepo(store.NewTestSQLite(t))
	env.impl.SessionCache = cache

	emptyLive := make(chan struct{}, 8)
	unsubscribe := env.hub.Subscribe(func(f *llmv1.ConversationStreamFrame) {
		if st := f.GetSessionStatus(); st != nil && st.GetHeadline() == "" {
			emptyLive <- struct{}{}
		}
	})
	defer unsubscribe()

	status := func() string {
		t.Helper()
		got, err := cache.TurnStatus(bg, testScope, env.sessionID)
		if err != nil {
			t.Fatalf("read turn status: %v", err)
		}
		return got
	}

	// Left over from a turn that never reached its end.
	if err := cache.SetTurnStatus(bg, testScope, env.sessionID, "Grepping for falafel"); err != nil {
		t.Fatalf("seed stale status: %v", err)
	}

	st := env.start(t, ctx, "what about chicken shawarma?")
	first, err := st.Recv()
	if err != nil {
		t.Fatalf("recv first frame: %v", err)
	}
	if first.GetUserTurnCommitted() == nil {
		t.Fatalf("first frame = %T, want user_turn_committed", first.GetFrame())
	}
	if got := status(); got != "" {
		t.Fatalf("status = %q at turn start, want cleared", got)
	}

	// The agent sets a status mid-turn; the turn's end must clear it.
	<-be.started
	if err := cache.SetTurnStatus(bg, testScope, env.sessionID, "Slicing shawarma"); err != nil {
		t.Fatalf("set mid-turn status: %v", err)
	}
	env.stop(t, ctx)
	env.drainUntilCut(t, st)
	if !env.impl.WaitForTurns(5 * time.Second) {
		t.Fatal("turn never finished")
	}
	if got := status(); got != "" {
		t.Fatalf("status = %q after the turn ended, want cleared", got)
	}

	if n := len(emptyLive); n < 2 {
		t.Fatalf("live empty session_status frames = %d, want one at start and one at end", n)
	}
	rows, err := env.events.GetAllEvents(bg, testScope, env.sessionID)
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	// Both clears were logged while the turn ran; once it ends the log is
	// trimmed to its newest event.
	if len(rows) != 1 {
		t.Fatalf("event rows after the turn = %d, want 1 (trimmed)", len(rows))
	}
}
