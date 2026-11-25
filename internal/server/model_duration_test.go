package server

import (
	"context"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	conversationevents "github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/store"
)

const timedRoundDelay = 30 * time.Millisecond

// timedBackend takes timedRoundDelay to answer and keeps the turn-finished
// frame it emitted, so the test can see what ChatStream stamped on it.
type timedBackend struct {
	stubBackend
	finished *llmv1.EvAssistantTurnFinished
}

func (b *timedBackend) ChatStream(_ context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.finished = &llmv1.EvAssistantTurnFinished{
		TurnId:             "turn_timed",
		AssistantMessageId: strPtr("asst_timed"),
		FinishReason:       strPtr("end_turn"),
		Usage:              &llmv1.TokenUsage{InputTokens: int64Ptr(40), OutputTokens: int64Ptr(9)},
	}
	frames := []*llmv1.ConversationStreamFrame{
		{Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: "turn_timed"}}},
		{Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: b.finished}},
		{Frame: &llmv1.ConversationStreamFrame_StreamComplete{StreamComplete: &llmv1.AssistantReply{Id: "asst_timed", Content: "done", FinishReason: "end_turn"}}},
	}
	for i, f := range frames {
		if i == 1 {
			time.Sleep(timedRoundDelay)
		}
		f.SchemaVersion = conversationevents.SchemaVersion
		if err := emit(f); err != nil {
			return err
		}
	}
	return nil
}

// TestChatStream_RecordsModelDurationPerCall proves each model call's wall time
// is sent on its turn-finished frame, saved with its tokens, and returned by
// history and the turn breakdown for the message it produced.
func TestChatStream_RecordsModelDurationPerCall(t *testing.T) {
	e := newDrainEnv(t)
	be := &timedBackend{}
	e.impl.LLM = be
	e.impl.TurnMetadata = store.NewTurnMetadataRepo(e.db)

	e.enqueue(t, "time this")
	e.settle(t)

	minMs := timedRoundDelay.Milliseconds()
	if be.finished == nil || be.finished.ModelDurationMs == nil {
		t.Fatal("turn-finished frame was not stamped with model_duration_ms")
	}
	if got := *be.finished.ModelDurationMs; got < minMs {
		t.Fatalf("frame model_duration_ms = %d, want >= %d", got, minMs)
	}

	hist, err := e.impl.GetSessionHistory(e.ctx, &llmv1.GetSessionHistoryRequest{SessionId: drainSessionID})
	if err != nil {
		t.Fatalf("GetSessionHistory: %v", err)
	}
	var asst *llmv1.Message
	for _, m := range hist.GetMessages() {
		if m.GetRole() == "assistant" {
			asst = m
		}
	}
	if asst == nil {
		t.Fatalf("no assistant message in history: %v", hist.GetMessages())
	}
	if asst.ModelDurationMs == nil || *asst.ModelDurationMs < minMs {
		t.Fatalf("history model_duration_ms = %v, want >= %d", asst.ModelDurationMs, minMs)
	}
	if asst.GetUsage().GetInputTokens() != 40 || asst.GetUsage().GetOutputTokens() != 9 {
		t.Fatalf("history usage = %+v, want 40 in / 9 out", asst.GetUsage())
	}

	bd, err := e.impl.GetSessionTurnBreakdown(e.ctx, &llmv1.GetSessionTurnBreakdownRequest{SessionId: drainSessionID})
	if err != nil {
		t.Fatalf("GetSessionTurnBreakdown: %v", err)
	}
	if len(bd.GetTurns()) != 1 {
		t.Fatalf("breakdown turns = %d, want 1: %+v", len(bd.GetTurns()), bd.GetTurns())
	}
	if got := bd.GetTurns()[0].GetModelDurationMs(); got < minMs {
		t.Fatalf("breakdown model_duration_ms = %d, want >= %d", got, minMs)
	}
}
