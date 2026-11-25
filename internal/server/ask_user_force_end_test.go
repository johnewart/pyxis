package server

import (
	"context"
	"github.com/johnewart/pyxis/internal/serverutil"
	"io"
	"net"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	conversationevents "github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// askUserThenTextBackend emits an ask_user tool call on round 1, then (if ever
// invoked again) a plain text reply. rounds counts how many times ChatStream
// was actually called — the assertion this test cares about is that it stays
// at 1: llmd must not re-query the model after a successful ask_user call.
type askUserThenTextBackend struct {
	stubBackend
	rounds int
}

func (b *askUserThenTextBackend) ChatStream(_ context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.rounds++
	const turnID = "turn_ask_user"
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID}},
	}); err != nil {
		return err
	}
	if b.rounds == 1 {
		reply := &llmv1.AssistantReply{
			Id: "asst_ask",
			ToolCalls: []*llmv1.ToolCall{
				{Id: "call_ask_1", Name: toolexec.ToolAskUser, ArgumentsJson: `{"title":"Proceed?","type":"text"}`},
			},
		}
		if err := emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame:         &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{TurnId: turnID}},
		}); err != nil {
			return err
		}
		return emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame:         &llmv1.ConversationStreamFrame_StreamComplete{StreamComplete: reply},
		})
	}
	// Should never be reached if the forced-end behavior works.
	reply := &llmv1.AssistantReply{Id: "asst_done", Content: "should not get here", FinishReason: "end_turn"}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_AssistantTextDelta{AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: reply.Content}},
	}); err != nil {
		return err
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{TurnId: turnID}},
	}); err != nil {
		return err
	}
	return emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_StreamComplete{StreamComplete: reply},
	})
}

// TestAskUser_ForceEndsTurn_NoSecondModelRound drives a real ChatStream in
// which the model calls ask_user. It asserts the internal tool loop does NOT
// re-query the model afterward — the whole point of forcing the turn to end
// (see server.go's forceEndTurn) is that a model has no way to itself decline
// to keep generating once it's called this tool; llmd enforces it.
func TestAskUser_ForceEndsTurn_NoSecondModelRound(t *testing.T) {
	const ws = "ws-ask-force-end"
	ctx := serverutil.Scope{TenantID: ws}.OutgoingContext(context.Background())

	be := &askUserThenTextBackend{}
	reg, err := providers.NewRegistryFromEntries("stub_ask", map[string]*providers.Entry{
		"stub_ask": {ID: "stub_ask", Kind: providerkind.OpenAI, Backend: be, DefaultModel: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	comp := providers.NewComposite(reg, nil)
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     "agent_ask_force_end",
		LlmProviderId: strPtr("stub_ask"),
	}, serverutil.Scope{TenantID: ws}); err != nil {
		t.Fatal(err)
	}

	// Deliberately no seeded prior assistant row: this is the session's very
	// first turn. ChatAttachmentRepo.ensureAnchorTurn must synthesize an
	// empty placeholder to anchor to rather than failing (see
	// internal/toolexec/session_tools_test.go's
	// TestAskUser_FirstTurnOfSessionWithNoPriorHistory for the same case at
	// the handler level).
	attachmentRepo := store.NewChatAttachmentRepo(db)
	llmSrv := &LlmServer{
		Providers:       comp,
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		Attachments:     attachmentRepo,
	}
	if err := toolexec.RegisterSessionTools(llmSrv.Tools, llmSrv.handlerSet(), toolexec.SessionToolsDeps{
		Sessions:    store.NewSessionRepo(db),
		Attachments: attachmentRepo,
	}); err != nil {
		t.Fatal(err)
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
	client := llmv1.NewLlmServiceClient(conn)

	st, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "should I?", Id: "u1"}},
		SessionId:      strPtr("agent_ask_force_end"),
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawFinished *llmv1.EvToolExecutionFinished
	var sawCard *llmv1.EvAttachmentUpserted
	for {
		f, err := st.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if fin := f.GetToolExecutionFinished(); fin != nil {
			sawFinished = fin
		}
		if up := f.GetAttachmentUpserted(); up != nil {
			sawCard = up
		}
	}

	if be.rounds != 1 {
		t.Fatalf("model was queried %d times, want exactly 1 (no follow-up round after ask_user)", be.rounds)
	}
	if sawFinished == nil {
		t.Fatal("no tool_execution_finished frame streamed to client")
	}
	if !sawFinished.GetSuccess() {
		t.Fatalf("ask_user tool_execution_finished reported failure: %+v", sawFinished)
	}
	// The card must reach the live stream — otherwise it is persisted but
	// invisible until a session reload.
	if sawCard == nil || sawCard.GetKind() != "ask_card" || sawCard.GetPayloadJson() == "" {
		t.Fatalf("no attachment_upserted ask_card frame streamed to client (got %+v)", sawCard)
	}
}
