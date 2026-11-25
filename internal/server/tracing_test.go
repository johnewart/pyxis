package server

import (
	"context"
	"io"
	"net"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/internal/tracing"
	"github.com/johnewart/pyxis/toolregistry"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

const fixedTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

func newRecorderTP() (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	rec := tracetest.NewSpanRecorder()
	return sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)), rec
}

// tracedLoopServer starts a bufconn server with the otelgrpc stats handler
// and returns a client, the span recorder and an outgoing context.
func tracedLoopServer(t *testing.T, withTracer bool) (llmv1.LlmServiceClient, *tracetest.SpanRecorder, context.Context) {
	t.Helper()
	ctx := testScope.OutgoingContext(context.Background())
	tp, rec := newRecorderTP()

	be := &builtinLoopBackend{}
	reg, err := providers.NewRegistryFromEntries("stub_loop", map[string]*providers.Entry{
		"stub_loop": {ID: "stub_loop", Kind: providerkind.OpenAI, Backend: be, DefaultModel: "gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: "agent_loop", LlmProviderId: strPtr("stub_loop")}, testScope); err != nil {
		t.Fatal(err)
	}
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
	}
	if withTracer {
		llmSrv.Tracer = tp.Tracer("llmd-test")
	}

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler(
		otelgrpc.WithTracerProvider(tp),
		otelgrpc.WithPropagators(tracing.Propagator()),
	)))
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
	return llmv1.NewLlmServiceClient(conn), rec, ctx
}

func runLoopTurn(t *testing.T, ctx context.Context, c llmv1.LlmServiceClient) {
	t.Helper()
	stream, err := c.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:  []*llmv1.Message{{Role: "user", Content: "who imports A()?", Id: "u1"}},
		SessionId: strPtr("agent_loop"),
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "identity", Content: "You are a helpful assistant."},
		},
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

func TestChatStreamContinuesIncomingTrace(t *testing.T) {
	c, rec, ctx := tracedLoopServer(t, true)
	ctx = metadata.AppendToOutgoingContext(ctx, "traceparent", "00-"+fixedTraceID+"-00f067aa0ba902b7-01")
	runLoopTurn(t, ctx, c)

	spans := rec.Ended()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	var sawTurn bool
	for _, s := range spans {
		if got := s.SpanContext().TraceID().String(); got != fixedTraceID {
			t.Errorf("span %q trace id = %s, want %s", s.Name(), got, fixedTraceID)
		}
		if s.Name() == "turn" {
			sawTurn = true
		}
	}
	if !sawTurn {
		t.Error("no turn span recorded")
	}
}

func TestToolLoopSpans(t *testing.T) {
	c, rec, ctx := tracedLoopServer(t, true)
	runLoopTurn(t, ctx, c)

	byName := map[string][]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		byName[s.Name()] = append(byName[s.Name()], s)
	}
	for _, n := range []string{"turn", "llm.round", "provider.chat_stream", "tool.execute"} {
		if len(byName[n]) == 0 {
			t.Fatalf("missing span %q", n)
		}
	}
	turn := byName["turn"][0]
	for _, r := range byName["llm.round"] {
		if r.Parent().SpanID() != turn.SpanContext().SpanID() {
			t.Errorf("llm.round parent = %s, want turn", r.Parent().SpanID())
		}
	}
	for _, x := range byName["tool.execute"] {
		if x.Parent().SpanID() != turn.SpanContext().SpanID() {
			t.Errorf("tool.execute parent = %s, want turn", x.Parent().SpanID())
		}
	}
	roundIDs := map[string]bool{}
	for _, r := range byName["llm.round"] {
		roundIDs[r.SpanContext().SpanID().String()] = true
	}
	for _, p := range byName["provider.chat_stream"] {
		if !roundIDs[p.Parent().SpanID().String()] {
			t.Errorf("provider.chat_stream parent %s is not an llm.round", p.Parent().SpanID())
		}
	}

	attrs := map[string]string{}
	for _, s := range rec.Ended() {
		for _, kv := range s.Attributes() {
			attrs[string(kv.Key)] = kv.Value.Emit()
			if kv.Value.Emit() == "who imports A()?" {
				t.Errorf("span %q attribute %s carries message content", s.Name(), kv.Key)
			}
		}
	}
	for _, k := range []string{"llmd.model", "llmd.provider.id", "llmd.provider.type", "llmd.tool.name", "llmd.session.id", "llmd.round", "llmd.outcome"} {
		if _, ok := attrs[k]; !ok {
			t.Errorf("missing attribute %s; have %v", k, attrs)
		}
	}
}

func TestCompleteProviderSpan(t *testing.T) {
	s, _ := newCompleteTestServer(t)
	tp, rec := newRecorderTP()
	s.Tracer = tp.Tracer("llmd-test")
	if _, err := s.Complete(testScope.IncomingContext(context.Background()), &llmv1.CompleteRequest{ProviderId: "claude", Messages: userMsgs()}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for _, sp := range rec.Ended() {
		if sp.Name() == "provider.chat_stream" {
			return
		}
	}
	t.Fatal("no provider.chat_stream span from Complete")
}

func TestNilTracerWorks(t *testing.T) {
	c, rec, ctx := tracedLoopServer(t, false)
	runLoopTurn(t, ctx, c)
	for _, s := range rec.Ended() {
		if s.Name() == "turn" || s.Name() == "llm.round" {
			t.Errorf("unexpected llmd span %q with nil Tracer", s.Name())
		}
	}
}
