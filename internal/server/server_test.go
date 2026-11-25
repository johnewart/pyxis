package server

import (
	"context"
	"fmt"
	"github.com/johnewart/pyxis/internal/serverutil"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/assistant/history"
	"github.com/johnewart/pyxis/internal/assistant/session"
	claudecodeclillm "github.com/johnewart/pyxis/internal/llm/claudecode"
	"github.com/johnewart/pyxis/internal/llm/common"
	cursorclillm "github.com/johnewart/pyxis/internal/llm/cursorcli"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

const bufSize = 1024 * 1024

// testSessionID is a stable id for tests that require a managed session runtime (chat, tools, streams).
const testSessionID = "test_session"

// testScope is the shared tenant scope tests attach to handler contexts
// (testScope.OutgoingContext / testScope.IncomingContext).
var testScope = serverutil.Scope{TenantID: "test_tenant"}

func sessionManagerWithSQLite(t *testing.T, hub session.HubOptions) (*sessionmanager.Manager, *gorm.DB) {
	t.Helper()
	db := store.NewTestSQLite(t)
	sessionRepo := store.NewSessionRepo(db)
	mgr, err := sessionmanager.New(hub, sessionmanager.Options{
		SessionPersist: sessionRepo,
		NewSessionHistory: func(sc serverutil.Scope, agentID string) history.Store {
			return store.NewHistory(db, sc, agentID)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return mgr, db
}

func testAgentManager(t *testing.T) *sessionmanager.Manager {
	t.Helper()
	mgr, _ := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(context.Background(), &llmv1.EnsureSessionRequest{SessionId: testSessionID}, testScope); err != nil {
		t.Fatal(err)
	}
	return mgr
}

func testAgentManagerWithCreate(t *testing.T, req *llmv1.EnsureSessionRequest) *sessionmanager.Manager {
	t.Helper()
	if req == nil || strings.TrimSpace(req.GetSessionId()) == "" {
		t.Fatal("EnsureSessionRequest.session_id is required")
	}
	mgr, _ := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(context.Background(), req, testScope); err != nil {
		t.Fatal(err)
	}
	return mgr
}

type stubBackend struct {
	chatReply     *llmv1.AssistantReply
	chatErr       error
	generateReply *llmv1.AssistantReply
	generateErr   error
	chatStreamErr error
	// When non-empty, ChatStream emits an assistant_activity_snapshot after assistant_turn_started.
	stubActivityHeadline string
}

func (s *stubBackend) Chat(context.Context, []*llmv1.Message, *llmv1.CallOptions, []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	return s.chatReply, s.chatErr
}

func (s *stubBackend) Generate(context.Context, string, string, *llmv1.CallOptions, []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	if s.generateReply != nil || s.generateErr != nil {
		return s.generateReply, s.generateErr
	}
	return &llmv1.AssistantReply{Content: "generated"}, nil
}

func (s *stubBackend) ChatStream(_ context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	if s.chatStreamErr != nil {
		return s.chatStreamErr
	}
	if emit == nil {
		return nil
	}
	turnID := "turn_stub"
	reply := s.chatReply
	if reply == nil {
		reply = &llmv1.AssistantReply{Content: "hello", Id: "asst_1", FinishReason: "end_turn"}
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID},
		},
	}); err != nil {
		return err
	}
	if s.stubActivityHeadline != "" {
		if err := emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_AssistantActivitySnapshot{
				AssistantActivitySnapshot: &llmv1.EvAssistantActivitySnapshot{
					TurnId:   turnID,
					Headline: s.stubActivityHeadline,
				},
			},
		}); err != nil {
			return err
		}
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
			AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: reply.GetContent()},
		},
	}); err != nil {
		return err
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{
			AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{TurnId: turnID},
		},
	}); err != nil {
		return err
	}
	return emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_StreamComplete{
			StreamComplete: reply,
		},
	})
}

// captureStreamBackend records each ChatStream message slice before delegating to stubBackend.
type captureStreamBackend struct {
	stubBackend
	saw [][]*llmv1.Message
}

func (b *captureStreamBackend) ChatStream(ctx context.Context, msgs []*llmv1.Message, opt *llmv1.CallOptions, tf func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	cp := make([]*llmv1.Message, len(msgs))
	copy(cp, msgs)
	b.saw = append(b.saw, cp)
	fireCaptureStatefulSessionSink(ctx)
	return b.stubBackend.ChatStream(ctx, msgs, opt, tf, emit)
}

func fireCaptureStatefulSessionSink(ctx context.Context) {
	const testSession = "capture-test-session"
	if fn := cursorclillm.BackendSessionSinkFromContext(ctx); fn != nil {
		fn(testSession)
	}
	if fn := claudecodeclillm.BackendSessionSinkFromContext(ctx); fn != nil {
		fn(testSession)
	}
}

func TestLlmServer_ChatStream_grpcRoundTrip(t *testing.T) {
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	mgr := testAgentManagerWithCreate(t, &llmv1.EnsureSessionRequest{SessionId: "sess1"})
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		LLM:             &stubBackend{},
		Tools:           toolregistry.New(),
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

	client := llmv1.NewLlmServiceClient(conn)
	stream, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "hi", Id: "u1"}},
		SessionId:      strPtr("sess1"),
		PersistSession: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var frames []*llmv1.ConversationStreamFrame
	for {
		f, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, f)
	}
	if len(frames) < 4 {
		t.Fatalf("expected at least 4 frames, got %d", len(frames))
	}
	ma, ok := mgr.Get(testScope, "sess1")
	if !ok {
		t.Fatal("missing test agent")
	}
	hist := ma.Core.History().History("sess1", 0)
	if len(hist) != 2 {
		t.Fatalf("persisted history len=%d want 2", len(hist))
	}
	if hist[0].GetRole() != "user" || hist[1].GetRole() != "assistant" {
		t.Fatalf("unexpected roles: %+v / %+v", hist[0], hist[1])
	}
	snap := ma.Core.Session("sess1").Snapshot()
	if snap.State.ActiveAssistantTurn.Status != conversationevents.TurnCompleted {
		t.Fatalf("hub fold status=%q", snap.State.ActiveAssistantTurn.Status)
	}
}

func TestLlmServer_ChatStream_activity_snapshot_roundTrip(t *testing.T) {
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	mgr := testAgentManagerWithCreate(t, &llmv1.EnsureSessionRequest{SessionId: "sess_activity"})
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		LLM:             &stubBackend{stubActivityHeadline: "Planning next steps"},
		Tools:           toolregistry.New(),
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

	client := llmv1.NewLlmServiceClient(conn)
	stream, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "hi", Id: "u1"}},
		SessionId:      strPtr("sess_activity"),
		PersistSession: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var frames []*llmv1.ConversationStreamFrame
	snapIdx := -1
	turnStartedIdx := -1
	for {
		f, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, f)
		if f.GetAssistantTurnStarted() != nil && turnStartedIdx < 0 {
			turnStartedIdx = len(frames) - 1
		}
		if f.GetAssistantActivitySnapshot() != nil {
			if snapIdx >= 0 {
				t.Fatalf("unexpected duplicate activity snapshot")
			}
			snapIdx = len(frames) - 1
			ev := f.GetAssistantActivitySnapshot()
			if ev.GetHeadline() != "Planning next steps" || ev.GetTurnId() != "turn_stub" {
				t.Fatalf("unexpected snapshot: %+v", ev)
			}
		}
	}
	if snapIdx < 0 {
		t.Fatal("expected assistant_activity_snapshot frame")
	}
	if turnStartedIdx < 0 || snapIdx <= turnStartedIdx {
		t.Fatalf("snapshot should follow assistant_turn_started; turn=%d snap=%d frames=%d", turnStartedIdx, snapIdx, len(frames))
	}
	ma, ok := mgr.Get(testScope, "sess_activity")
	if !ok {
		t.Fatal("missing test agent")
	}
	sessSnap := ma.Core.Session("sess_activity").Snapshot()
	if sessSnap.State.ActiveAssistantTurn.ActivityHeadline != "Planning next steps" {
		t.Fatalf("hub fold headline=%q", sessSnap.State.ActiveAssistantTurn.ActivityHeadline)
	}
}

func TestLlmServer_ChatStream_prepends_persisted_history(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	drain := func(t *testing.T, st llmv1.LlmService_ChatStreamClient) {
		t.Helper()
		for {
			_, err := st.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("non_stateful_provider_prepends_persisted_history", func(t *testing.T) {
		openCap := &captureStreamBackend{}
		reg, err := providers.NewRegistryFromEntries("openai_stub", map[string]*providers.Entry{
			"openai_stub": {
				ID:           "openai_stub",
				Kind:         providerkind.OpenAI,
				Backend:      openCap,
				DefaultModel: "gpt-4o-mini",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		comp := providers.NewComposite(reg, nil)
		mgr := testAgentManagerWithCreate(t, &llmv1.EnsureSessionRequest{SessionId: "sess_hist"})
		ma, ok := mgr.Get(testScope, "sess_hist")
		if !ok {
			t.Fatal("missing agent")
		}
		ma.Core.History().Store("sess_hist", &llmv1.Message{Role: "user", Content: "prior", Id: "prior1"})

		lis := bufconn.Listen(bufSize)
		srv := grpc.NewServer()
		llmv1.RegisterLlmServiceServer(srv, &LlmServer{
			Providers:       comp,
			Tools:           toolregistry.New(),
			SessionRuntimes: mgr,
		})
		go func() { _ = srv.Serve(lis) }()
		t.Cleanup(func() { srv.Stop() })

		conn, err := grpc.DialContext(ctx, "bufnet",
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })

		client := llmv1.NewLlmServiceClient(conn)
		st, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
			Messages:       []*llmv1.Message{{Role: "user", Content: "new", Id: "u_new"}},
			SessionId:      strPtr("sess_hist"),
			PersistSession: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		drain(t, st)
		if len(openCap.saw) != 1 || len(openCap.saw[0]) != 2 {
			t.Fatalf("want [prior,new] got len(saw)=%d first=%d", len(openCap.saw), len(openCap.saw[0]))
		}
		if openCap.saw[0][0].GetContent() != "prior" || openCap.saw[0][1].GetContent() != "new" {
			t.Fatalf("messages: %+v / %+v", openCap.saw[0][0], openCap.saw[0][1])
		}
	})

	t.Run("cursor_provider_prepends_persisted_history", func(t *testing.T) {
		curCap := &captureStreamBackend{}
		reg, err := providers.NewRegistryFromEntries("cursor_stub", map[string]*providers.Entry{
			"cursor_stub": {
				ID:           "cursor_stub",
				Kind:         providerkind.CursorCLI,
				Backend:      curCap,
				DefaultModel: "auto",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		comp := providers.NewComposite(reg, nil)
		mgr := testAgentManagerWithCreate(t, &llmv1.EnsureSessionRequest{
			SessionId:     "sess_cur",
			LlmProviderId: strPtr("cursor_stub"),
		})
		ma, ok := mgr.Get(testScope, "sess_cur")
		if !ok {
			t.Fatal("missing agent")
		}
		ma.Core.History().Store("sess_cur", &llmv1.Message{Role: "user", Content: "prior", Id: "prior1"})

		lis := bufconn.Listen(bufSize)
		srv := grpc.NewServer()
		llmv1.RegisterLlmServiceServer(srv, &LlmServer{
			Providers:       comp,
			Tools:           toolregistry.New(),
			SessionRuntimes: mgr,
		})
		go func() { _ = srv.Serve(lis) }()
		t.Cleanup(func() { srv.Stop() })

		conn, err := grpc.DialContext(ctx, "bufnet",
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })

		client := llmv1.NewLlmServiceClient(conn)
		st, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
			Messages:       []*llmv1.Message{{Role: "user", Content: "new2", Id: "u_new2"}},
			SessionId:      strPtr("sess_cur"),
			PersistSession: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		drain(t, st)
		if len(curCap.saw) != 1 || len(curCap.saw[0]) != 2 {
			t.Fatalf("want [prior,new2] got len(saw)=%d first=%d", len(curCap.saw), len(curCap.saw[0]))
		}
		if curCap.saw[0][0].GetContent() != "prior" || curCap.saw[0][1].GetContent() != "new2" {
			t.Fatalf("messages: %+v / %+v", curCap.saw[0][0], curCap.saw[0][1])
		}
	})
}

func TestLlmServer_ChatStream_cursor_stateful_incremental(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	drain := func(t *testing.T, st llmv1.LlmService_ChatStreamClient) {
		t.Helper()
		for {
			_, err := st.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	curCap := &captureStreamBackend{}
	reg, err := providers.NewRegistryFromEntries("cursor_stub", map[string]*providers.Entry{
		"cursor_stub": {
			ID:           "cursor_stub",
			Kind:         providerkind.CursorCLI,
			Backend:      curCap,
			DefaultModel: "auto",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	comp := providers.NewComposite(reg, nil)
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	syncRepo := store.NewProviderSessionSyncRepo(db)
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     "sess_sync",
		LlmProviderId: strPtr("cursor_stub"),
	}, testScope); err != nil {
		t.Fatal(err)
	}
	ma, ok := mgr.Get(testScope, "sess_sync")
	if !ok {
		t.Fatal("missing agent")
	}
	ma.Core.History().Store("sess_sync", &llmv1.Message{Role: "user", Content: "prior", Id: "prior1"})

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		Providers:           comp,
		Tools:               toolregistry.New(),
		SessionRuntimes:     mgr,
		ProviderSessionSync: syncRepo,
	})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := llmv1.NewLlmServiceClient(conn)

	st1, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "round1", Id: "u1"}},
		SessionId:      strPtr("sess_sync"),
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, st1)
	if len(curCap.saw) != 1 || len(curCap.saw[0]) != 2 {
		t.Fatalf("first rpc want 2 backend msgs [prior,round1] got saw=%d first=%d", len(curCap.saw), len(curCap.saw[0]))
	}

	st2, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "round2", Id: "u2"}},
		SessionId:      strPtr("sess_sync"),
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, st2)
	if len(curCap.saw) != 2 {
		t.Fatalf("want 2 backend invocations got %d", len(curCap.saw))
	}
	if len(curCap.saw[1]) != 1 || curCap.saw[1][0].GetContent() != "round2" {
		t.Fatalf("incremental want [round2] got %+v", curCap.saw[1])
	}
}

func TestLlmServer_ChatStream_claude_v2_stateful_incremental(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	drain := func(t *testing.T, st llmv1.LlmService_ChatStreamClient) {
		t.Helper()
		for {
			_, err := st.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	claudeCap := &captureStreamBackend{}
	reg, err := providers.NewRegistryFromEntries("claude_v2_stub", map[string]*providers.Entry{
		"claude_v2_stub": {
			ID:           "claude_v2_stub",
			Kind:         providerkind.ClaudeCodeV2,
			Backend:      claudeCap,
			DefaultModel: "sonnet",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	comp := providers.NewComposite(reg, nil)
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	syncRepo := store.NewProviderSessionSyncRepo(db)
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     "sess_claude_v2",
		LlmProviderId: strPtr("claude_v2_stub"),
	}, testScope); err != nil {
		t.Fatal(err)
	}
	ma, ok := mgr.Get(testScope, "sess_claude_v2")
	if !ok {
		t.Fatal("missing agent")
	}
	ma.Core.History().Store("sess_claude_v2", &llmv1.Message{Role: "user", Content: "prior", Id: "prior1"})

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		Providers:           comp,
		Tools:               toolregistry.New(),
		SessionRuntimes:     mgr,
		ProviderSessionSync: syncRepo,
	})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := llmv1.NewLlmServiceClient(conn)

	st1, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "round1", Id: "u1"}},
		SessionId:      strPtr("sess_claude_v2"),
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, st1)
	if len(claudeCap.saw) != 1 || len(claudeCap.saw[0]) != 2 {
		t.Fatalf("first rpc want 2 backend msgs [prior,round1] got saw=%d first=%d", len(claudeCap.saw), len(claudeCap.saw[0]))
	}

	st2, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "round2", Id: "u2"}},
		SessionId:      strPtr("sess_claude_v2"),
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, st2)
	if len(claudeCap.saw) != 2 {
		t.Fatalf("want 2 backend invocations got %d", len(claudeCap.saw))
	}
	if len(claudeCap.saw[1]) != 1 || claudeCap.saw[1][0].GetContent() != "round2" {
		t.Fatalf("incremental want [round2] got %+v", claudeCap.saw[1])
	}
}

// cancelAfterPartialBackend streams an assistant turn-start plus one partial text
// delta, signals via emitted, then blocks until the request context is cancelled
// and returns ctx.Err() — emulating a CLI subprocess killed mid-stream on a user
// abort. It also fires the stateful session sink so the backend session id is
// captured, letting the test assert the resume pointer is retained (not cleared).
type cancelAfterPartialBackend struct {
	stubBackend
	turnID  string
	content string
	emitted chan struct{}
}

func (b *cancelAfterPartialBackend) ChatStream(ctx context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	fireCaptureStatefulSessionSink(ctx)
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: b.turnID},
		},
	}); err != nil {
		return err
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
			AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: b.turnID, Delta: b.content},
		},
	}); err != nil {
		return err
	}
	close(b.emitted)
	<-ctx.Done()
	return ctx.Err()
}

// TestLlmServer_ChatStream_cancel_commits_partial_turn verifies that a mid-stream
// user cancel is finalized as a normal (short) turn rather than discarded: the
// user message and the streamed-so-far assistant partial are persisted to
// history, and the provider-sync watermark advances to the committed length while
// the resume pointer is retained — so the next turn resumes cheaply instead of
// forcing a full resync.
func TestLlmServer_ChatStream_cancel_commits_partial_turn(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())

	be := &cancelAfterPartialBackend{turnID: "turn_cancel", content: "partial answer", emitted: make(chan struct{})}
	reg, err := providers.NewRegistryFromEntries("claude_v2_stub", map[string]*providers.Entry{
		"claude_v2_stub": {
			ID:           "claude_v2_stub",
			Kind:         providerkind.ClaudeCodeV2,
			Backend:      be,
			DefaultModel: "sonnet",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	comp := providers.NewComposite(reg, nil)
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	syncRepo := store.NewProviderSessionSyncRepo(db)
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     "sess_cancel",
		LlmProviderId: strPtr("claude_v2_stub"),
	}, testScope); err != nil {
		t.Fatal(err)
	}
	ma, ok := mgr.Get(testScope, "sess_cancel")
	if !ok {
		t.Fatal("missing agent")
	}

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		Providers:           comp,
		Tools:               toolregistry.New(),
		SessionRuntimes:     mgr,
		ProviderSessionSync: syncRepo,
	})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := llmv1.NewLlmServiceClient(conn)

	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	st, err := client.ChatStream(callCtx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "the question", Id: "u1"}},
		SessionId:      strPtr("sess_cancel"),
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Explicit Stop once the backend has streamed its partial. (Cancelling the
	// RPC ctx no longer cuts a turn — client disconnect is not a Stop.)
	go func() {
		<-be.emitted
		if _, cerr := client.CancelTurn(ctx, &llmv1.CancelTurnRequest{SessionId: "sess_cancel"}); cerr != nil {
			t.Errorf("CancelTurn: %v", cerr)
		}
	}()
	for {
		if _, rerr := st.Recv(); rerr != nil {
			break
		}
	}

	// The server finalizes the cancelled turn asynchronously; poll until it lands.
	deadline := time.Now().Add(3 * time.Second)
	var hist []*llmv1.Message
	for time.Now().Before(deadline) {
		hist = ma.Core.History().History("sess_cancel", 0)
		if len(hist) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(hist) < 2 {
		t.Fatalf("cancel commit: want user+partial-assistant persisted, got %d messages: %+v", len(hist), hist)
	}
	if hist[0].GetRole() != "user" || hist[0].GetContent() != "the question" {
		t.Fatalf("cancel commit: first message should be the user turn, got role=%q content=%q", hist[0].GetRole(), hist[0].GetContent())
	}
	last := hist[len(hist)-1]
	if last.GetRole() != "assistant" || last.GetContent() != "partial answer" {
		t.Fatalf("cancel commit: last message should be the partial assistant reply, got role=%q content=%q", last.GetRole(), last.GetContent())
	}

	// Watermark advances to the committed transcript length; resume pointer retained.
	var row *store.ProviderSessionSync
	for time.Now().Before(deadline) {
		row, _ = syncRepo.Get(context.Background(), testScope, "sess_cancel", "sess_cancel", "claude_v2_stub")
		if row != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if row == nil {
		t.Fatal("cancel commit: expected a provider session sync row after cancel")
	}
	if row.SyncedMessageCount != len(hist) {
		t.Fatalf("cancel commit: watermark should equal committed history length %d, got %d", len(hist), row.SyncedMessageCount)
	}
	if row.BackendSessionID != "capture-test-session" {
		t.Fatalf("cancel commit: resume pointer should be retained, got %q", row.BackendSessionID)
	}
}

func TestLlmServer_ChatStream_persist_thinkingContent(t *testing.T) {
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	mgr := testAgentManagerWithCreate(t, &llmv1.EnsureSessionRequest{SessionId: "sess_th"})
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		LLM: &stubBackend{chatReply: &llmv1.AssistantReply{
			Content:         "visible",
			ThinkingContent: "chain",
			Id:              "asst_th",
			FinishReason:    "end_turn",
		}},
		Tools:           toolregistry.New(),
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

	client := llmv1.NewLlmServiceClient(conn)
	stream, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "hi", Id: "u1"}},
		SessionId:      strPtr("sess_th"),
		PersistSession: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	ma, ok := mgr.Get(testScope, "sess_th")
	if !ok {
		t.Fatal("missing test agent")
	}
	hist := ma.Core.History().History("sess_th", 0)
	if len(hist) != 2 {
		t.Fatalf("persisted history len=%d want 2", len(hist))
	}
	asst := hist[1]
	if asst.GetRole() != "assistant" || asst.GetContent() != "visible" {
		t.Fatalf("assistant=%+v", asst)
	}
	if asst.GetThinkingContent() != "chain" {
		t.Fatalf("thinking_content=%q want chain", asst.GetThinkingContent())
	}
}

func TestLlmServer_ChatStream_wire_stripsRedundantStreamCompleteBodies(t *testing.T) {
	t.Parallel()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	mgr := testAgentManagerWithCreate(t, &llmv1.EnsureSessionRequest{SessionId: "sess_wire_strip"})
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		LLM: &stubBackend{chatReply: &llmv1.AssistantReply{
			Content:         "visible",
			ThinkingContent: "chain",
			Id:              "asst_wire",
			FinishReason:    "end_turn",
		}},
		Tools:           toolregistry.New(),
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

	client := llmv1.NewLlmServiceClient(conn)
	stream, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "hi", Id: "u1"}},
		SessionId:      strPtr("sess_wire_strip"),
		PersistSession: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var (
		assistantText  strings.Builder
		assistantThink strings.Builder
		lastComplete   *llmv1.AssistantReply
	)
	for {
		f, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if td := f.GetAssistantTextDelta(); td != nil {
			assistantText.WriteString(td.GetDelta())
		}
		if td := f.GetAssistantThinkingDelta(); td != nil {
			assistantThink.WriteString(td.GetDelta())
		}
		if sc := f.GetStreamComplete(); sc != nil {
			lastComplete = sc
		}
	}

	if lastComplete == nil {
		t.Fatal("missing stream_complete")
	}
	if lastComplete.GetContent() != "" || lastComplete.GetThinkingContent() != "" {
		t.Fatalf("expected redundant stream_complete bodies stripped on wire, got content=%q thinking=%q",
			lastComplete.GetContent(), lastComplete.GetThinkingContent())
	}
	if assistantText.String() != "visible" {
		t.Fatalf("unexpected assistant text deltas: %q", assistantText.String())
	}
	if assistantThink.String() != "chain" {
		t.Fatalf("unexpected assistant thinking deltas: %q", assistantThink.String())
	}

	ma, ok := mgr.Get(testScope, "sess_wire_strip")
	if !ok {
		t.Fatal("missing test agent")
	}
	hist := ma.Core.History().History("sess_wire_strip", 0)
	if len(hist) != 2 {
		t.Fatalf("persisted history len=%d want 2", len(hist))
	}
	asst := hist[1]
	if asst.GetContent() != "visible" || asst.GetThinkingContent() != "chain" {
		t.Fatalf("expected persisted canonical assistant body, got %#v", asst)
	}
}

func TestLlmServer_ChatStream_persist_activityHeadline(t *testing.T) {
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	mgr := testAgentManagerWithCreate(t, &llmv1.EnsureSessionRequest{SessionId: "sess_ah"})
	llmv1.RegisterLlmServiceServer(srv, &LlmServer{
		LLM: &stubBackend{
			stubActivityHeadline: "Planning next steps",
			chatReply: &llmv1.AssistantReply{
				Content:      "visible",
				Id:           "asst_act",
				FinishReason: "end_turn",
			},
		},
		Tools:           toolregistry.New(),
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

	client := llmv1.NewLlmServiceClient(conn)
	stream, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: "hi", Id: "u1"}},
		SessionId:      strPtr("sess_ah"),
		PersistSession: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	ma, ok := mgr.Get(testScope, "sess_ah")
	if !ok {
		t.Fatal("missing test agent")
	}
	hist := ma.Core.History().History("sess_ah", 0)
	if len(hist) != 2 {
		t.Fatalf("persisted history len=%d want 2", len(hist))
	}
	asst := hist[1]
	if asst.GetRole() != "assistant" || asst.GetContent() != "visible" {
		t.Fatalf("assistant=%+v", asst)
	}
	if asst.GetActivityHeadline() != "Planning next steps" {
		t.Fatalf("activity_headline=%q", asst.GetActivityHeadline())
	}
}

func strPtr(s string) *string { return &s }

func toolDefinitionsContainName(defs []*llmv1.ToolDefinition, name string) bool {
	for _, d := range defs {
		if d != nil && strings.TrimSpace(d.GetName()) == name {
			return true
		}
	}
	return false
}

// waitForToolBackend blocks in ChatStream until tools() includes toolName, then emits a
// streaming turn that requests that tool (mirrors provider tool-call streaming).
type waitForToolBackend struct {
	toolName string
}

func (b *waitForToolBackend) Chat(context.Context, []*llmv1.Message, *llmv1.CallOptions, []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	return nil, fmt.Errorf("chat not implemented")
}

func (b *waitForToolBackend) Generate(context.Context, string, string, *llmv1.CallOptions, []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	return nil, fmt.Errorf("generate not implemented")
}

func (b *waitForToolBackend) ChatStream(ctx context.Context, msgs []*llmv1.Message, _ *llmv1.CallOptions, tools func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	if emit == nil {
		return fmt.Errorf("emit callback is required")
	}
	for _, m := range msgs {
		if m != nil && m.GetRole() == "tool" {
			turnID, err := common.RandomTurnID()
			if err != nil {
				return err
			}
			reply := &llmv1.AssistantReply{Content: "after_tool", Id: "asst_followup", FinishReason: "end_turn"}
			if err := emit(&llmv1.ConversationStreamFrame{
				SchemaVersion: conversationevents.SchemaVersion,
				Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
					AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID},
				},
			}); err != nil {
				return err
			}
			if err := emit(&llmv1.ConversationStreamFrame{
				SchemaVersion: conversationevents.SchemaVersion,
				Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
					AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: reply.GetContent()},
				},
			}); err != nil {
				return err
			}
			if err := emit(&llmv1.ConversationStreamFrame{
				SchemaVersion: conversationevents.SchemaVersion,
				Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{
					AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{TurnId: turnID},
				},
			}); err != nil {
				return err
			}
			return emit(&llmv1.ConversationStreamFrame{
				SchemaVersion: conversationevents.SchemaVersion,
				Frame: &llmv1.ConversationStreamFrame_StreamComplete{
					StreamComplete: reply,
				},
			})
		}
	}
	for {
		var snap []*llmv1.ToolDefinition
		if tools != nil {
			snap = tools()
		}
		if toolDefinitionsContainName(snap, b.toolName) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
	}
	turnID, err := common.RandomTurnID()
	if err != nil {
		return err
	}
	tcID := "toolcall_concurrent_1"
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{
				TurnId:             turnID,
				ThinkingEnabled:    proto.Bool(false),
				StreamingToolCalls: proto.Bool(true),
			},
		},
	}); err != nil {
		return err
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_ToolCallStreamStarted{
			ToolCallStreamStarted: &llmv1.EvToolCallStreamStarted{
				TurnId: turnID,
				Id:     tcID,
				Name:   proto.String(b.toolName),
			},
		},
	}); err != nil {
		return err
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_ToolCallStreamFinished{
			ToolCallStreamFinished: &llmv1.EvToolCallStreamFinished{
				TurnId:        turnID,
				Id:            tcID,
				Name:          b.toolName,
				ArgumentsJson: `{}`,
			},
		},
	}); err != nil {
		return err
	}
	reply := &llmv1.AssistantReply{
		Content:      "",
		Id:           "asst_stream_1",
		FinishReason: "tool_use",
		ToolCalls: []*llmv1.ToolCall{
			{Id: tcID, Name: b.toolName, ArgumentsJson: `{}`},
		},
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{
			AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{TurnId: turnID},
		},
	}); err != nil {
		return err
	}
	return emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_StreamComplete{
			StreamComplete: reply,
		},
	})
}
