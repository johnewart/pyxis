package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	conversationevents "github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// toolsThenTextBackend emits round1Calls on the first ChatStream round and a
// plain text reply on any later round; rounds counts model queries.
type toolsThenTextBackend struct {
	stubBackend
	round1Calls []*llmv1.ToolCall
	rounds      int
}

func (b *toolsThenTextBackend) ChatStream(_ context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.rounds++
	const turnID = "turn_ends_turn"
	reply := &llmv1.AssistantReply{Id: "asst_done", Content: "follow-up", FinishReason: "end_turn"}
	if b.rounds == 1 {
		reply = &llmv1.AssistantReply{Id: "asst_tools", ToolCalls: b.round1Calls}
	}
	frames := []*llmv1.ConversationStreamFrame{
		{Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID}}},
		{Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{TurnId: turnID}}},
		{Frame: &llmv1.ConversationStreamFrame_StreamComplete{StreamComplete: reply}},
	}
	for _, f := range frames {
		f.SchemaVersion = conversationevents.SchemaVersion
		if err := emit(f); err != nil {
			return err
		}
	}
	return nil
}

// runEndsTurnChat drives one ChatStream whose first round calls
// propose_delta (EndsTurn: true; returns an error when proposeFails) and
// plain_tool. It returns the model round count and whether plain_tool ran.
func runEndsTurnChat(t *testing.T, proposeFails bool) (rounds int, plainRan bool) {
	t.Helper()
	const ws = "ws-ends-turn"
	ctx := serverutil.Scope{TenantID: ws}.OutgoingContext(context.Background())

	be := &toolsThenTextBackend{round1Calls: []*llmv1.ToolCall{
		{Id: "call_propose", Name: "propose_delta", ArgumentsJson: `{}`},
		{Id: "call_plain", Name: "plain_tool", ArgumentsJson: `{}`},
	}}
	reg, err := providers.NewRegistryFromEntries("stub_end", map[string]*providers.Entry{
		"stub_end": {ID: "stub_end", Kind: providerkind.OpenAI, Backend: be, DefaultModel: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, _ := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     "agent_ends_turn",
		LlmProviderId: strPtr("stub_end"),
	}, serverutil.Scope{TenantID: ws}); err != nil {
		t.Fatal(err)
	}
	llmSrv := &LlmServer{
		Providers:       providers.NewComposite(reg, nil),
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
	}
	var plain atomic.Bool
	register := []func(*toolregistry.Registry, *toolexec.Handlers) error{
		registerInlineBuiltin("propose_delta", func(context.Context, json.RawMessage, toolexec.Invocation) (toolexec.Result, error) {
			if proposeFails {
				return toolexec.Result{EndsTurn: true}, errors.New("proposal rejected")
			}
			return toolexec.Result{Content: `{"proposed":true}`, EndsTurn: true}, nil
		}),
		registerInlineBuiltin("plain_tool", func(context.Context, json.RawMessage, toolexec.Invocation) (toolexec.Result, error) {
			plain.Store(true)
			return toolexec.Result{Content: "ok"}, nil
		}),
	}
	for _, r := range register {
		if err := r(llmSrv.Tools, llmSrv.handlerSet()); err != nil {
			t.Fatal(err)
		}
	}

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, llmSrv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	st, err := llmv1.NewLlmServiceClient(conn).ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:  []*llmv1.Message{{Role: "user", Content: "change it", Id: "u1"}},
		SessionId: strPtr("agent_ends_turn"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err := st.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return be.rounds, plain.Load()
}

// A successful tool returning EndsTurn ends the turn after the WHOLE round
// runs: no second model query, and the sibling tool call still executed.
func TestToolEndsTurn_SuccessEndsTurnAfterRound(t *testing.T) {
	rounds, plainRan := runEndsTurnChat(t, false)
	if rounds != 1 {
		t.Fatalf("model queried %d times, want 1 (EndsTurn must stop the loop)", rounds)
	}
	if !plainRan {
		t.Fatal("sibling tool call in the same round did not run")
	}
}

// A failed tool never ends the turn, even if its Result set EndsTurn: the
// model must get a chance to react to the error.
func TestToolEndsTurn_FailureDoesNotEndTurn(t *testing.T) {
	rounds, _ := runEndsTurnChat(t, true)
	if rounds != 2 {
		t.Fatalf("model queried %d times, want 2 (failed tool must not end the turn)", rounds)
	}
}
