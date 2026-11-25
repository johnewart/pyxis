package server

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	conversationevents "github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testWrapUpInstruction = "WRAP UP NOW: answer without tools."

type roundRecord struct {
	toolCount  int
	hasInstr   bool
	hasDefault bool
}

// alwaysToolsBackend requests a tool on every round that offers tools. On a
// round with no tools it returns text, unless stubborn is set, in which case it
// still returns a tool call (with text) to exercise leftover-call handling.
type alwaysToolsBackend struct {
	stubBackend
	stubborn bool
	mu       sync.Mutex
	rounds   []roundRecord
}

func (b *alwaysToolsBackend) ChatStream(_ context.Context, msgs []*llmv1.Message, _ *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	rec := roundRecord{toolCount: len(tools())}
	for _, m := range msgs {
		if strings.Contains(m.GetContent(), testWrapUpInstruction) {
			rec.hasInstr = true
		}
		if strings.Contains(m.GetContent(), defaultFinalRoundInstruction) {
			rec.hasDefault = true
		}
	}
	b.mu.Lock()
	b.rounds = append(b.rounds, rec)
	n := len(b.rounds)
	b.mu.Unlock()

	turnID := "turn_final_round"
	reply := &llmv1.AssistantReply{Id: "asst_" + string(rune('a'+n)), Content: "wrapped up", FinishReason: "end_turn"}
	if rec.toolCount > 0 || b.stubborn {
		reply.ToolCalls = []*llmv1.ToolCall{{Id: "call_" + string(rune('a'+n)), Name: "delegated_echo", ArgumentsJson: `{}`}}
	}
	for _, f := range []*llmv1.ConversationStreamFrame{
		{SchemaVersion: conversationevents.SchemaVersion, Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID}}},
		{SchemaVersion: conversationevents.SchemaVersion, Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{TurnId: turnID}}},
		{SchemaVersion: conversationevents.SchemaVersion, Frame: &llmv1.ConversationStreamFrame_StreamComplete{StreamComplete: reply}},
	} {
		if err := emit(f); err != nil {
			return err
		}
	}
	return nil
}

// runFinalRound drives a 3-round ChatStream and returns the last StreamComplete
// reply, the stream error (if any), and a loader for the persisted rows.
func runFinalRound(t *testing.T, be *alwaysToolsBackend, fr *llmv1.FinalRoundOptions) (*llmv1.AssistantReply, error, func() []store.ChatMessageRow) {
	t.Helper()
	fp := &fakeToolProvider{result: "ok"}
	client, ctx, db := grpcDispatchHarnessWith(t, fp, be)
	maxRounds := int32(3)
	st, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:  []*llmv1.Message{{Role: "user", Content: "use the tool", Id: "u1"}},
		SessionId: strPtr("agent_dispatch"),
		Options:   &llmv1.CallOptions{MaxToolRounds: &maxRounds, FinalRound: fr},
	})
	if err != nil {
		t.Fatal(err)
	}
	var complete *llmv1.AssistantReply
	for {
		f, rerr := st.Recv()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			err = rerr
			break
		}
		if sc := f.GetStreamComplete(); sc != nil {
			complete = sc
		}
	}
	rows := func() []store.ChatMessageRow {
		var out []store.ChatMessageRow
		if qerr := db.Where("session_id = ?", "agent_dispatch").Find(&out).Error; qerr != nil {
			t.Fatalf("query rows: %v", qerr)
		}
		return out
	}
	return complete, err, rows
}

func TestChatStream_FinalRound(t *testing.T) {
	t.Run("EnabledWrapsUpWithoutTools", func(t *testing.T) {
		be := &alwaysToolsBackend{}
		reply, err, rows := runFinalRound(t, be, &llmv1.FinalRoundOptions{Enabled: true, Instruction: testWrapUpInstruction})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(be.rounds) != 3 {
			t.Fatalf("want 3 rounds, got %d", len(be.rounds))
		}
		for i, r := range be.rounds[:2] {
			if r.toolCount == 0 || r.hasInstr {
				t.Fatalf("round %d should offer tools and no instruction: %+v", i, r)
			}
		}
		last := be.rounds[2]
		if last.toolCount != 0 {
			t.Fatalf("last round must offer no tools, got %d", last.toolCount)
		}
		if !last.hasInstr {
			t.Fatal("last round must carry the instruction")
		}
		// The wire normalizer carries text as deltas, so check the final reply's
		// identity (third round) and that it has no tool calls.
		if reply == nil || reply.GetId() != "asst_d" || reply.GetFinishReason() != "end_turn" || len(reply.GetToolCalls()) != 0 {
			t.Fatalf("want text reply, got %+v", reply)
		}
		for _, r := range rows() {
			if strings.Contains(r.Content, testWrapUpInstruction) {
				t.Fatalf("instruction was persisted: %+v", r)
			}
		}
	})

	t.Run("EmptyInstructionUsesDefault", func(t *testing.T) {
		be := &alwaysToolsBackend{}
		_, err, rows := runFinalRound(t, be, &llmv1.FinalRoundOptions{Enabled: true})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(be.rounds) != 3 {
			t.Fatalf("want 3 rounds, got %d", len(be.rounds))
		}
		if be.rounds[0].hasDefault || be.rounds[1].hasDefault {
			t.Fatal("default instruction must only appear on the last round")
		}
		last := be.rounds[2]
		if last.toolCount != 0 || !last.hasDefault {
			t.Fatalf("last round must have no tools and the default instruction: %+v", last)
		}
		for _, r := range rows() {
			if strings.Contains(r.Content, defaultFinalRoundInstruction) {
				t.Fatalf("default instruction was persisted: %+v", r)
			}
		}
	})

	t.Run("EnabledDropsLeftoverToolCalls", func(t *testing.T) {
		be := &alwaysToolsBackend{stubborn: true}
		_, err, rows := runFinalRound(t, be, &llmv1.FinalRoundOptions{Enabled: true, Instruction: testWrapUpInstruction})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(be.rounds) != 3 {
			t.Fatalf("want 3 rounds, got %d", len(be.rounds))
		}
		for _, r := range rows() {
			if strings.Contains(r.Content, testWrapUpInstruction) {
				t.Fatalf("instruction was persisted: %+v", r)
			}
		}
	})

	t.Run("DisabledReturnsResourceExhausted", func(t *testing.T) {
		be := &alwaysToolsBackend{}
		_, err, _ := runFinalRound(t, be, nil)
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("want ResourceExhausted, got %v", err)
		}
		for i, r := range be.rounds {
			if r.toolCount == 0 || r.hasInstr {
				t.Fatalf("round %d should offer tools and no instruction: %+v", i, r)
			}
		}
	})
}
