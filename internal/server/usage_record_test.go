package server

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/gorm"
)

func usageTestClient(t *testing.T, be *stubBackend, sessionID string) (llmv1.LlmServiceClient, *gorm.DB) {
	t.Helper()
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(context.Background(), &llmv1.EnsureSessionRequest{SessionId: sessionID}, testScope); err != nil {
		t.Fatal(err)
	}
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		LLM:             be,
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		Usage:           store.NewUsageRepo(db),
	})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })
	conn, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return llmv1.NewLlmServiceClient(conn), db
}

func drainChat(t *testing.T, c llmv1.LlmServiceClient, ctx context.Context, sid string) error {
	t.Helper()
	stream, err := c.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "hi", Id: "u1"}},
		Options:        &llmv1.CallOptions{Model: "m-chat"},
		SessionId:      strPtr(sid),
		PersistSession: true,
	})
	if err != nil {
		return err
	}
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func TestUsage_ChatStreamRecordsOneRowPerCall(t *testing.T) {
	c, db := usageTestClient(t, &stubBackend{}, "usess")
	ctx := metadata.AppendToOutgoingContext(testScope.OutgoingContext(context.Background()), principalMetadataKey, "alice")
	if err := drainChat(t, c, ctx, "usess"); err != nil {
		t.Fatal(err)
	}
	var rows []store.UsageRecordRow
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d want 1", len(rows))
	}
	r := rows[0]
	if r.TenantID != testScope.TenantID || r.Principal != "alice" || r.RPC != "ChatStream" || r.Outcome != usageOutcomeOK ||
		r.ScopeID != testScope.ScopeID || r.Model != "m-chat" {
		t.Fatalf("unexpected row: %+v", r)
	}
}

func TestUsage_ChatStreamErrorRecordsOutcome(t *testing.T) {
	c, db := usageTestClient(t, &stubBackend{chatStreamErr: errors.New("boom")}, "usess")
	ctx := testScope.OutgoingContext(context.Background())
	if err := drainChat(t, c, ctx, "usess"); err == nil {
		t.Fatal("expected error")
	}
	var rows []store.UsageRecordRow
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Outcome != llmv1.LlmErrorKind_LLM_ERROR_KIND_UNKNOWN.String() {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestUsage_WriteFailureOnlyLogged(t *testing.T) {
	c, db := usageTestClient(t, &stubBackend{}, "usess")
	if err := db.Migrator().DropTable(&store.UsageRecordRow{}); err != nil {
		t.Fatal(err)
	}
	ctx := testScope.OutgoingContext(context.Background())
	if err := drainChat(t, c, ctx, "usess"); err != nil {
		t.Fatalf("usage write failure must not fail the RPC: %v", err)
	}
}

func TestUsage_QueryUsageAggregatesAndIsolatesTenants(t *testing.T) {
	c, db := usageTestClient(t, &stubBackend{}, "usess")
	repo := store.NewUsageRepo(db)
	for _, row := range []store.UsageRecordRow{
		{TenantID: testScope.TenantID, Model: "m1", InputTokens: 10, OutputTokens: 5, ReasoningTokens: 2, CostUSD: 0.5},
		{TenantID: testScope.TenantID, Model: "m1", InputTokens: 20, OutputTokens: 7, CostUSD: 0.25},
		{TenantID: "other", Model: "m1", InputTokens: 999},
	} {
		if err := repo.RecordUsage(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}
	ctx := testScope.OutgoingContext(context.Background())
	resp, err := c.QueryUsage(ctx, &llmv1.QueryUsageRequest{GroupBy: llmv1.UsageGroupBy_USAGE_GROUP_BY_AGGREGATE})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetRows()) != 0 || len(resp.GetTotals()) != 1 {
		t.Fatalf("unexpected response: %v", resp)
	}
	tot := resp.GetTotals()[0]
	if tot.GetCallCount() != 2 || tot.GetInputTokens() != 30 || tot.GetOutputTokens() != 12 || tot.GetReasoningTokens() != 2 || tot.GetCostUsd() != 0.75 {
		t.Fatalf("unexpected totals: %v", tot)
	}
	if _, err := c.QueryUsage(ctx, &llmv1.QueryUsageRequest{TenantId: "other"}); err == nil {
		t.Fatal("expected cross-tenant query to be refused")
	}
	rowsResp, err := c.QueryUsage(ctx, &llmv1.QueryUsageRequest{GroupBy: llmv1.UsageGroupBy_USAGE_GROUP_BY_ROWS})
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsResp.GetRows()) != 2 || len(rowsResp.GetTotals()) != 0 {
		t.Fatalf("unexpected rows response: %v", rowsResp)
	}
}

var _ = serverutil.Scope{}
