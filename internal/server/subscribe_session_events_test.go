package server

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
)

func appendFrame(t *testing.T, events *store.SessionEventRepo, f *llmv1.ConversationStreamFrame) {
	t.Helper()
	b, err := protojson.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.AppendEvent(context.Background(), testScope, testSessionID, string(b)); err != nil {
		t.Fatal(err)
	}
}

func subTextDelta(s string) *llmv1.ConversationStreamFrame {
	return &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
		AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: "t", Delta: s},
	}}
}

// Rejoining a running turn: frames persisted before the subscriber arrived are
// replayed DECODED (oneof intact — replay used to run protojson rows through
// encoding/json and deliver empty frames), then every frame appended afterwards
// arrives exactly once and in order, even when they are produced in a burst far
// faster than one poke per frame.
func TestSubscribeSessionEvents_ReplayThenLive_NoGapNoDup(t *testing.T) {
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{})
	ctx := context.Background()
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: testSessionID}, testScope); err != nil {
		t.Fatal(err)
	}
	events := store.NewSessionEventRepo(db)
	llmSrv := &LlmServer{SessionRuntimes: mgr, SessionEvents: events}

	// Already streamed before the client came back.
	appendFrame(t, events, subTextDelta("d0"))
	appendFrame(t, events, subTextDelta("d1"))

	lis := bufconn.Listen(bufSize)
	gs := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(gs, llmSrv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	dialCtx := testScope.OutgoingContext(context.Background())
	conn, err := grpc.DialContext(dialCtx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	subCtx, cancel := context.WithCancel(dialCtx)
	t.Cleanup(cancel)
	stream, err := llmv1.NewLlmServiceClient(conn).SubscribeSessionEvents(subCtx, &llmv1.SubscribeSessionEventsRequest{SessionId: testSessionID})
	if err != nil {
		t.Fatal(err)
	}

	const live = 50
	got := make(chan string, live+2)
	go func() {
		for {
			f, err := stream.Recv()
			if err != nil {
				close(got)
				return
			}
			got <- f.GetAssistantTextDelta().GetDelta()
		}
	}()

	// Burst: no waiting between frames, so some land while the subscriber is
	// still replaying. None may be lost or repeated.
	for i := 2; i < live+2; i++ {
		appendFrame(t, events, subTextDelta(fmt.Sprintf("d%d", i)))
	}

	timeout := time.After(10 * time.Second)
	for i := 0; i < live+2; i++ {
		select {
		case d, open := <-got:
			if !open {
				t.Fatalf("stream ended after %d frames", i)
			}
			if want := fmt.Sprintf("d%d", i); d != want {
				t.Fatalf("frame %d: got delta %q, want %q (gap, duplicate, or undecoded replay)", i, d, want)
			}
		case <-timeout:
			t.Fatalf("timed out after %d of %d frames", i, live+2)
		}
	}
	select {
	case d := <-got:
		t.Fatalf("unexpected extra frame %q (duplicate delivery)", d)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestSubscribeSessionEvents_LiveTail is the regression test for
// SubscribeSessionEvents being a one-shot replay-then-close instead of a
// genuine live subscription: a frame appended to the session's event log
// AFTER the RPC's initial replay has already finished (simulating e.g. an
// MCP-dispatched tool call landing concurrently with an open subscription —
// see internal/mcpserver/server.go's publishToolEventFrame and
// SubscribeSessionEvents' own doc comment) must still arrive on the open
// stream, without the caller having to reconnect.
func TestSubscribeSessionEvents_LiveTail(t *testing.T) {
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{})
	ctx := context.Background()
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: testSessionID}, testScope); err != nil {
		t.Fatal(err)
	}

	events := store.NewSessionEventRepo(db)
	llmSrv := &LlmServer{
		SessionRuntimes: mgr,
		SessionEvents:   events,
	}

	lis := bufconn.Listen(bufSize)
	gs := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(gs, llmSrv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	dialCtx := testScope.OutgoingContext(context.Background())
	conn, err := grpc.DialContext(dialCtx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := llmv1.NewLlmServiceClient(conn)
	subCtx, cancel := context.WithCancel(dialCtx)
	t.Cleanup(cancel)
	stream, err := client.SubscribeSessionEvents(subCtx, &llmv1.SubscribeSessionEventsRequest{
		SessionId: testSessionID,
		AfterSeq:  0,
	})
	if err != nil {
		t.Fatal(err)
	}

	frameCh := make(chan *llmv1.ConversationStreamFrame, 8)
	errCh := make(chan error, 1)
	go func() {
		for {
			f, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			frameCh <- f
		}
	}()

	// The empty-session replay completes near-instantly; wait so the append
	// below exercises the live path rather than the replay read.
	time.Sleep(50 * time.Millisecond)

	want := &llmv1.ConversationStreamFrame{
		Frame: &llmv1.ConversationStreamFrame_ToolExecutionStarted{
			ToolExecutionStarted: &llmv1.EvToolExecutionStarted{
				TurnId:     "turn-1",
				ToolCallId: "call-1",
				Name:       "live_tail_probe",
			},
		},
	}
	// Append to the durable log only: the log pushes to live tails.
	appendFrame(t, events, want)

	select {
	case got := <-frameCh:
		started := got.GetToolExecutionStarted()
		if started == nil {
			t.Fatalf("got frame is not ToolExecutionStarted: %+v", got)
		}
		if started.GetName() != "live_tail_probe" || started.GetToolCallId() != "call-1" {
			t.Fatalf("got %+v, want name=live_tail_probe call_id=call-1", started)
		}
	case err := <-errCh:
		t.Fatalf("stream ended before delivering the live frame: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for live-dispatched frame on SubscribeSessionEvents")
	}
}
