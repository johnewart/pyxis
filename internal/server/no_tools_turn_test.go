package server

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	conversationevents "github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/toolexec"
)

// noToolsProbeBackend calls probe_tool on round 1 regardless of what it was
// offered (a provider ignoring the hint), records what it saw, and replies in
// text on later rounds.
type noToolsProbeBackend struct {
	stubBackend
	srv             *LlmServer
	rounds          int
	offered         []int
	disabledInRound []bool
}

func (b *noToolsProbeBackend) ChatStream(_ context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.rounds++
	b.offered = append(b.offered, len(tools()))
	b.disabledInRound = append(b.disabledInRound, b.srv.ToolsDisabledForTurn(testScope, drainSessionID))
	reply := &llmv1.AssistantReply{Id: "asst_text", Content: "ok", FinishReason: "end_turn"}
	if b.rounds == 1 {
		reply = &llmv1.AssistantReply{Id: "asst_tools", ToolCalls: []*llmv1.ToolCall{{Id: "call_probe", Name: "probe_tool", ArgumentsJson: `{}`}}}
	}
	for _, f := range []*llmv1.ConversationStreamFrame{
		{Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: "turn_nt"}}},
		{Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{TurnId: "turn_nt"}}},
		{Frame: &llmv1.ConversationStreamFrame_StreamComplete{StreamComplete: reply}},
	} {
		f.SchemaVersion = conversationevents.SchemaVersion
		if err := emit(f); err != nil {
			return err
		}
	}
	return nil
}

func newNoToolsEnv(t *testing.T) (*drainEnv, *noToolsProbeBackend, *atomic.Bool) {
	t.Helper()
	e := newDrainEnv(t)
	be := &noToolsProbeBackend{srv: e.impl}
	e.impl.LLM = be
	var ran atomic.Bool
	if err := registerInlineBuiltin("probe_tool", func(context.Context, json.RawMessage, toolexec.Invocation) (toolexec.Result, error) {
		ran.Store(true)
		return toolexec.Result{Content: "ran"}, nil
	})(e.impl.Tools, e.impl.handlerSet()); err != nil {
		t.Fatal(err)
	}
	return e, be, &ran
}

func TestNoToolsTurn_queuedEntryOffersNoToolsAndRefusesCalls(t *testing.T) {
	e, be, ran := newNoToolsEnv(t)
	e.setPaused(t, true)
	res, err := e.impl.EnqueueMessage(e.ctx, &llmv1.EnqueueMessageRequest{
		SessionId: drainSessionID, Content: "fyi", TurnOptions: &llmv1.TurnOptions{DisableTools: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.GetEntry().GetTurnOptions().GetDisableTools() {
		t.Fatal("enqueued entry should report disable_tools")
	}
	e.setPaused(t, false)
	e.settle(t)

	if be.rounds != 1 {
		t.Fatalf("turn should end after the refused round; model queried %d times", be.rounds)
	}
	if be.offered[0] != 0 {
		t.Fatalf("provider offered %d tools on a no-tools turn", be.offered[0])
	}
	if !be.disabledInRound[0] {
		t.Fatal("ToolsDisabledForTurn false during the turn (MCP would not see it)")
	}
	if ran.Load() {
		t.Fatal("probe_tool ran on a no-tools turn")
	}
	if e.impl.ToolsDisabledForTurn(testScope, drainSessionID) {
		t.Fatal("no-tools mark leaked past the end of the turn")
	}
	if e.impl.toolAllowedForSession(e.ctx, drainSessionID, "probe_tool") != true {
		t.Fatal("tools should be allowed again after the turn")
	}
	hist := e.ma.Core.History().History(drainSessionID, 0)
	var roles []string
	for _, m := range hist {
		roles = append(roles, m.GetRole())
	}
	// user, the assistant tool-call reply exactly once, then the refusal.
	if len(hist) != 3 || roles[0] != "user" || roles[1] != "assistant" || roles[2] != "tool" {
		t.Fatalf("history roles = %v, want [user assistant tool]", roles)
	}
	if hist[2].GetToolCallId() != "call_probe" {
		t.Fatalf("tool result tool_call_id = %q, want call_probe", hist[2].GetToolCallId())
	}
	if hist[2].GetContent() != NoToolsTurnRefusal {
		t.Fatalf("tool result = %q, want the no-tools refusal", hist[2].GetContent())
	}
}

func TestNoToolsTurn_ordinaryEntryKeepsTools(t *testing.T) {
	e, be, ran := newNoToolsEnv(t)
	e.enqueue(t, "do work")
	e.settle(t)
	if be.offered[0] == 0 || be.disabledInRound[0] {
		t.Fatalf("ordinary turn lost its tools: offered=%v disabled=%v", be.offered, be.disabledInRound)
	}
	if !ran.Load() {
		t.Fatal("probe_tool should run on an ordinary turn")
	}
}
