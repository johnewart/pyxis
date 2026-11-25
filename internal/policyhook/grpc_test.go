package policyhook

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// fakeServer is an in-process PolicyHookServiceServer.
type fakeServer struct {
	llmv1.UnimplementedPolicyHookServiceServer
	decision    llmv1.Decision
	delay       time.Duration
	policyCalls atomic.Int32
	lastRequest atomic.Pointer[llmv1.CheckLLMCallRequest]
}

func (f *fakeServer) GetCheckPolicy(ctx context.Context, _ *llmv1.GetCheckPolicyRequest) (*llmv1.GetCheckPolicyResponse, error) {
	f.policyCalls.Add(1)
	return &llmv1.GetCheckPolicyResponse{
		LlmCall: &llmv1.CheckPolicy{Content: llmv1.ContentScope_CONTENT_SCOPE_FULL, Cadence: llmv1.Cadence_CADENCE_EVERY_N_ROUNDS, EveryNRounds: 2},
	}, nil
}

func (f *fakeServer) wait(ctx context.Context) error {
	if f.delay == 0 {
		return nil
	}
	select {
	case <-time.After(f.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeServer) CheckLLMCall(ctx context.Context, r *llmv1.CheckLLMCallRequest) (*llmv1.CheckLLMCallResponse, error) {
	f.lastRequest.Store(r)
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	return &llmv1.CheckLLMCallResponse{Decision: f.decision, Reason: "fake", UserMessage: "blocked by fake"}, nil
}

func (f *fakeServer) FilterTools(ctx context.Context, r *llmv1.FilterToolsRequest) (*llmv1.FilterToolsResponse, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	return &llmv1.FilterToolsResponse{Decision: llmv1.Decision_DECISION_ALLOW, AllowedToolNames: []string{"keep"}}, nil
}

func (f *fakeServer) CheckToolCall(ctx context.Context, r *llmv1.CheckToolCallRequest) (*llmv1.CheckToolCallResponse, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	return &llmv1.CheckToolCallResponse{Decision: f.decision, Reason: "fake"}, nil
}

func dialFake(t *testing.T, f *fakeServer) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	llmv1.RegisterPolicyHookServiceServer(srv, f)
	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("fake server stopped: %v", err)
		}
	}()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})
	return conn
}

func TestPolicyProtoGenerated(t *testing.T) {
	conn := dialFake(t, &fakeServer{})
	c := llmv1.NewPolicyHookServiceClient(conn)
	resp, err := c.GetCheckPolicy(context.Background(), &llmv1.GetCheckPolicyRequest{})
	if err != nil {
		t.Fatalf("GetCheckPolicy: %v", err)
	}
	p := resp.GetLlmCall()
	if p.GetContent() != llmv1.ContentScope_CONTENT_SCOPE_FULL ||
		p.GetCadence() != llmv1.Cadence_CADENCE_EVERY_N_ROUNDS || p.GetEveryNRounds() != 2 {
		t.Fatalf("unexpected policy %v", p)
	}
}

func TestGRPCClientAllow(t *testing.T) {
	f := &fakeServer{decision: llmv1.Decision_DECISION_ALLOW}
	c := NewGRPCClient(dialFake(t, f), time.Second)
	ctx := context.Background()

	resp, err := c.CheckLLMCall(ctx, LLMCallRequest{AgentID: "a1", Round: 3, Model: "m"})
	if err != nil {
		t.Fatalf("CheckLLMCall: %v", err)
	}
	if resp.Decision != DecisionAllow {
		t.Fatalf("decision = %v", resp.Decision)
	}
	if got := f.lastRequest.Load(); got.GetAgentId() != "a1" || got.GetRound() != 3 {
		t.Fatalf("server saw %v", got)
	}

	ft, err := c.FilterTools(ctx, FilterToolsRequest{Tools: []*llmv1.ToolDefinition{{Name: "keep"}, {Name: "drop"}}})
	if err != nil || len(ft.AllowedToolNames) != 1 || ft.AllowedToolNames[0] != "keep" {
		t.Fatalf("FilterTools = %+v, %v", ft, err)
	}

	// GetCheckPolicy is cached: two calls, one round trip.
	for i := 0; i < 2; i++ {
		p, err := c.GetCheckPolicy(ctx)
		if err != nil || p.LLMCall == nil || p.LLMCall.EveryNRounds != 2 {
			t.Fatalf("GetCheckPolicy = %+v, %v", p, err)
		}
	}
	if n := f.policyCalls.Load(); n != 1 {
		t.Fatalf("server GetCheckPolicy calls = %d, want 1", n)
	}
	// Refresh (reconnect) fetches again.
	if err := c.RefreshPolicy(ctx); err != nil {
		t.Fatalf("RefreshPolicy: %v", err)
	}
	if n := f.policyCalls.Load(); n != 2 {
		t.Fatalf("after refresh calls = %d, want 2", n)
	}
}

func TestGRPCClientDeny(t *testing.T) {
	f := &fakeServer{decision: llmv1.Decision_DECISION_DENY}
	c := NewGRPCClient(dialFake(t, f), time.Second)
	resp, err := c.CheckLLMCall(context.Background(), LLMCallRequest{})
	if err != nil {
		t.Fatalf("CheckLLMCall: %v", err)
	}
	if resp.Decision != DecisionDeny || resp.Reason != "fake" || resp.UserMessage != "blocked by fake" {
		t.Fatalf("unexpected response %+v", resp)
	}
	tc, err := c.CheckToolCall(context.Background(), ToolCallRequest{ToolCallID: "c1", Name: "x"})
	if err != nil || tc.Decision != DecisionDeny {
		t.Fatalf("CheckToolCall = %+v, %v", tc, err)
	}
}

func TestGRPCClientTimeout(t *testing.T) {
	f := &fakeServer{decision: llmv1.Decision_DECISION_ALLOW, delay: 2 * time.Second}
	c := NewGRPCClient(dialFake(t, f), 50*time.Millisecond)
	start := time.Now()
	_, err := c.CheckLLMCall(context.Background(), LLMCallRequest{})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !IsTimeout(err) {
		t.Fatalf("IsTimeout(%v) = false", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("call took %v, timeout not applied", time.Since(start))
	}
}
