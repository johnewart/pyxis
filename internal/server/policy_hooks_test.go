package server

// Policy hook enforcement tests. They use the story-2 policyhook API plus one
// server field the wiring must add: LlmServer.PolicyHooks (a policyhook.Hooks;
// nil means no-op).

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	conversationevents "github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/policyhook"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const policyProbeTool = "policy_probe"
const policyOtherTool = "policy_other"

// policyScriptBackend asks for policyProbeTool for toolRounds rounds, then
// answers. It records every call and the tool names offered each round.
type policyScriptBackend struct {
	stubBackend
	toolRounds int

	mu          sync.Mutex
	calls       int
	offered     [][]string
	roundInputs [][]*llmv1.Message
}

func (b *policyScriptBackend) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func (b *policyScriptBackend) offeredAt(i int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.offered[i]
}

func (b *policyScriptBackend) ChatStream(_ context.Context, msgs []*llmv1.Message, _ *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	var names []string
	if tools != nil {
		for _, d := range tools() {
			names = append(names, d.GetName())
		}
	}
	b.mu.Lock()
	b.calls++
	n := b.calls
	b.offered = append(b.offered, names)
	b.roundInputs = append(b.roundInputs, msgs)
	b.mu.Unlock()

	const turnID = "turn_policy"
	send := func(f *llmv1.ConversationStreamFrame) error {
		f.SchemaVersion = conversationevents.SchemaVersion
		return emit(f)
	}
	if err := send(&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID}}}); err != nil {
		return err
	}
	var reply *llmv1.AssistantReply
	if n <= b.toolRounds {
		reply = &llmv1.AssistantReply{Id: "asst_tool", ToolCalls: []*llmv1.ToolCall{
			{Id: "call_policy", Name: policyProbeTool, ArgumentsJson: `{}`},
		}}
	} else {
		reply = &llmv1.AssistantReply{Id: "asst_final", Content: "done", FinishReason: "end_turn"}
		if err := send(&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: "done"}}}); err != nil {
			return err
		}
	}
	if err := send(&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{TurnId: turnID}}}); err != nil {
		return err
	}
	return send(&llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_StreamComplete{StreamComplete: reply}})
}

type policyTurnResult struct {
	frames   []*llmv1.ConversationStreamFrame
	failed   []string
	finished []*llmv1.EvToolExecutionFinished
}

// runPolicyTurn runs one ChatStream turn over bufconn with hooks installed and
// returns the frames. executed counts runs of the probe tool's executor.
func runPolicyTurn(t *testing.T, hooks policyhook.Hooks, be *policyScriptBackend, executed *int32) policyTurnResult {
	t.Helper()
	ctx := testScope.OutgoingContext(context.Background())
	reg, err := providers.NewRegistryFromEntries("stub_policy", map[string]*providers.Entry{
		"stub_policy": {ID: "stub_policy", Kind: providerkind.OpenAI, Backend: be, DefaultModel: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     "policy_session",
		LlmProviderId: strPtr("stub_policy"),
	}, testScope); err != nil {
		t.Fatal(err)
	}
	tools := toolregistry.New()
	handlers := toolexec.NewHandlers()
	probe := registerInlineBuiltin(policyProbeTool, func(context.Context, json.RawMessage, toolexec.Invocation) (toolexec.Result, error) {
		atomic.AddInt32(executed, 1)
		return toolexec.Result{Content: "probe-ran"}, nil
	})
	other := registerInlineBuiltin(policyOtherTool, func(context.Context, json.RawMessage, toolexec.Invocation) (toolexec.Result, error) {
		return toolexec.Result{Content: "other-ran"}, nil
	})
	if _, err := InstallBuiltinTools(tools, handlers, probe, other); err != nil {
		t.Fatal(err)
	}
	llmSrv := &LlmServer{
		Providers:       providers.NewComposite(reg, nil),
		Tools:           tools,
		Handlers:        handlers,
		SessionRuntimes: mgr,
		TurnRegister:    store.NewTurnRegister(db),
		PolicyHooks:     hooks,
	}
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, llmSrv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	stream, err := llmv1.NewLlmServiceClient(conn).ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:  []*llmv1.Message{{Role: "user", Content: "go", Id: "u1"}},
		SessionId: strPtr("policy_session"),
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "identity", Content: "You are a helpful assistant."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var res policyTurnResult
	for {
		f, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			// A denied turn may surface as a stream status error after the
			// failure frame; record it instead of dropping it.
			t.Logf("ChatStream recv ended with: %v", err)
			break
		}
		res.frames = append(res.frames, f)
		if x := f.GetAssistantTurnFailed(); x != nil {
			res.failed = append(res.failed, x.GetError())
		}
		if x := f.GetToolExecutionFinished(); x != nil {
			res.finished = append(res.finished, x)
		}
	}
	return res
}

func TestPolicyDenyLLMCallEndsTurn(t *testing.T) {
	be := &policyScriptBackend{}
	hooks := policyhook.Funcs{CheckLLMCallFn: func(context.Context, policyhook.LLMCallRequest) (policyhook.Verdict, error) {
		return policyhook.Verdict{Decision: policyhook.DecisionDeny, Reason: "budget exceeded by policy"}, nil
	}}
	var executed int32
	res := runPolicyTurn(t, hooks, be, &executed)
	if got := be.callCount(); got != 0 {
		t.Fatalf("backend called %d times after DENY, want 0", got)
	}
	if len(res.failed) == 0 {
		t.Fatalf("no AssistantTurnFailed frame; frames=%d", len(res.frames))
	}
	if !strings.Contains(strings.Join(res.failed, " "), "budget exceeded by policy") {
		t.Fatalf("failure does not carry the reason: %v", res.failed)
	}
}

func TestPolicyFilterToolsHidesTool(t *testing.T) {
	be := &policyScriptBackend{}
	hooks := policyhook.Funcs{FilterToolsFn: func(_ context.Context, r policyhook.FilterToolsRequest) (policyhook.FilterResult, error) {
		var keep []string
		for _, d := range r.Tools {
			if d.GetName() != policyOtherTool {
				keep = append(keep, d.GetName())
			}
		}
		return policyhook.FilterResult{Decision: policyhook.DecisionAllow, AllowedToolNames: keep}, nil
	}}
	var executed int32
	runPolicyTurn(t, hooks, be, &executed)
	if be.callCount() < 1 {
		t.Fatal("backend never called")
	}
	offered := be.offeredAt(0)
	if containsStr(offered, policyOtherTool) {
		t.Fatalf("filtered tool %q was offered: %v", policyOtherTool, offered)
	}
	if !containsStr(offered, policyProbeTool) {
		t.Fatalf("allowed tool %q missing from offered tools: %v", policyProbeTool, offered)
	}
}

func TestPolicyDenyToolCallNotExecuted(t *testing.T) {
	be := &policyScriptBackend{toolRounds: 1}
	hooks := policyhook.Funcs{CheckToolCallFn: func(_ context.Context, r policyhook.ToolCallRequest) (policyhook.Verdict, error) {
		if r.Name == policyProbeTool {
			return policyhook.Verdict{Decision: policyhook.DecisionDeny, Reason: "tool blocked by policy"}, nil
		}
		return policyhook.Verdict{}, nil
	}}
	var executed int32
	res := runPolicyTurn(t, hooks, be, &executed)
	if n := atomic.LoadInt32(&executed); n != 0 {
		t.Fatalf("denied tool executor ran %d times", n)
	}
	if len(res.finished) != 1 || res.finished[0].GetSuccess() {
		t.Fatalf("want one failed ToolExecutionFinished, got %+v", res.finished)
	}
	if be.callCount() != 2 {
		t.Fatalf("backend rounds = %d, want 2 (error result fed back)", be.callCount())
	}
	be.mu.Lock()
	second := be.roundInputs[1]
	be.mu.Unlock()
	if !messagesContain(second, "call_policy", "tool blocked by policy") {
		t.Fatalf("model did not receive the deny reason as the tool result: %s", dumpMessages(second))
	}
}

func TestPolicyCadenceFirstRoundAndEveryN(t *testing.T) {
	cases := []struct {
		name   string
		policy *policyhook.CheckPolicy
		want   []uint32
	}{
		{"first_round", &policyhook.CheckPolicy{Cadence: policyhook.CadenceFirstRound}, []uint32{0}},
		{"every_2", &policyhook.CheckPolicy{Cadence: policyhook.CadenceEveryNRounds, EveryNRounds: 2}, []uint32{0, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			be := &policyScriptBackend{toolRounds: 3} // 4 rounds: 0..3
			var mu sync.Mutex
			var rounds []uint32
			hooks := policyhook.Funcs{
				GetCheckPolicyFn: func(context.Context) (policyhook.HookPolicies, error) {
					return policyhook.HookPolicies{LLMCall: tc.policy}, nil
				},
				CheckLLMCallFn: func(_ context.Context, r policyhook.LLMCallRequest) (policyhook.Verdict, error) {
					mu.Lock()
					rounds = append(rounds, r.Round)
					mu.Unlock()
					return policyhook.Verdict{Decision: policyhook.DecisionAllow}, nil
				},
			}
			var executed int32
			runPolicyTurn(t, hooks, be, &executed)
			if be.callCount() != 4 {
				t.Fatalf("backend rounds = %d, want 4", be.callCount())
			}
			mu.Lock()
			defer mu.Unlock()
			if len(rounds) != len(tc.want) {
				t.Fatalf("hook rounds = %v, want %v", rounds, tc.want)
			}
			for i := range rounds {
				if rounds[i] != tc.want[i] {
					t.Fatalf("hook rounds = %v, want %v", rounds, tc.want)
				}
			}
		})
	}
}

// Regression: a FilterTools decision must hold on rounds its cadence skips,
// not fall back to the full tool set.
func TestPolicyFilterToolsFirstRoundHoldsOnLaterRounds(t *testing.T) {
	be := &policyScriptBackend{toolRounds: 2} // 3 rounds: 0..2
	var filterCalls int32
	hooks := policyhook.Funcs{
		GetCheckPolicyFn: func(context.Context) (policyhook.HookPolicies, error) {
			return policyhook.HookPolicies{FilterTools: &policyhook.CheckPolicy{Cadence: policyhook.CadenceFirstRound}}, nil
		},
		FilterToolsFn: func(_ context.Context, r policyhook.FilterToolsRequest) (policyhook.FilterResult, error) {
			atomic.AddInt32(&filterCalls, 1)
			var keep []string
			for _, d := range r.Tools {
				if d.GetName() != policyOtherTool {
					keep = append(keep, d.GetName())
				}
			}
			return policyhook.FilterResult{Decision: policyhook.DecisionAllow, AllowedToolNames: keep}, nil
		},
	}
	var executed int32
	runPolicyTurn(t, hooks, be, &executed)
	if be.callCount() != 3 {
		t.Fatalf("backend rounds = %d, want 3", be.callCount())
	}
	if n := atomic.LoadInt32(&filterCalls); n != 1 {
		t.Fatalf("FilterTools called %d times, want 1 (first round only)", n)
	}
	for i := 0; i < 3; i++ {
		offered := be.offeredAt(i)
		if containsStr(offered, policyOtherTool) {
			t.Fatalf("round %d: filtered tool %q offered again: %v", i, policyOtherTool, offered)
		}
		if !containsStr(offered, policyProbeTool) {
			t.Fatalf("round %d: allowed tool %q missing: %v", i, policyProbeTool, offered)
		}
	}
	if n := atomic.LoadInt32(&executed); n != 2 {
		t.Fatalf("allowed tool executed %d times, want 2", n)
	}
}

func TestCompletePolicyDenyAndFilter(t *testing.T) {
	ctx := testScope.IncomingContext(context.Background())
	tools := []*llmv1.ToolDefinition{{Name: "lookup"}, {Name: "secret"}}

	// Deny: the backend is never called and the RPC fails with the reason.
	s, be := newCompleteTestServer(t)
	s.PolicyHooks = policyhook.Funcs{CheckLLMCallFn: func(_ context.Context, r policyhook.LLMCallRequest) (policyhook.Verdict, error) {
		if r.Purpose != llmv1.PolicyPurpose_POLICY_PURPOSE_COMPLETE {
			t.Errorf("purpose = %v, want complete", r.Purpose)
		}
		return policyhook.Verdict{Decision: policyhook.DecisionDeny, Reason: "complete denied by policy"}, nil
	}}
	_, err := s.Complete(ctx, &llmv1.CompleteRequest{ProviderId: "claude", Messages: userMsgs(), Tools: tools})
	if err == nil || !strings.Contains(err.Error(), "complete denied by policy") {
		t.Fatalf("Complete err = %v, want one carrying the reason", err)
	}
	if be.calls != 0 {
		t.Fatalf("backend called %d times after DENY", be.calls)
	}

	// Filter: only allowed tools reach the backend.
	s2, be2 := newCompleteTestServer(t)
	s2.PolicyHooks = policyhook.Funcs{FilterToolsFn: func(context.Context, policyhook.FilterToolsRequest) (policyhook.FilterResult, error) {
		return policyhook.FilterResult{Decision: policyhook.DecisionAllow, AllowedToolNames: []string{"lookup"}}, nil
	}}
	if _, err := s2.Complete(ctx, &llmv1.CompleteRequest{ProviderId: "claude", Messages: userMsgs(), Tools: tools}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(be2.tools) != 1 || be2.tools[0].GetName() != "lookup" {
		t.Fatalf("backend saw tools %v, want only lookup", be2.tools)
	}
}

func TestNoopPolicyExistingBehavior(t *testing.T) {
	be := &policyScriptBackend{toolRounds: 2}
	var executed int32
	res := runPolicyTurn(t, policyhook.Noop{}, be, &executed)
	if be.callCount() != 3 {
		t.Fatalf("backend rounds = %d, want 3", be.callCount())
	}
	if n := atomic.LoadInt32(&executed); n != 2 {
		t.Fatalf("tool executed %d times, want 2", n)
	}
	if len(res.failed) != 0 {
		t.Fatalf("unexpected failure frames: %v", res.failed)
	}
	for _, f := range res.finished {
		if !f.GetSuccess() {
			t.Fatalf("tool failed under no-op policy: %+v", f)
		}
	}
	if !containsStr(be.offeredAt(0), policyProbeTool) || !containsStr(be.offeredAt(0), policyOtherTool) {
		t.Fatalf("no-op policy hid tools: %v", be.offeredAt(0))
	}
}
