package server

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	conversationevents "github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

func int64Ptr(v int64) *int64 { return &v }

// countRows counts rows of the given gorm model across all tenants.
func countRows(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(model).Count(&n).Error; err != nil {
		t.Fatalf("count %T: %v", model, err)
	}
	return n
}

func assertNoSessionRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, m := range []any{&store.SessionRow{}, &store.ChatMessageRow{}, &store.SessionTurnRow{}, &store.SessionEventRow{}, &store.TurnMetadataRow{}} {
		if n := countRows(t, db, m); n != 0 {
			t.Errorf("%T has %d rows, want 0", m, n)
		}
	}
}

func TestComplete_MultiTurnMessagesForwardedInOrderUnchanged(t *testing.T) {
	s, be := newCompleteTestServer(t)
	in := []*llmv1.Message{
		{Role: "user", Content: "look it up", Id: "u1"},
		{Role: "assistant", Content: "calling", Id: "a1", ToolCalls: []*llmv1.ToolCall{{Id: "call_9", Name: "lookup", ArgumentsJson: `{"q":"y"}`}}},
		{Role: "tool", Content: "result", Id: "t1", ToolCallId: "call_9"},
		{Role: "user", Content: "thanks", Id: "u2"},
	}
	if _, err := s.Complete(testScope.IncomingContext(context.Background()), &llmv1.CompleteRequest{ProviderId: "claude", Messages: in}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(be.msgs) != len(in) {
		t.Fatalf("backend got %d messages, want %d: %+v", len(be.msgs), len(in), be.msgs)
	}
	for i := range in {
		if !proto.Equal(in[i], be.msgs[i]) {
			t.Errorf("message %d = %+v, want %+v", i, be.msgs[i], in[i])
		}
	}
	if be.msgs[1].GetToolCalls()[0].GetId() != "call_9" || be.msgs[2].GetToolCallId() != "call_9" {
		t.Errorf("tool call id not preserved: %+v", be.msgs)
	}
}

func TestComplete_ToolCallsReturnedNotExecuted(t *testing.T) {
	s, be := newCompleteTestServer(t)
	resp, err := s.Complete(testScope.IncomingContext(context.Background()), &llmv1.CompleteRequest{
		ProviderId: "claude",
		Messages:   userMsgs(),
		Tools:      []*llmv1.ToolDefinition{{Name: "lookup"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.GetReply().GetToolCalls()) != 1 {
		t.Fatalf("reply = %+v, want one tool call", resp.GetReply())
	}
	// Exactly one backend call means no follow-up turn carrying a tool result.
	if be.calls != 1 {
		t.Errorf("backend called %d times, want 1", be.calls)
	}
	for _, m := range be.msgs {
		if m.GetRole() == "tool" {
			t.Errorf("backend saw a tool result message: %+v", m)
		}
	}
}

func TestComplete_DisabledModelIsRejected(t *testing.T) {
	s, be, db := newCompleteTestServerDB(t)
	at := time.Now().UTC()
	seedCatalog(t, db, "claude", at, store.ProviderModel{ModelKey: "claude-x", Label: "X"}, store.ProviderModel{ModelKey: "claude-off", Label: "Off"})
	// Disable claude-off by dropping it from the provider's catalog.
	seedCatalog(t, db, "claude", at, store.ProviderModel{ModelKey: "claude-x", Label: "X"})
	_, err := s.Complete(testScope.IncomingContext(context.Background()), &llmv1.CompleteRequest{
		ProviderId: "claude",
		Messages:   userMsgs(),
		Options:    &llmv1.CallOptions{Model: "claude-off"},
	})
	if c := status.Code(err); c != codes.InvalidArgument && c != codes.NotFound {
		t.Fatalf("code = %v (%v), want InvalidArgument or NotFound", c, err)
	}
	if be.calls != 0 {
		t.Errorf("backend called %d times, want 0", be.calls)
	}
}

func TestComplete_WritesNoSessionRows(t *testing.T) {
	s, _, db := newCompleteTestServerDB(t)
	ctx := testScope.IncomingContext(context.Background())
	if _, err := s.Complete(ctx, &llmv1.CompleteRequest{ProviderId: "claude", Messages: userMsgs()}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	assertNoSessionRows(t, db)
}

// completeBackend is a fake backend that records what Complete/CompleteStream
// hand it and replies with fixed content.
type completeBackend struct {
	stubBackend
	mu    sync.Mutex
	calls int
	msgs  []*llmv1.Message
	opt   *llmv1.CallOptions
	tools []*llmv1.ToolDefinition
}

func (b *completeBackend) Chat(_ context.Context, msgs []*llmv1.Message, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	b.msgs, b.opt, b.tools = msgs, opt, tools
	if b.chatErr != nil {
		return nil, b.chatErr
	}
	return &llmv1.AssistantReply{
		Id:           "asst_complete",
		Content:      "pong",
		FinishReason: "end_turn",
		ToolCalls:    []*llmv1.ToolCall{{Id: "call_1", Name: "lookup", ArgumentsJson: `{"q":"x"}`}},
	}, nil
}

func (b *completeBackend) ChatStream(_ context.Context, msgs []*llmv1.Message, opt *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.mu.Lock()
	b.calls++
	b.msgs, b.opt = msgs, opt
	b.mu.Unlock()
	frames := []*llmv1.ConversationStreamFrame{
		{Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: "t", Delta: "po"}}},
		{Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: "t", Delta: "ng"}}},
		{Frame: &llmv1.ConversationStreamFrame_ToolCallStreamFinished{ToolCallStreamFinished: &llmv1.EvToolCallStreamFinished{Id: "call_1", Name: "lookup", ArgumentsJson: `{}`}}},
		{Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{TurnId: "t", FinishReason: strPtr("end_turn"), Usage: &llmv1.TokenUsage{InputTokens: int64Ptr(11), OutputTokens: int64Ptr(7)}}}},
	}
	for _, f := range frames {
		f.SchemaVersion = conversationevents.SchemaVersion
		if err := emit(f); err != nil {
			return err
		}
	}
	return nil
}

// newCompleteTestServer registers provider "claude" with enabled model
// "claude-x" and swaps its backend for a fake.
func newCompleteTestServer(t *testing.T) (*LlmServer, *completeBackend) {
	t.Helper()
	s, be, _ := newCompleteTestServerDB(t)
	return s, be
}

func newCompleteTestServerDB(t *testing.T) (*LlmServer, *completeBackend, *gorm.DB) {
	t.Helper()
	s, db := newTestLlmServerWithModelCache(t)
	registerTestAnthropic(t, s, "claude")
	seedCatalog(t, db, "claude", time.Now().UTC(), store.ProviderModel{ModelKey: "claude-x", Label: "X"})
	e, ok := s.Providers.Get(context.Background(), testScope, "claude")
	if !ok || e == nil {
		t.Fatal("provider claude not resolvable")
	}
	be := &completeBackend{}
	e.Backend = be
	return s, be, db
}

func userMsgs() []*llmv1.Message {
	return []*llmv1.Message{{Role: "user", Content: "ping", Id: "u1"}}
}

func TestComplete_ReturnsReplyAndForwardsInputs(t *testing.T) {
	s, be := newCompleteTestServer(t)
	ctx := testScope.IncomingContext(context.Background())
	resp, err := s.Complete(ctx, &llmv1.CompleteRequest{
		ProviderId: "claude",
		System:     strPtr("be brief"),
		Messages:   userMsgs(),
		Tools:      []*llmv1.ToolDefinition{{Name: "lookup"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	r := resp.GetReply()
	if r.GetContent() != "pong" || len(r.GetToolCalls()) != 1 || r.GetToolCalls()[0].GetName() != "lookup" {
		t.Fatalf("reply = %+v", r)
	}
	if len(be.msgs) != 2 || be.msgs[0].GetRole() != "system" || be.msgs[0].GetContent() != "be brief" || be.msgs[1].GetContent() != "ping" {
		t.Fatalf("messages = %+v, want system prompt then user message", be.msgs)
	}
	if be.opt.GetModel() != "claude-x" {
		t.Errorf("model = %q, want provider default claude-x", be.opt.GetModel())
	}
	if len(be.tools) != 1 || be.tools[0].GetName() != "lookup" {
		t.Errorf("tools = %+v, want lookup forwarded", be.tools)
	}
}

func TestComplete_EmptyMessagesIsInvalidArgument(t *testing.T) {
	s, be := newCompleteTestServer(t)
	_, err := s.Complete(testScope.IncomingContext(context.Background()), &llmv1.CompleteRequest{ProviderId: "claude"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v (%v), want InvalidArgument", status.Code(err), err)
	}
	if be.calls != 0 {
		t.Errorf("backend called %d times, want 0", be.calls)
	}
}

func TestComplete_UnknownProviderIsNotFound(t *testing.T) {
	s, _ := newCompleteTestServer(t)
	_, err := s.Complete(testScope.IncomingContext(context.Background()), &llmv1.CompleteRequest{ProviderId: "nope", Messages: userMsgs()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v (%v), want NotFound", status.Code(err), err)
	}
}

func TestComplete_ModelNotEnabledIsNotFound(t *testing.T) {
	s, be := newCompleteTestServer(t)
	_, err := s.Complete(testScope.IncomingContext(context.Background()), &llmv1.CompleteRequest{
		ProviderId: "claude",
		Messages:   userMsgs(),
		Options:    &llmv1.CallOptions{Model: "not-enabled"},
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v (%v), want NotFound", status.Code(err), err)
	}
	if be.calls != 0 {
		t.Errorf("backend called %d times, want 0", be.calls)
	}
}

func TestComplete_MissingTenantIsUnauthenticated(t *testing.T) {
	s, be := newCompleteTestServer(t)
	_, err := s.Complete(context.Background(), &llmv1.CompleteRequest{ProviderId: "claude", Messages: userMsgs()})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v (%v), want Unauthenticated", status.Code(err), err)
	}
	if be.calls != 0 {
		t.Errorf("backend called %d times, want 0", be.calls)
	}
}

func TestCompleteStream_ForwardsDeltasToolCallAndFinalReply(t *testing.T) {
	s, be, db := newCompleteTestServerDB(t)
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

	stream, err := llmv1.NewLlmServiceClient(conn).CompleteStream(ctx, &llmv1.CompleteRequest{
		ProviderId: "claude",
		Messages:   userMsgs(),
	})
	if err != nil {
		t.Fatal(err)
	}

	var deltas string
	var calls []*llmv1.ToolCall
	var final *llmv1.AssistantReply
	for {
		f, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("CompleteStream: %v", err)
		}
		if final != nil {
			t.Fatalf("frame %+v received after final reply", f)
		}
		switch {
		case f.GetContentDelta() != "":
			deltas += f.GetContentDelta()
		case f.GetToolCall() != nil:
			calls = append(calls, f.GetToolCall())
		case f.GetFinalReply() != nil:
			final = f.GetFinalReply()
		}
	}
	if deltas != "pong" {
		t.Errorf("content deltas = %q, want pong", deltas)
	}
	if len(calls) != 1 || calls[0].GetName() != "lookup" {
		t.Errorf("tool calls = %+v, want one lookup", calls)
	}
	if final == nil {
		t.Fatal("no final reply frame")
	}
	if final.GetContent() != "pong" || final.GetFinishReason() != "end_turn" || len(final.GetToolCalls()) != 1 {
		t.Errorf("final reply = %+v", final)
	}
	if final.GetUsage().GetInputTokens() != 11 || final.GetUsage().GetOutputTokens() != 7 {
		t.Errorf("final usage = %+v, want input 11 output 7", final.GetUsage())
	}
	if be.opt.GetModel() != "claude-x" {
		t.Errorf("model = %q, want claude-x", be.opt.GetModel())
	}
	assertNoSessionRows(t, db)
}
