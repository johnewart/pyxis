package server

// Auto-reconciliation of a session's bound provider/model from an inline
// ChatStream CallOptions, without a separate EnsureSession/UpdateSessionLlm
// round trip. See reconcileSessionLlmBinding in server.go.

import (
	"context"
	"io"
	"net"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func drainChatStream(t *testing.T, st llmv1.LlmService_ChatStreamClient) error {
	t.Helper()
	for {
		_, err := st.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// TestChatStream_ReconcilesSessionLlmBindingFromCallOptions pins the flow
// the user asked for: a session starts unbound, its first message's
// CallOptions.provider_id/model set the binding, and a later message naming
// a different provider switches it — all without a separate
// EnsureSession/UpdateSessionLlm call.
func TestChatStream_ReconcilesSessionLlmBindingFromCallOptions(t *testing.T) {
	capA := &captureStreamBackend{}
	capB := &captureStreamBackend{}
	reg, err := providers.NewRegistryFromEntries("provider_a", map[string]*providers.Entry{
		"provider_a": {ID: "provider_a", Kind: providerkind.OpenAI, Backend: capA, DefaultModel: "model-a"},
		"provider_b": {ID: "provider_b", Kind: providerkind.OpenAI, Backend: capB, DefaultModel: "model-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	comp := providers.NewComposite(reg, nil)

	// Session created with NO provider/model bound — mirrors "created with no
	// model/provider" from the ask.
	mgr := testAgentManagerWithCreate(t, &llmv1.EnsureSessionRequest{SessionId: "sess_reconcile"})

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		Providers:       comp,
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
	})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	ctx := testScope.OutgoingContext(context.Background())
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := llmv1.NewLlmServiceClient(conn)

	// First message names provider_b explicitly — this should both dispatch
	// to it AND persist it as the session's bound provider.
	st, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "hello", Id: "u1"}},
		SessionId:      strPtr("sess_reconcile"),
		Options:        &llmv1.CallOptions{ProviderId: strPtr("provider_b")},
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := drainChatStream(t, st); err != nil {
		t.Fatal(err)
	}
	if len(capA.saw) != 0 {
		t.Fatalf("provider_a should not have been called, got %d calls", len(capA.saw))
	}
	if len(capB.saw) != 1 {
		t.Fatalf("provider_b should have been called once, got %d calls", len(capB.saw))
	}
	ma, ok := mgr.Get(testScope, "sess_reconcile")
	if !ok {
		t.Fatal("missing session")
	}
	if ma.ProviderID != "provider_b" {
		t.Fatalf("session should now be bound to provider_b, got %q", ma.ProviderID)
	}

	// Second message carries no options at all — the binding must stick
	// (sticky until a message names a different one).
	st, err = client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "again", Id: "u2"}},
		SessionId:      strPtr("sess_reconcile"),
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := drainChatStream(t, st); err != nil {
		t.Fatal(err)
	}
	if len(capB.saw) != 2 {
		t.Fatalf("provider_b should still be bound and called again, got %d calls", len(capB.saw))
	}

	// Third message names provider_a — the session must switch.
	st, err = client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "switch", Id: "u3"}},
		SessionId:      strPtr("sess_reconcile"),
		Options:        &llmv1.CallOptions{ProviderId: strPtr("provider_a")},
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := drainChatStream(t, st); err != nil {
		t.Fatal(err)
	}
	if len(capA.saw) != 1 {
		t.Fatalf("provider_a should have been called once after switch, got %d calls", len(capA.saw))
	}
	ma, ok = mgr.Get(testScope, "sess_reconcile")
	if !ok {
		t.Fatal("missing session")
	}
	if ma.ProviderID != "provider_a" {
		t.Fatalf("session should now be bound to provider_a, got %q", ma.ProviderID)
	}
}

// TestChatStream_ReconcileRejectsUnknownProvider pins that naming an unknown
// provider inline fails the turn cleanly rather than silently rebinding.
func TestChatStream_ReconcileRejectsUnknownProvider(t *testing.T) {
	reg, err := providers.NewRegistryFromEntries("provider_a", map[string]*providers.Entry{
		"provider_a": {ID: "provider_a", Kind: providerkind.OpenAI, Backend: &captureStreamBackend{}, DefaultModel: "model-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	comp := providers.NewComposite(reg, nil)
	mgr := testAgentManagerWithCreate(t, &llmv1.EnsureSessionRequest{SessionId: "sess_reject"})

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		Providers:       comp,
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
	})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	ctx := testScope.OutgoingContext(context.Background())
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := llmv1.NewLlmServiceClient(conn)

	st, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "hello", Id: "u1"}},
		SessionId:      strPtr("sess_reject"),
		Options:        &llmv1.CallOptions{ProviderId: strPtr("does_not_exist")},
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = drainChatStream(t, st)
	if err == nil {
		t.Fatal("expected an error for an unknown provider_id")
	}
	if status.Code(err).String() != "InvalidArgument" {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
	ma, ok := mgr.Get(testScope, "sess_reject")
	if !ok {
		t.Fatal("missing session")
	}
	if ma.ProviderID != "" {
		t.Fatalf("session should remain unbound after a rejected switch, got %q", ma.ProviderID)
	}
}
