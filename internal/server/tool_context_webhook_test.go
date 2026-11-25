package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

// webhookToolBackend calls the "ctx_hook" tool on odd rounds and answers on
// even rounds, so each ChatStream turn is one tool call plus one answer.
type webhookToolBackend struct {
	stubBackend
	mu sync.Mutex
	n  int
}

func (b *webhookToolBackend) ChatStream(_ context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.mu.Lock()
	b.n++
	n := b.n
	b.mu.Unlock()

	const turnID = "turn_ctx_hook"
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID}},
	}); err != nil {
		return err
	}
	reply := &llmv1.AssistantReply{Id: "asst_final", Content: "done", FinishReason: "end_turn"}
	if n%2 == 1 {
		reply = &llmv1.AssistantReply{Id: "asst_call", ToolCalls: []*llmv1.ToolCall{
			{Id: "call_ctx", Name: "ctx_hook", ArgumentsJson: `{}`},
		}}
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

// TestChatStream_ToolContextReachesWebhook proves tool_context from the
// request (overriding the session's EnsureSession value) and from the session
// (when the request sends none) arrives in the webhook body.
func TestChatStream_ToolContextReachesWebhook(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())

	var mu sync.Mutex
	var bodies []map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read webhook body: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Errorf("decode webhook body %q: %v", b, err)
		}
		mu.Lock()
		bodies = append(bodies, m)
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(ts.Close)

	be := &webhookToolBackend{}
	reg, err := providers.NewRegistryFromEntries("stub_ctx", map[string]*providers.Entry{
		"stub_ctx": {ID: "stub_ctx", Kind: providerkind.OpenAI, Backend: be, DefaultModel: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     "ctx_sess",
		LlmProviderId: strPtr("stub_ctx"),
		ToolContext:   map[string]string{"from": "session", "only_session": "yes"},
	}, testScope); err != nil {
		t.Fatal(err)
	}

	tools := toolregistry.New()
	if err := tools.Register(&llmv1.ToolDefinition{
		Name:           "ctx_hook",
		Description:    "records tool context",
		Classification: llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: ts.URL},
		},
	}); err != nil {
		t.Fatal(err)
	}

	llmSrv := &LlmServer{
		Providers:       providers.NewComposite(reg, nil),
		Tools:           tools,
		Handlers:        toolexec.NewHandlers(),
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
	client := llmv1.NewLlmServiceClient(conn)

	runTurn := func(id string, tc map[string]string) {
		t.Helper()
		stream, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
			Messages:    []*llmv1.Message{{Role: "user", Content: "go", Id: id}},
			SessionId:   strPtr("ctx_sess"),
			ToolContext: tc,
		})
		if err != nil {
			t.Fatal(err)
		}
		for {
			_, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Fatalf("ChatStream: %v", err)
			}
		}
	}

	runTurn("u1", map[string]string{"from": "request"})
	runTurn("u2", nil)

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("webhook calls=%d, want 2", len(bodies))
	}
	first, _ := bodies[0]["tool_context"].(map[string]any)
	if len(first) != 1 || first["from"] != "request" {
		t.Errorf("turn 1 tool_context=%v, want request value only (override, not merge)", bodies[0]["tool_context"])
	}
	second, _ := bodies[1]["tool_context"].(map[string]any)
	if second["from"] != "session" || second["only_session"] != "yes" {
		t.Errorf("turn 2 tool_context=%v, want session value", bodies[1]["tool_context"])
	}
	if bodies[0]["tenant_id"] != testScope.TenantID {
		t.Errorf("tenant_id=%v, want %q", bodies[0]["tenant_id"], testScope.TenantID)
	}
}
