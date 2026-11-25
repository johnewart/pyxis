package server

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	conversationevents "github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/llm/backend"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolprovider"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/gorm"
)

// fakeToolProvider implements the ToolProviderService_ConnectServer surface the
// hub uses to dispatch a delegated tool call. It records every ToolExecutionInvoke
// the hub sends and immediately answers via hub.DeliverResult, standing in for a
// connected client without a real bidi stream.
type fakeToolProvider struct {
	grpc.ServerStream
	hub    *toolprovider.Hub
	result string
	isErr  bool
	mu     sync.Mutex
	seen   []*llmv1.ToolExecutionInvoke
}

func (f *fakeToolProvider) Send(m *llmv1.ToolServerToProvider) error {
	if inv := m.GetInvoke(); inv != nil {
		f.mu.Lock()
		f.seen = append(f.seen, inv)
		f.mu.Unlock()
		go f.hub.DeliverResult(inv.GetExecutionId(), inv.GetToolId(), f.result, f.isErr, "", nil, false)
	}
	return nil
}

func (f *fakeToolProvider) Recv() (*llmv1.ToolProviderToServer, error) {
	// Never invoked by the hub in these tests; block if it ever is.
	select {}
}

func (f *fakeToolProvider) invocations() []*llmv1.ToolExecutionInvoke {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*llmv1.ToolExecutionInvoke, len(f.seen))
	copy(out, f.seen)
	return out
}

// toolThenDoneBackend emits a single delegated tool call on its first ChatStream
// round, then returns a plain assistant turn on the second round so the server's
// tool loop terminates instead of looping to max_tool_rounds.
type toolThenDoneBackend struct {
	stubBackend
	toolName string
	rounds   int
}

func (b *toolThenDoneBackend) ChatStream(_ context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.rounds++
	const turnID = "turn_dispatch"
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID}},
	}); err != nil {
		return err
	}
	if b.rounds == 1 {
		reply := &llmv1.AssistantReply{
			Id: "asst_tool",
			ToolCalls: []*llmv1.ToolCall{
				{Id: "call_1", Name: b.toolName, ArgumentsJson: `{"x":1}`},
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
	reply := &llmv1.AssistantReply{Id: "asst_done", Content: "final", FinishReason: "end_turn"}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_AssistantTextDelta{AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: "final"}},
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

// grpcDispatchHarness wires a real in-memory gRPC LlmService whose model requests
// a client-delegated tool, with a fake provider connected to the shared hub. It
// returns the streaming client, the workspace-scoped context, the provider spy, and
// the backend (for round assertions).
func grpcDispatchHarness(t *testing.T, fp *fakeToolProvider) (llmv1.LlmServiceClient, context.Context, *toolThenDoneBackend, *gorm.DB) {
	t.Helper()
	be := &toolThenDoneBackend{toolName: "delegated_echo"}
	client, ctx, db := grpcDispatchHarnessWith(t, fp, be)
	return client, ctx, be, db
}

// grpcDispatchHarnessWith is grpcDispatchHarness with a caller-supplied backend.
func grpcDispatchHarnessWith(t *testing.T, fp *fakeToolProvider, be backend.Backend) (llmv1.LlmServiceClient, context.Context, *gorm.DB) {
	t.Helper()
	ctx := testScope.OutgoingContext(context.Background())

	reg, err := providers.NewRegistryFromEntries("stub_dispatch", map[string]*providers.Entry{
		"stub_dispatch": {ID: "stub_dispatch", Kind: providerkind.OpenAI, Backend: be, DefaultModel: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	comp := providers.NewComposite(reg, nil)
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     "agent_dispatch",
		LlmProviderId: strPtr("stub_dispatch"),
	}, testScope); err != nil {
		t.Fatal(err)
	}

	hub := toolprovider.NewHub()
	fp.hub = hub
	unregister := hub.Register(testScope, fp)
	t.Cleanup(unregister)

	llmSrv := &LlmServer{
		Providers:       comp,
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		ToolProviderHub: hub,
		TurnRegister:    store.NewTurnRegister(db),
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

	if _, err := client.RegisterSessionTool(ctx, &llmv1.RegisterSessionToolRequest{
		SessionId: "agent_dispatch",
		Tool: &llmv1.ToolDefinition{
			Name:                 "delegated_echo",
			ParametersSchemaJson: `{"type":"object","properties":{}}`,
			Execution:            &llmv1.ToolDefinition_DelegateToClient{DelegateToClient: &llmv1.ToolExecutionDelegateToClient{}},
		},
	}); err != nil {
		t.Fatalf("RegisterSessionTool: %v", err)
	}
	return client, ctx, db
}

// runDispatch drives a ChatStream turn that triggers the delegated tool and
// returns the last tool_execution_finished frame observed.
func runDispatch(t *testing.T, client llmv1.LlmServiceClient, ctx context.Context) *llmv1.EvToolExecutionFinished {
	t.Helper()
	st, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "use the tool", Id: "u1"}},
		SessionId:      strPtr("agent_dispatch"),
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	var finished *llmv1.EvToolExecutionFinished
	for {
		f, err := st.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if fin := f.GetToolExecutionFinished(); fin != nil {
			finished = fin
		}
	}
	return finished
}

// TestToolDispatch_InternalLoop_DelegatesToProvider drives a real ChatStream in
// which the model requests a client-delegated tool. It asserts the internal tool
// loop routes the call through the shared ToolProviderHub to the connected
// provider and streams back the right tool_execution_finished outcome.
func TestToolDispatch_InternalLoop_DelegatesToProvider(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		fp := &fakeToolProvider{result: "provider-result"}
		client, ctx, be, _ := grpcDispatchHarness(t, fp)

		finished := runDispatch(t, client, ctx)

		invs := fp.invocations()
		if len(invs) != 1 {
			t.Fatalf("expected provider invoked exactly once, got %d", len(invs))
		}
		if got := invs[0].GetToolId(); got != "delegated_echo" {
			t.Fatalf("provider invoked for wrong tool: got %q", got)
		}
		if got := invs[0].GetSessionId(); got != "agent_dispatch" {
			t.Fatalf("provider invoke missing session id: got %q", got)
		}
		if finished == nil {
			t.Fatal("no tool_execution_finished frame streamed to client")
		}
		if !finished.GetSuccess() {
			t.Fatalf("tool_execution_finished reported failure: %+v", finished)
		}
		if finished.GetName() != "delegated_echo" {
			t.Fatalf("tool_execution_finished for wrong tool: %q", finished.GetName())
		}
		if be.rounds < 2 {
			t.Fatalf("expected a follow-up round after the tool result, rounds=%d", be.rounds)
		}
	})

	t.Run("ProviderError", func(t *testing.T) {
		fp := &fakeToolProvider{result: "tool blew up", isErr: true}
		client, ctx, be, _ := grpcDispatchHarness(t, fp)

		finished := runDispatch(t, client, ctx)

		if len(fp.invocations()) != 1 {
			t.Fatalf("expected provider invoked exactly once, got %d", len(fp.invocations()))
		}
		if finished == nil {
			t.Fatal("no tool_execution_finished frame streamed to client")
		}
		if finished.GetSuccess() {
			t.Fatal("tool_execution_finished must report success=false on a provider error")
		}
		// The loop must still feed the error back to the model and finish cleanly.
		if be.rounds < 2 {
			t.Fatalf("expected a follow-up round after the tool error, rounds=%d", be.rounds)
		}
	})

	// PersistsToolResultWithTurnSeq proves the last mile end-to-end: a dispatched
	// tool result is persisted to llmd_chat_messages carrying the turn's canonical
	// turn_seq (minted at this ChatStream's turn start), not a bare/NULL value.
	t.Run("PersistsToolResultWithTurnSeq", func(t *testing.T) {
		fp := &fakeToolProvider{result: "provider-result"}
		client, ctx, _, db := grpcDispatchHarness(t, fp)

		runDispatch(t, client, ctx)

		var toolRows []store.ChatMessageRow
		if err := db.Where("session_id = ? AND kind = ?", "agent_dispatch", store.ChatMsgKindToolResult).
			Find(&toolRows).Error; err != nil {
			t.Fatalf("query tool rows: %v", err)
		}
		if len(toolRows) != 1 {
			t.Fatalf("expected exactly one persisted tool_result row, got %d", len(toolRows))
		}
		if toolRows[0].TurnSeq == nil {
			t.Fatal("persisted tool_result row is missing turn_seq")
		}
		if *toolRows[0].TurnSeq != 1 {
			t.Fatalf("tool_result row: want turn_seq=1 (first turn), got %d", *toolRows[0].TurnSeq)
		}

		// The user message of the same turn shares the turn_seq.
		var userRows []store.ChatMessageRow
		if err := db.Where("session_id = ? AND kind = ?", "agent_dispatch", store.ChatMsgKindUser).
			Find(&userRows).Error; err != nil {
			t.Fatalf("query user rows: %v", err)
		}
		if len(userRows) != 1 || userRows[0].TurnSeq == nil || *userRows[0].TurnSeq != 1 {
			t.Fatalf("user row should share turn_seq=1, got %+v", userRows)
		}
	})
}
