package server

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/sashabaranov/go-openai"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const rpcSecret = "sk-super-secret-key-123"

// rateLimitErr is a provider error whose text contains a secret.
func rateLimitErr() error {
	return &openai.APIError{
		HTTPStatusCode: 429,
		Message:        "rate limited for key " + rpcSecret + " at https://api.example.com/v1?key=" + rpcSecret + ". Please try again in 1.5s",
	}
}

// failingStreamBackend fails ChatStream with a fixed error.
type failingStreamBackend struct {
	completeBackend
	streamErr error
}

func (b *failingStreamBackend) ChatStream(context.Context, []*llmv1.Message, *llmv1.CallOptions, func() []*llmv1.ToolDefinition, func(*llmv1.ConversationStreamFrame) error) error {
	return b.streamErr
}

func rpcClient(t *testing.T, s *LlmServer) (llmv1.LlmServiceClient, context.Context) {
	t.Helper()
	ctx := testScope.OutgoingContext(context.Background())
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, s)
	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("grpc serve: %v", err)
		}
	}()
	t.Cleanup(srv.Stop)
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return llmv1.NewLlmServiceClient(conn), ctx
}

func assertRateLimitDetail(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("not a status error: %v", err)
	}
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("code = %v, want ResourceExhausted", st.Code())
	}
	if strings.Contains(st.Message(), rpcSecret) || strings.Contains(err.Error(), rpcSecret) {
		t.Errorf("secret leaked in error: %v", err)
	}
	var detail *llmv1.LlmError
	for _, d := range st.Details() {
		if le, ok := d.(*llmv1.LlmError); ok {
			detail = le
		}
	}
	if detail == nil {
		t.Fatalf("no LlmError detail in %v", st.Proto())
	}
	if detail.GetKind() != llmv1.LlmErrorKind_LLM_ERROR_KIND_RATE_LIMITED || !detail.GetRetryable() || detail.GetProviderStatus() != 429 {
		t.Errorf("detail = %+v", detail)
	}
	if strings.Contains(detail.String(), rpcSecret) {
		t.Errorf("secret leaked in detail: %v", detail)
	}
	for _, leak := range []string{"https://", "api.example.com"} {
		if strings.Contains(st.Message(), leak) || strings.Contains(detail.String(), leak) {
			t.Errorf("%q leaked in message %q or detail %v", leak, st.Message(), detail)
		}
	}
	if ra := detail.GetRetryAfter(); ra == nil {
		t.Error("retry_after not set")
	} else if got := ra.AsDuration(); got != 1500*time.Millisecond {
		t.Errorf("retry_after = %v, want 1.5s", got)
	}
}

func TestComplete_ProviderErrorCarriesLlmErrorDetail(t *testing.T) {
	s, be := newCompleteTestServer(t)
	be.chatErr = rateLimitErr()
	client, ctx := rpcClient(t, s)
	_, err := client.Complete(ctx, &llmv1.CompleteRequest{ProviderId: "claude", Messages: userMsgs()})
	assertRateLimitDetail(t, err)
}

func TestGenerateOnce_ProviderErrorCarriesLlmErrorDetail(t *testing.T) {
	s, be := newCompleteTestServer(t)
	be.generateErr = rateLimitErr()
	client, ctx := rpcClient(t, s)
	_, err := client.GenerateOnce(ctx, &llmv1.GenerateOnceRequest{ProviderId: strPtr("claude"), Prompt: "hi"})
	assertRateLimitDetail(t, err)
}

func TestCompleteStream_ProviderErrorCarriesLlmErrorDetail(t *testing.T) {
	s, _ := newCompleteTestServer(t)
	e, ok := s.Providers.Get(context.Background(), testScope, "claude")
	if !ok || e == nil {
		t.Fatal("provider claude not resolvable")
	}
	e.Backend = &failingStreamBackend{streamErr: rateLimitErr()}
	client, ctx := rpcClient(t, s)
	stream, err := client.CompleteStream(ctx, &llmv1.CompleteRequest{ProviderId: "claude", Messages: userMsgs()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	assertRateLimitDetail(t, err)
}

func TestComplete_ExistingStatusErrorPassesThrough(t *testing.T) {
	s, be := newCompleteTestServer(t)
	be.chatErr = status.Error(codes.PermissionDenied, "nope")
	_, err := s.Complete(testScope.IncomingContext(context.Background()), &llmv1.CompleteRequest{ProviderId: "claude", Messages: userMsgs()})
	if status.Code(err) != codes.PermissionDenied || status.Convert(err).Message() != "nope" {
		t.Fatalf("err = %v, want unchanged PermissionDenied", err)
	}
	if len(status.Convert(err).Details()) != 0 {
		t.Errorf("unexpected details on pass-through status")
	}
}
