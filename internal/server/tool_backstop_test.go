package server

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/tooltimeout"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// backstopCaptureBackend records the tool backstop the server attached to the
// backend ctx, and the max_duration_seconds visible on the (stripped) tools.
type backstopCaptureBackend struct {
	stubBackend
	mu          sync.Mutex
	backstop    time.Duration
	sawBackstop bool
	strippedMax uint32
}

func (b *backstopCaptureBackend) ChatStream(ctx context.Context, msgs []*llmv1.Message, opt *llmv1.CallOptions, tf func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.mu.Lock()
	b.backstop, b.sawBackstop = tooltimeout.BackstopFromContext(ctx)
	for _, d := range tf() {
		if d.GetName() == "slow_exec" {
			b.strippedMax = d.GetExecutor().GetMaxDurationSeconds()
		}
	}
	b.mu.Unlock()
	return b.stubBackend.ChatStream(ctx, msgs, opt, tf, emit)
}

// A tool declaring 30m must yield a backstop of 30m + 2m (margin ceiling) on
// the backend ctx, even though the provider's tool list is stripped of
// execution metadata. This is what the Claude Code CLI turns into
// MCP_TOOL_TIMEOUT.
func TestChatStream_attachesToolBackstopFromFullDefinitions(t *testing.T) {
	t.Setenv(tooltimeout.CapEnvVar, "")
	t.Setenv(tooltimeout.DefaultEnvVar, "")

	tools := toolregistry.New()
	if err := tools.Register(&llmv1.ToolDefinition{
		Name:                 "slow_exec",
		Description:          "long-running executor tool",
		ParametersSchemaJson: `{"type":"object"}`,
		Execution: &llmv1.ToolDefinition_Executor{
			Executor: &llmv1.ToolExecutionExecutor{ExecutorId: "exec1", MaxDurationSeconds: 1800},
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	be := &backstopCaptureBackend{}
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	mgr := testAgentManagerWithCreate(t, &llmv1.EnsureSessionRequest{SessionId: "sess_backstop"})
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		LLM:             be,
		Tools:           tools,
		SessionRuntimes: mgr,
	})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	ctx := testScope.OutgoingContext(context.Background())
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	stream, err := llmv1.NewLlmServiceClient(conn).ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "hi", Id: "u1"}},
		SessionId:      strPtr("sess_backstop"),
		PersistSession: true,
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

	be.mu.Lock()
	defer be.mu.Unlock()
	if be.strippedMax != 0 {
		t.Fatalf("provider tool list leaked executor metadata (max=%d); test premise broken", be.strippedMax)
	}
	if !be.sawBackstop {
		t.Fatal("backend ctx carried no tool backstop")
	}
	if want := 32 * time.Minute; be.backstop != want {
		t.Fatalf("backstop = %s, want %s (30m tool + 2m margin)", be.backstop, want)
	}
}
