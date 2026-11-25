package server

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/gorm"
)

func usageRows(t *testing.T, db *gorm.DB) []store.UsageRecordRow {
	t.Helper()
	var rows []store.UsageRecordRow
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func assertOneUsageRow(t *testing.T, db *gorm.DB, rpc, model string) {
	t.Helper()
	rows := usageRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("rows=%d want 1: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.TenantID != testScope.TenantID || r.ScopeID != testScope.ScopeID || r.Model != model || r.RPC != rpc || r.Outcome != usageOutcomeOK {
		t.Fatalf("unexpected row for %s: %+v", rpc, r)
	}
}

func completeServerWithUsage(t *testing.T) (*LlmServer, *gorm.DB) {
	t.Helper()
	s, _, db := newCompleteTestServerDB(t)
	if err := db.AutoMigrate(&store.UsageRecordRow{}); err != nil {
		t.Fatal(err)
	}
	s.Usage = store.NewUsageRepo(db)
	return s, db
}

func TestUsage_GenerateOnceRecordsOneRow(t *testing.T) {
	s, db := completeServerWithUsage(t)
	ctx := testScope.IncomingContext(context.Background())
	if _, err := s.GenerateOnce(ctx, &llmv1.GenerateOnceRequest{
		ProviderId: strPtr("claude"), Prompt: "hi", Options: &llmv1.CallOptions{Model: "claude-x"},
	}); err != nil {
		t.Fatal(err)
	}
	assertOneUsageRow(t, db, "GenerateOnce", "claude-x")
}

func TestUsage_CompleteRecordsOneRow(t *testing.T) {
	s, db := completeServerWithUsage(t)
	ctx := testScope.IncomingContext(context.Background())
	if _, err := s.Complete(ctx, &llmv1.CompleteRequest{ProviderId: "claude", Messages: userMsgs()}); err != nil {
		t.Fatal(err)
	}
	assertOneUsageRow(t, db, "Complete", "claude-x")
}

func TestUsage_CompleteStreamRecordsOneRow(t *testing.T) {
	s, db := completeServerWithUsage(t)
	ctx := testScope.OutgoingContext(context.Background())
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	stream, err := llmv1.NewLlmServiceClient(conn).CompleteStream(ctx, &llmv1.CompleteRequest{ProviderId: "claude", Messages: userMsgs()})
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := stream.Recv(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	assertOneUsageRow(t, db, "CompleteStream", "claude-x")
}

// A ChatStream tool loop of N provider rounds writes exactly N rows.
func TestUsage_ChatStreamToolLoopRecordsOneRowPerRound(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	be := &builtinLoopBackend{} // scripted: 2 tool rounds + 1 final answer = 3 rounds
	reg, err := providers.NewRegistryFromEntries("stub_loop", map[string]*providers.Entry{
		"stub_loop": {ID: "stub_loop", Kind: providerkind.OpenAI, Backend: be, DefaultModel: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: "usage_loop", LlmProviderId: strPtr("stub_loop")}, testScope); err != nil {
		t.Fatal(err)
	}
	tools := toolregistry.New()
	handlers := toolexec.NewHandlers()
	if _, err := InstallBuiltinTools(tools, handlers,
		registerFakeRelatedTool(map[string]map[string][]string{testScope.TenantID: {"src/a.go": {"src/b.go"}}}),
		registerFakeFetchTool(map[string]map[string]string{testScope.TenantID: {"src/a.go": "package a\n\nfunc A() {}\n"}})); err != nil {
		t.Fatal(err)
	}
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		Providers:       providers.NewComposite(reg, nil),
		Tools:           tools,
		Handlers:        handlers,
		SessionRuntimes: mgr,
		TurnRegister:    store.NewTurnRegister(db),
		Usage:           store.NewUsageRepo(db),
	})
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
		SessionId: strPtr("usage_loop"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := stream.Recv(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	rounds := be.roundCount()
	if rounds != 3 {
		t.Fatalf("rounds=%d, want 3", rounds)
	}
	rows := usageRows(t, db)
	if len(rows) != rounds {
		t.Fatalf("rows=%d want %d (one per provider round)", len(rows), rounds)
	}
	for _, r := range rows {
		if r.TenantID != testScope.TenantID || r.RPC != "ChatStream" || r.ProviderID != "stub_loop" {
			t.Fatalf("unexpected row: %+v", r)
		}
	}
}
