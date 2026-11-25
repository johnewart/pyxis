package server

// End-to-end agent loop, entirely inside llmd.
//
// This is the proof that an agent turn needs no client participation: the
// system prompt is rendered here, the model is called here, the tool calls are
// dispatched through the RegisterBuiltins registry in-process, and the results
// are fed back into the next round. There is deliberately NO ToolProviderHub on
// the server, so any call that tried to delegate out to a client would fail
// loudly rather than quietly succeed.

import (
	"context"
	"io"
	"net"
	"strings"
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

// builtinLoopBackend is a scripted model: round 1 asks for host_fetch, round 2
// asks for host_related, round 3 answers. Every round's inbound message slice
// is recorded so the test can assert the tool results were fed back.
type builtinLoopBackend struct {
	stubBackend
	mu     sync.Mutex
	rounds [][]*llmv1.Message
	tools  []string
}

func (b *builtinLoopBackend) roundCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.rounds)
}

func (b *builtinLoopBackend) round(i int) []*llmv1.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rounds[i]
}

func (b *builtinLoopBackend) advertised() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.tools...)
}

func (b *builtinLoopBackend) ChatStream(_ context.Context, msgs []*llmv1.Message, _ *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.mu.Lock()
	b.rounds = append(b.rounds, msgs)
	n := len(b.rounds)
	if tools != nil && b.tools == nil {
		for _, d := range tools() {
			b.tools = append(b.tools, d.GetName())
		}
	}
	b.mu.Unlock()

	const turnID = "turn_builtin_loop"
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID}},
	}); err != nil {
		return err
	}

	var reply *llmv1.AssistantReply
	switch n {
	case 1:
		reply = &llmv1.AssistantReply{Id: "asst_fetch", ToolCalls: []*llmv1.ToolCall{
			{Id: "call_fetch", Name: "host_fetch", ArgumentsJson: `{"key":"src/a.go"}`},
		}}
	case 2:
		reply = &llmv1.AssistantReply{Id: "asst_related", ToolCalls: []*llmv1.ToolCall{
			{Id: "call_related", Name: "host_related", ArgumentsJson: `{"key":"src/a.go"}`},
		}}
	default:
		reply = &llmv1.AssistantReply{Id: "asst_final", Content: "A() is imported by src/b.go", FinishReason: "end_turn"}
		if err := emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame:         &llmv1.ConversationStreamFrame_AssistantTextDelta{AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: reply.GetContent()}},
		}); err != nil {
			return err
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

// TestAgentLoop_BuiltinToolsEndToEndInsideLlmd drives a full multi-round agent
// turn over a real gRPC ChatStream: two builtin tool rounds plus a final answer,
// with no tool provider connected and nothing executed outside llmd.
func TestAgentLoop_BuiltinToolsEndToEndInsideLlmd(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())

	be := &builtinLoopBackend{}
	reg, err := providers.NewRegistryFromEntries("stub_loop", map[string]*providers.Entry{
		"stub_loop": {ID: "stub_loop", Kind: providerkind.OpenAI, Backend: be, DefaultModel: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     "agent_loop",
		LlmProviderId: strPtr("stub_loop"),
	}, testScope); err != nil {
		t.Fatal(err)
	}

	// llmd core registers no project-inspection tools of its own; a host
	// supplies them through InstallBuiltinTools' extra-registrar seam. Register
	// minimal local stand-ins through that same seam, closing over per-workspace
	// data exactly as a real registrar does, so this test proves the seam end to
	// end. A real tool's own dispatch is its owner's to test.
	relatedByWorkspace := map[string]map[string][]string{testScope.TenantID: {"src/a.go": {"src/b.go"}}}
	entriesByWorkspace := map[string]map[string]string{testScope.TenantID: {"src/a.go": "package a\n\nfunc A() {}\n"}}

	tools := toolregistry.New()
	handlers := toolexec.NewHandlers()
	if _, err := InstallBuiltinTools(tools, handlers,
		registerFakeRelatedTool(relatedByWorkspace),
		registerFakeFetchTool(entriesByWorkspace)); err != nil {
		t.Fatal(err)
	}

	llmSrv := &LlmServer{
		Providers:       providers.NewComposite(reg, nil),
		Tools:           tools,
		Handlers:        handlers,
		SessionRuntimes: mgr,
		TurnRegister:    store.NewTurnRegister(db),
		// ToolProviderHub is deliberately nil: nothing may be delegated out.
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
		Messages:  []*llmv1.Message{{Role: "user", Content: "who imports A()?", Id: "u1"}},
		SessionId: strPtr("agent_loop"),
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "identity", Content: "You are a helpful assistant."},
		},
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	var finished []*llmv1.EvToolExecutionFinished
	var text strings.Builder
	for {
		f, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("ChatStream: %v", err)
		}
		if fin := f.GetToolExecutionFinished(); fin != nil {
			finished = append(finished, fin)
		}
		if d := f.GetAssistantTextDelta(); d != nil {
			text.WriteString(d.GetDelta())
		}
	}

	// Three model rounds: host_fetch, host_related, answer.
	if got := be.roundCount(); got != 3 {
		t.Fatalf("expected 3 model rounds inside llmd, got %d", got)
	}
	// Both builtins were advertised to the model by llmd's own registry.
	adv := be.advertised()
	for _, want := range []string{"host_fetch", "host_related"} {
		if !containsStr(adv, want) {
			t.Fatalf("llmd did not advertise %q to the model; advertised=%v", want, adv)
		}
	}
	// Both tools executed in-process and succeeded.
	if len(finished) != 2 {
		t.Fatalf("expected 2 tool_execution_finished frames, got %d", len(finished))
	}
	for _, fin := range finished {
		if !fin.GetSuccess() {
			t.Fatalf("builtin %q failed inside llmd: %+v", fin.GetName(), fin)
		}
	}

	// Round 2 must carry round 1's host_fetch result back to the model.
	r2 := be.round(1)
	if !messagesContain(r2, "call_fetch", "func A()") {
		t.Fatalf("round 2 did not carry the host_fetch result back: %s", dumpMessages(r2))
	}
	// Round 3 must carry round 2's host_related result back to the model.
	r3 := be.round(2)
	if !messagesContain(r3, "call_related", "src/b.go") {
		t.Fatalf("round 3 did not carry the host_related result back: %s", dumpMessages(r3))
	}
	// The prompt reaching the model was composed by llmd from the sections.
	if !promptMentions(be.round(0), "You are a helpful assistant.") {
		t.Fatalf("llmd did not compose the system prompt: %s", dumpMessages(be.round(0)))
	}
	if got := text.String(); !strings.Contains(got, "src/b.go") {
		t.Fatalf("final assistant text not streamed to the client: %q", got)
	}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// messagesContain reports whether a tool message for toolCallID carries substr.
func messagesContain(msgs []*llmv1.Message, toolCallID, substr string) bool {
	for _, m := range msgs {
		if m.GetRole() == "tool" && m.GetToolCallId() == toolCallID &&
			strings.Contains(m.GetContent(), substr) {
			return true
		}
	}
	return false
}

// promptMentions reports whether llmd's composed system prompt (delivered as a
// system message or via the provider's own prompt channel) carries substr.
func promptMentions(msgs []*llmv1.Message, substr string) bool {
	for _, m := range msgs {
		if m.GetRole() == "system" && strings.Contains(m.GetContent(), substr) {
			return true
		}
	}
	return false
}

func dumpMessages(msgs []*llmv1.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.GetRole())
		b.WriteString("[")
		b.WriteString(m.GetToolCallId())
		b.WriteString("]: ")
		b.WriteString(m.GetContent())
		b.WriteString("\n")
	}
	return b.String()
}
