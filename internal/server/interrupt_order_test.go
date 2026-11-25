package server

import (
	"context"
	"sync"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
)

// The interrupt case: a turn is streaming, the user sends a correction instead of
// waiting, and the UI aborts the live turn and immediately opens the next one
// WITHOUT waiting for the aborted turn to finish unwinding server-side (that is
// exactly what useLlmdChatDriver.send does while `streaming`).
//
// Whatever the model managed to say before being cut off has to stay between the
// message that prompted it and the message that interrupted it. Unserialized,
// turn 2 reads history before turn 1's post-cancel commit lands, and the partial
// is appended afterwards — ordered after the message that interrupted it, and
// missing from the context turn 2 was answered with.

// interruptBackend serves the first turn as a stream that stalls after emitting a
// partial (so the test can cancel it mid-flight) and every later turn as a quick
// complete reply, recording the message list it was handed each time.
type interruptBackend struct {
	stubBackend
	mu      sync.Mutex
	saw     [][]*llmv1.Message
	calls   int
	emitted chan struct{}
	reply   string
}

func (b *interruptBackend) ChatStream(ctx context.Context, msgs []*llmv1.Message, opt *llmv1.CallOptions, tf func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.mu.Lock()
	b.calls++
	call := b.calls
	cp := make([]*llmv1.Message, len(msgs))
	copy(cp, msgs)
	b.saw = append(b.saw, cp)
	b.mu.Unlock()

	fireCaptureStatefulSessionSink(ctx)
	if call > 1 {
		b.stubBackend.chatReply = &llmv1.AssistantReply{
			Content:      b.reply,
			Id:           "asst_after_interrupt",
			FinishReason: "end_turn",
		}
		return b.stubBackend.ChatStream(ctx, msgs, opt, tf, emit)
	}

	const turnID = "turn_interrupted"
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID},
		},
	}); err != nil {
		return err
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
			AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: "I was in the middle of"},
		},
	}); err != nil {
		return err
	}
	close(b.emitted)
	<-ctx.Done()
	return ctx.Err()
}

func (b *interruptBackend) sawCall(i int) []*llmv1.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i >= len(b.saw) {
		return nil
	}
	return b.saw[i]
}

func TestLlmServer_ChatStream_interrupt_keeps_partial_between_both_user_messages(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	const sid = "sess_interrupt_order"

	be := &interruptBackend{emitted: make(chan struct{}), reply: "Never mind then."}
	// A stateless kind, so the backend is handed the whole prompt and the test
	// can read what turn 2 was actually answered with (stateful kinds get only
	// the unsynced tail by design).
	env := newCancelEnv(t, ctx, sid, be, true, withProviderKind(providerkind.OpenAI))

	// Turn 1 starts streaming.
	turn1Ctx, interrupt := context.WithCancel(ctx)
	defer interrupt()
	st1 := env.startWithID(t, turn1Ctx, "Tell me something long.", "u_first")
	select {
	case <-be.emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("first turn never streamed anything")
	}

	// Interrupt: abort turn 1 and open turn 2 immediately, without waiting for
	// turn 1 to unwind — the UI's behaviour, and the race this guards.
	// Explicit Stop (CancelTurn) is what cuts the turn; the stream abort alone
	// would not. The driver does both, back to back, so this test does too.
	env.stop(t, ctx)
	interrupt()
	st2 := env.startWithID(t, ctx, "Actually, never mind.", "u_second")

	// Drain both. Turn 1's stream is already broken; turn 2 runs to completion.
	for {
		if _, err := st1.Recv(); err != nil {
			break
		}
	}
	for {
		if _, err := st2.Recv(); err != nil {
			break
		}
	}

	// No polling needed, and none wanted (a reader hammering SQLite competes with
	// the commit for the write lock): turn 2 could not start until turn 1 released
	// the session, and turn 2's commit happens before its RPC returns. So turn 2
	// reaching EOF above means both turns are fully committed.
	hist := env.history()

	type row struct{ role, content string }
	got := make([]row, 0, len(hist))
	for _, m := range hist {
		got = append(got, row{m.GetRole(), m.GetContent()})
	}
	want := []row{
		{"user", "Tell me something long."},
		{"assistant", "I was in the middle of"},
		{"user", "Actually, never mind."},
		{"assistant", "Never mind then."},
	}
	if len(got) != len(want) {
		t.Fatalf("interrupt transcript: want %d rows %+v, got %d rows %+v", len(want), want, len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("interrupt transcript out of order at row %d: want %+v, got %+v (full: %+v)", i, want[i], got[i], got)
		}
	}

	// The interrupted reply is still marked as truncated, not silently promoted
	// to a complete answer just because a later turn succeeded.
	if reason := hist[1].GetFinishReason(); reason != finishReasonCancelled {
		t.Fatalf("interrupted partial should keep finish_reason=%q, got %q", finishReasonCancelled, reason)
	}

	// And turn 2 was answered with the partial already in context — which is only
	// possible if turn 1's commit strictly preceded turn 2's history read.
	second := be.sawCall(1)
	var secondContents []string
	for _, m := range second {
		if r := m.GetRole(); r == "user" || r == "assistant" {
			secondContents = append(secondContents, m.GetContent())
		}
	}
	wantContents := []string{"Tell me something long.", "I was in the middle of", "Actually, never mind."}
	if len(secondContents) != len(wantContents) {
		t.Fatalf("turn 2 context: want %v, got %v", wantContents, secondContents)
	}
	for i := range wantContents {
		if secondContents[i] != wantContents[i] {
			t.Fatalf("turn 2 must be answered with the interrupted partial in place: want %v, got %v",
				wantContents, secondContents)
		}
	}
}
