package server

import (
	"context"
	"net"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func toolUsageQueryClient(t *testing.T, repo *store.ToolUsageRepo) llmv1.LlmServiceClient {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{Tools: toolregistry.New(), ToolUsage: repo})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })
	conn, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return llmv1.NewLlmServiceClient(conn)
}

func TestQueryToolUsage_Stats(t *testing.T) {
	repo := store.NewToolUsageRepo(store.NewTestSQLite(t))
	ctx := context.Background()
	for i, d := range []int64{10, 20, 40, 30} {
		row := store.ToolUsageRow{TenantID: testScope.TenantID, ToolName: "a", Source: "chat", Success: i != 0, DurationMs: d}
		if err := repo.Record(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Record(ctx, store.ToolUsageRow{TenantID: "other", ToolName: "a", Success: true, DurationMs: 999}); err != nil {
		t.Fatal(err)
	}
	c := toolUsageQueryClient(t, repo)
	now := time.Now().Unix()
	resp, err := c.QueryToolUsage(testScope.OutgoingContext(ctx), &llmv1.QueryToolUsageRequest{SinceUnix: now - 60, UntilUnix: now + 60})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetStats()) != 1 {
		t.Fatalf("stats=%v", resp.GetStats())
	}
	s := resp.GetStats()[0]
	if s.GetToolName() != "a" || s.GetCalls() != 4 || s.GetErrors() != 1 || s.GetMinMs() != 10 ||
		s.GetMedianMs() != 25 || s.GetMeanMs() != 25 || s.GetMaxMs() != 40 {
		t.Fatalf("unexpected stat: %v", s)
	}
}

func TestQueryToolUsage_TenantMismatchRefused(t *testing.T) {
	c := toolUsageQueryClient(t, store.NewToolUsageRepo(store.NewTestSQLite(t)))
	_, err := c.QueryToolUsage(testScope.OutgoingContext(context.Background()), &llmv1.QueryToolUsageRequest{TenantId: "other"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("err=%v, want PermissionDenied", err)
	}
}

func TestQueryToolUsage_ScopeMismatchRefused(t *testing.T) {
	c := toolUsageQueryClient(t, store.NewToolUsageRepo(store.NewTestSQLite(t)))
	sc := serverutil.Scope{TenantID: testScope.TenantID, ScopeID: "scope-a"}
	other := "scope-b"
	_, err := c.QueryToolUsage(sc.OutgoingContext(context.Background()), &llmv1.QueryToolUsageRequest{ScopeId: &other})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("err=%v, want PermissionDenied", err)
	}
}

func TestQueryToolUsage_ScopeForcedWhenCallerScoped(t *testing.T) {
	repo := store.NewToolUsageRepo(store.NewTestSQLite(t))
	ctx := context.Background()
	for scope, tool := range map[string]string{"scope-a": "mine", "scope-b": "theirs"} {
		if err := repo.Record(ctx, store.ToolUsageRow{TenantID: testScope.TenantID, ScopeID: scope, ToolName: tool, Success: true, DurationMs: 5}); err != nil {
			t.Fatal(err)
		}
	}
	c := toolUsageQueryClient(t, repo)
	sc := serverutil.Scope{TenantID: testScope.TenantID, ScopeID: "scope-a"}
	resp, err := c.QueryToolUsage(sc.OutgoingContext(ctx), &llmv1.QueryToolUsageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetStats()) != 1 || resp.GetStats()[0].GetToolName() != "mine" {
		t.Fatalf("scope not forced: %v", resp.GetStats())
	}
}
