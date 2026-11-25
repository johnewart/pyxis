package server

import (
	"context"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	conversationevents "github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/store"
)

// gatedBackend streams a turn-start frame, signals `started`, waits for
// `release`, then finishes the turn. It lets a test look at a turn mid-flight.
type gatedBackend struct {
	stubBackend
	started chan struct{}
	release chan struct{}
}

func (b *gatedBackend) ChatStream(ctx context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	send := func(f *llmv1.ConversationStreamFrame) error {
		f.SchemaVersion = conversationevents.SchemaVersion
		return emit(f)
	}
	if err := send(&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: "turn_gated"}}}); err != nil {
		return err
	}
	if err := send(&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: "turn_gated", Delta: "par"}}}); err != nil {
		return err
	}
	select {
	case b.started <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := send(&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{
		TurnId: "turn_gated", AssistantMessageId: strPtr("asst_gated"), FinishReason: strPtr("end_turn"),
	}}}); err != nil {
		return err
	}
	return send(&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_StreamComplete{StreamComplete: &llmv1.AssistantReply{Id: "asst_gated", Content: "full answer", FinishReason: "end_turn"}}})
}

func TestChatStream_TrimsEventsAfterTurn(t *testing.T) {
	e := newDrainEnv(t)
	be := &gatedBackend{started: make(chan struct{}, 4), release: make(chan struct{}, 4)}
	e.impl.LLM = be
	events := store.NewSessionEventRepo(e.db)

	// Turn 1, held mid-flight.
	e.enqueue(t, "first")
	select {
	case <-be.started:
	case <-time.After(10 * time.Second):
		t.Fatal("turn did not start")
	}

	mid, err := e.impl.GetSessionHistory(e.ctx, &llmv1.GetSessionHistoryRequest{SessionId: drainSessionID})
	if err != nil {
		t.Fatal(err)
	}
	if !mid.GetTurnInProgress() {
		t.Fatal("history does not report the turn in progress")
	}
	start := mid.GetTurnStartSeq()
	rejoin, err := events.GetEventsSince(e.ctx, testScope, drainSessionID, start)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejoin) == 0 {
		t.Fatalf("mid-turn reconnect from turn_start_seq=%d finds no events of the running turn", start)
	}

	be.release <- struct{}{}
	e.settle(t)

	n, err := events.CountEventsForSession(e.ctx, testScope, drainSessionID)
	if err != nil || n != 1 {
		t.Fatalf("event rows after the turn = %d, %v; want exactly 1", n, err)
	}
	head, err := events.LastSeq(e.ctx, testScope, drainSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if head < 2 {
		t.Fatalf("head seq = %d; the turn appended several events, so the kept one must be the newest", head)
	}

	// A client reconnecting after the turn gets it from history.
	done, err := e.impl.GetSessionHistory(e.ctx, &llmv1.GetSessionHistoryRequest{SessionId: drainSessionID})
	if err != nil {
		t.Fatal(err)
	}
	if done.GetTurnInProgress() {
		t.Fatal("turn still in progress after settle")
	}
	var user, asst string
	for _, m := range done.GetMessages() {
		switch m.GetRole() {
		case "user":
			user = m.GetContent()
		case "assistant":
			asst = m.GetContent()
		}
	}
	if user != "first" || asst != "full answer" {
		t.Fatalf("history user=%q assistant=%q; want the full turn", user, asst)
	}

	// Turn 2: seq continues past the kept row.
	e.enqueue(t, "second")
	select {
	case <-be.started:
	case <-time.After(10 * time.Second):
		t.Fatal("second turn did not start")
	}
	be.release <- struct{}{}
	e.settle(t)
	rows, err := events.GetEventsSince(e.ctx, testScope, drainSessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Seq <= head {
		t.Fatalf("after turn 2 rows=%+v; want one row with seq > %d", rows, head)
	}
}
