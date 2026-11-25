package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
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

// priorityBackend asks for the given tool calls (in order) in round 1 and
// answers in round 2, recording the messages it received each round.
type priorityBackend struct {
	stubBackend
	mu     sync.Mutex
	calls  []string
	rounds [][]*llmv1.Message
}

func (b *priorityBackend) ChatStream(_ context.Context, msgs []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.mu.Lock()
	b.rounds = append(b.rounds, msgs)
	n := len(b.rounds)
	b.mu.Unlock()
	const turnID = "turn_prio"
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID}},
	}); err != nil {
		return err
	}
	reply := &llmv1.AssistantReply{Id: "asst_final", Content: "done", FinishReason: "end_turn"}
	if n == 1 {
		reply = &llmv1.AssistantReply{Id: "asst_calls"}
		for _, name := range b.calls {
			reply.ToolCalls = append(reply.ToolCalls, &llmv1.ToolCall{Id: "id_" + name, Name: name, ArgumentsJson: `{}`})
		}
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

// runPriorityTurn registers the named tools (priorities by name; absent = nil),
// has the model call them in `calls` order, and returns the execution order and
// the tool-message order the model saw in round 2.
func runPriorityTurn(t *testing.T, calls []string, prios map[string]int32) (executed, transcript []string) {
	t.Helper()
	ctx := testScope.OutgoingContext(context.Background())
	be := &priorityBackend{calls: calls}
	reg, err := providers.NewRegistryFromEntries("stub_prio", map[string]*providers.Entry{
		"stub_prio": {ID: "stub_prio", Kind: providerkind.OpenAI, Backend: be, DefaultModel: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: "prio", LlmProviderId: strPtr("stub_prio")}, testScope); err != nil {
		t.Fatal(err)
	}

	var emu sync.Mutex
	tools := toolregistry.New()
	handlers := toolexec.NewHandlers()
	for _, name := range calls {
		name := name
		def := &llmv1.ToolDefinition{
			Name:           name,
			Classification: llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
			Execution:      &llmv1.ToolDefinition_InlineBuiltin{InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{}},
		}
		if p, ok := prios[name]; ok {
			p := p
			def.Priority = &p
		}
		if err := tools.Register(def); err != nil {
			t.Fatal(err)
		}
		if err := handlers.Register(name, func(context.Context, json.RawMessage, toolexec.Invocation) (toolexec.Result, error) {
			emu.Lock()
			executed = append(executed, name)
			emu.Unlock()
			return toolexec.Result{Content: "result_" + name}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	llmSrv := &LlmServer{
		Providers:       providers.NewComposite(reg, nil),
		Tools:           tools,
		Handlers:        handlers,
		SessionRuntimes: mgr,
		TurnRegister:    store.NewTurnRegister(db),
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
		SessionId: strPtr("prio"),
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "identity", Content: "You are a helpful assistant."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("ChatStream: %v", err)
		}
	}

	be.mu.Lock()
	defer be.mu.Unlock()
	if len(be.rounds) != 2 {
		t.Fatalf("expected 2 model rounds, got %d", len(be.rounds))
	}
	for _, m := range be.rounds[1] {
		if m.GetRole() == "tool" {
			transcript = append(transcript, m.GetToolCallId())
		}
	}
	emu.Lock()
	defer emu.Unlock()
	return append([]string(nil), executed...), transcript
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestToolPriority_ExecutesLowerFirstTranscriptKeepsCallOrder(t *testing.T) {
	executed, transcript := runPriorityTurn(t, []string{"t_a", "t_b", "t_c"}, map[string]int32{"t_a": 5, "t_b": 1, "t_c": 3})
	if want := []string{"t_b", "t_c", "t_a"}; !equalStrs(executed, want) {
		t.Fatalf("execution order = %v, want %v", executed, want)
	}
	if want := []string{"id_t_a", "id_t_b", "id_t_c"}; !equalStrs(transcript, want) {
		t.Fatalf("transcript order = %v, want %v", transcript, want)
	}
}

func TestToolPriority_StableForEqualPriorities(t *testing.T) {
	executed, transcript := runPriorityTurn(t, []string{"t_a", "t_b", "t_c", "t_d"}, map[string]int32{"t_a": 2, "t_b": 1, "t_c": 2, "t_d": 1})
	if want := []string{"t_b", "t_d", "t_a", "t_c"}; !equalStrs(executed, want) {
		t.Fatalf("execution order = %v, want %v", executed, want)
	}
	if want := []string{"id_t_a", "id_t_b", "id_t_c", "id_t_d"}; !equalStrs(transcript, want) {
		t.Fatalf("transcript order = %v, want %v", transcript, want)
	}
}

func TestToolPriority_NoPrioritiesKeepsCallOrder(t *testing.T) {
	executed, transcript := runPriorityTurn(t, []string{"t_c", "t_a", "t_b"}, nil)
	if want := []string{"t_c", "t_a", "t_b"}; !equalStrs(executed, want) {
		t.Fatalf("execution order = %v, want %v", executed, want)
	}
	if want := []string{"id_t_c", "id_t_a", "id_t_b"}; !equalStrs(transcript, want) {
		t.Fatalf("transcript order = %v, want %v", transcript, want)
	}
}
