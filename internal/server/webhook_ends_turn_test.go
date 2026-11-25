package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// A typed webhook result with ends_turn stops the loop: one model round.
func TestWebhookEndsTurn_StopsLoop(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":"proposed","ends_turn":true}`))
	}))
	t.Cleanup(ts.Close)

	const ws = "ws-webhook-ends"
	ctx := serverutil.Scope{TenantID: ws}.OutgoingContext(context.Background())
	be := &toolsThenTextBackend{round1Calls: []*llmv1.ToolCall{
		{Id: "call_hook", Name: "hook_tool", ArgumentsJson: `{}`},
	}}
	reg, err := providers.NewRegistryFromEntries("stub_wh", map[string]*providers.Entry{
		"stub_wh": {ID: "stub_wh", Kind: providerkind.OpenAI, Backend: be, DefaultModel: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, _ := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     "agent_webhook_ends",
		LlmProviderId: strPtr("stub_wh"),
	}, serverutil.Scope{TenantID: ws}); err != nil {
		t.Fatal(err)
	}
	llmSrv := &LlmServer{
		Providers:       providers.NewComposite(reg, nil),
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
	}
	if err := llmSrv.Tools.Register(&llmv1.ToolDefinition{
		Name:           "hook_tool",
		Classification: llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: ts.URL},
		},
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

	st, err := llmv1.NewLlmServiceClient(conn).ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:  []*llmv1.Message{{Role: "user", Content: "go", Id: "u1"}},
		SessionId: strPtr("agent_webhook_ends"),
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
	if be.rounds != 1 {
		t.Fatalf("model queried %d times, want 1 (webhook ends_turn must stop the loop)", be.rounds)
	}
}
