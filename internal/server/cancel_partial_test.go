package server

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/llm/backend"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// Guarantees a cut-short turn owes the transcript, beyond "something was
// persisted" (TestLlmServer_ChatStream_cancel_commits_partial_turn in
// server_test.go covers that much): the truncation is recorded durably, the
// salvage does not depend on the raw-chunk table, a deadline is salvaged the
// same way a user abort is, a turn that produced nothing leaves no empty
// assistant bubble, and observers other than the aborting caller are told the
// turn ended.

// cancelSilentBackend announces a turn and then produces nothing before the
// request context dies: "the user hit stop before the model said a word".
type cancelSilentBackend struct {
	stubBackend
	turnID  string
	started chan struct{}
}

func (b *cancelSilentBackend) ChatStream(ctx context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	fireCaptureStatefulSessionSink(ctx)
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: b.turnID},
		},
	}); err != nil {
		return err
	}
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

// cancelEnv is a bufconn LlmServer over one backend and one session, plus the
// observation surfaces a cancelled turn has to reach.
type cancelEnv struct {
	client    llmv1.LlmServiceClient
	// impl is the server under test, for WaitForTurns (the post-commit signal
	// for a turn that ends normally, with no cut announcement to wait on).
	impl      *LlmServer
	sessionID string
	// history reads the session's persisted transcript.
	history func() []*llmv1.Message
	// hub is the live fan-out other windows on this session listen to.
	hub *session.Hub
	// events is the durable log replay is served from.
	events *store.SessionEventRepo
	// cut receives one token per announced turn-cut. Subscribed at construction,
	// so it cannot miss an abort that lands before a test starts watching.
	cut chan struct{}
}

// cancelEnvOpt tweaks the environment newCancelEnv builds.
type cancelEnvOpt func(*cancelEnvConfig)

type cancelEnvConfig struct {
	kind providerkind.Kind
}

// withProviderKind picks the provider kind the session is bound to. Defaults to
// a CLI/stateful kind (the interesting one for resume bookkeeping); tests that
// want to inspect the exact message list a turn was handed choose a stateless
// kind instead, since stateful providers are deliberately sent only the
// unsynced tail.
func withProviderKind(k providerkind.Kind) cancelEnvOpt {
	return func(c *cancelEnvConfig) { c.kind = k }
}

// newCancelEnv wires an LlmServer around be for one session. The bool is
// ignored: raw chunks no longer exist, partial replies are recovered from the
// session events and history.
func newCancelEnv(
	t *testing.T,
	ctx context.Context,
	sessionID string,
	be backend.Backend,
	_ bool,
	opts ...cancelEnvOpt,
) *cancelEnv {
	t.Helper()

	cfg := cancelEnvConfig{kind: providerkind.ClaudeCodeV2}
	for _, opt := range opts {
		opt(&cfg)
	}

	const providerID = "cancel_stub"
	reg, err := providers.NewRegistryFromEntries(providerID, map[string]*providers.Entry{
		providerID: {
			ID:           providerID,
			Kind:         cfg.kind,
			Backend:      be,
			DefaultModel: "sonnet",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     sessionID,
		LlmProviderId: strPtr(providerID),
	}, testScope); err != nil {
		t.Fatal(err)
	}
	ma, ok := mgr.Get(testScope, sessionID)
	if !ok {
		t.Fatalf("missing session runtime for %q", sessionID)
	}

	events := store.NewSessionEventRepo(db)
	impl := &LlmServer{
		Providers:           providers.NewComposite(reg, nil),
		Tools:               toolregistry.New(),
		SessionRuntimes:     mgr,
		ProviderSessionSync: store.NewProviderSessionSyncRepo(db),
		SessionEvents:       events,
	}

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(srv, impl)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	env := &cancelEnv{
		client:    llmv1.NewLlmServiceClient(conn),
		impl:      impl,
		sessionID: sessionID,
		history:   func() []*llmv1.Message { return ma.Core.History().History(sessionID, 0) },
		hub:       ma.Core.Session(sessionID),
		events:    events,
		cut:       make(chan struct{}, 8),
	}
	unsubscribe := env.hub.Subscribe(func(f *llmv1.ConversationStreamFrame) {
		if f.GetConversationAborted() == nil {
			return
		}
		select {
		case env.cut <- struct{}{}:
		default:
		}
	})
	t.Cleanup(unsubscribe)
	return env
}

func (e *cancelEnv) start(t *testing.T, ctx context.Context, userText string) llmv1.LlmService_ChatStreamClient {
	t.Helper()
	return e.startWithID(t, ctx, userText, "u1")
}

func (e *cancelEnv) startWithID(t *testing.T, ctx context.Context, userText, msgID string) llmv1.LlmService_ChatStreamClient {
	t.Helper()
	st, err := e.client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:       []*llmv1.Message{{Role: "user", Content: userText, Id: msgID}},
		SessionId:      strPtr(e.sessionID),
		PersistSession: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// stop is the user's explicit Stop: CancelTurn for the env's session. Client
// disconnect does not cut a turn, so this is the only way tests end one early.
func (e *cancelEnv) stop(t *testing.T, ctx context.Context) {
	t.Helper()
	resp, err := e.client.CancelTurn(ctx, &llmv1.CancelTurnRequest{SessionId: e.sessionID})
	if err != nil {
		t.Errorf("CancelTurn: %v", err)
		return
	}
	if !resp.GetCancelled() {
		t.Errorf("CancelTurn found no running turn for %q", e.sessionID)
	}
}

// drainUntilCut consumes the (already doomed) stream and then waits for the
// server to announce the turn was cut short, which it does only AFTER committing
// the salvaged partial — so the announcement is the commit's completion signal.
//
// Deliberately not a poll of the transcript: reading SQLite every few
// milliseconds competes with the commit for the write lock, and the commit path
// discards write errors, so a test that polls can make the very row it is
// waiting for fail to insert. Wait on the in-memory hub instead, then read once.
func (e *cancelEnv) drainUntilCut(t *testing.T, st llmv1.LlmService_ChatStreamClient) []*llmv1.Message {
	t.Helper()
	for {
		if _, err := st.Recv(); err != nil {
			break
		}
	}
	select {
	case <-e.cut:
	case <-time.After(10 * time.Second):
		t.Fatal("server never announced the cut turn")
	}
	return e.history()
}

// A cancelled partial must carry finish_reason into storage. Without it the
// persisted row is indistinguishable from a reply the model actually finished,
// so a client that reloads renders a truncated answer as a complete one.
func TestLlmServer_ChatStream_cancel_marks_partial_interrupted_in_history(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	const sid = "sess_cancel_reason"

	be := &cancelAfterPartialBackend{turnID: "turn_r", content: "half an answer", emitted: make(chan struct{})}
	env := newCancelEnv(t, ctx, sid, be, true)

	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	st := env.start(t, callCtx, "the question")
	go func() {
		<-be.emitted
		env.stop(t, ctx)
	}()

	hist := env.drainUntilCut(t, st)
	if len(hist) != 2 {
		t.Fatalf("want user + partial assistant persisted, got %d rows: %+v", len(hist), hist)
	}
	last := hist[len(hist)-1]
	if last.GetRole() != "assistant" || last.GetContent() != "half an answer" {
		t.Fatalf("want the partial assistant reply, got role=%q content=%q", last.GetRole(), last.GetContent())
	}
	if got := last.GetFinishReason(); got != finishReasonCancelled {
		t.Fatalf("interrupted partial must persist finish_reason=%q so a reloading client can tell it was cut short; got %q",
			finishReasonCancelled, got)
	}
}

// Salvage must not depend on the raw-chunk table. Those rows are written on the
// request ctx, which is already dead at cancel time (so the tail inserts fail),
// and the store is not configured for every embedder at all.
func TestLlmServer_ChatStream_cancel_salvages_partial_without_raw_chunk_store(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	const sid = "sess_cancel_nochunks"

	be := &cancelAfterPartialBackend{turnID: "turn_n", content: "salvaged from memory", emitted: make(chan struct{})}
	env := newCancelEnv(t, ctx, sid, be, false /* no raw-chunk store */)

	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	st := env.start(t, callCtx, "the question")
	go func() {
		<-be.emitted
		env.stop(t, ctx)
	}()

	hist := env.drainUntilCut(t, st)
	if len(hist) != 2 {
		t.Fatalf("want user + partial assistant persisted, got %d rows: %+v", len(hist), hist)
	}
	last := hist[len(hist)-1]
	if last.GetRole() != "assistant" || last.GetContent() != "salvaged from memory" {
		t.Fatalf("partial must survive with no raw-chunk store configured, got role=%q content=%q",
			last.GetRole(), last.GetContent())
	}
	if got := last.GetFinishReason(); got != finishReasonCancelled {
		t.Fatalf("finish_reason=%q, want %q", got, finishReasonCancelled)
	}
}

// finishAfterReleaseBackend streams a first chunk, then stalls until release is
// closed (standing in for a slow model while the client walks away), then
// streams the rest and completes normally. It only bails early if the TURN ctx
// dies — which, after a client disconnect, it must not.
type finishAfterReleaseBackend struct {
	stubBackend
	turnID  string
	first   string
	rest    string
	emitted chan struct{}
	release chan struct{}
	// returnNilOnCancel makes the backend treat a turn cancel as a clean end of
	// stream (as a killed CLI subprocess often looks), instead of returning ctx.Err().
	returnNilOnCancel bool
}

func (b *finishAfterReleaseBackend) ChatStream(ctx context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	fireCaptureStatefulSessionSink(ctx)
	frames := []*llmv1.ConversationStreamFrame{
		{SchemaVersion: conversationevents.SchemaVersion, Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: b.turnID}}},
		{SchemaVersion: conversationevents.SchemaVersion, Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
			AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: b.turnID, Delta: b.first}}},
	}
	for _, f := range frames {
		if err := emit(f); err != nil {
			return err
		}
	}
	close(b.emitted)
	select {
	case <-b.release:
	case <-ctx.Done():
		if b.returnNilOnCancel {
			return nil
		}
		return ctx.Err()
	}
	if err := emit(&llmv1.ConversationStreamFrame{SchemaVersion: conversationevents.SchemaVersion, Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
		AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: b.turnID, Delta: b.rest}}}); err != nil {
		return err
	}
	return emit(&llmv1.ConversationStreamFrame{SchemaVersion: conversationevents.SchemaVersion, Frame: &llmv1.ConversationStreamFrame_StreamComplete{
		StreamComplete: &llmv1.AssistantReply{Id: "asst_" + b.turnID, Content: b.first + b.rest, FinishReason: "end_turn"}}})
}

// awaitFinishedTurn waits for every in-flight turn on the env's server to commit.
func (e *cancelEnv) awaitFinishedTurn(t *testing.T) []*llmv1.Message {
	t.Helper()
	if !e.impl.WaitForTurns(10 * time.Second) {
		t.Fatal("turn never finished")
	}
	return e.history()
}

// A client deadline (or any client-side cancel) is the CLIENT going away, not a
// Stop: the turn must run to completion and persist the FULL reply, not a
// salvaged partial.
func TestLlmServer_ChatStream_client_deadline_does_not_cut_turn(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	const sid = "sess_client_deadline"

	be := &finishAfterReleaseBackend{turnID: "turn_d", first: "started, ", rest: "and finished.",
		emitted: make(chan struct{}), release: make(chan struct{})}
	env := newCancelEnv(t, ctx, sid, be, true)

	callCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	st := env.start(t, callCtx, "the question")
	<-be.emitted
	// Let the client's stream die of its deadline, then let the model finish.
	for {
		if _, err := st.Recv(); err != nil {
			break
		}
	}
	close(be.release)

	hist := env.awaitFinishedTurn(t)
	if len(hist) != 2 {
		t.Fatalf("want user + full assistant persisted, got %d rows: %+v", len(hist), hist)
	}
	last := hist[1]
	if last.GetContent() != "started, and finished." {
		t.Fatalf("turn must complete after the client left; got content=%q", last.GetContent())
	}
	if r := last.GetFinishReason(); r == finishReasonCancelled || r == finishReasonTimedOut {
		t.Fatalf("a completed turn must not be marked cut short; got finish_reason=%q", r)
	}
}

// Shutdown must not wait on work that, by design, outlives its client: a
// running turn (detached from its RPC) and an open session-events tail (follows
// the log until the client leaves). Ending the server lifetime context ends
// both promptly — the turn salvaging its partial as finish_reason="shutdown" —
// so a GracefulStop that follows has nothing left to wait for.
func TestLlmServer_shutdown_ends_running_turns_and_tails(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	const sid = "sess_shutdown"

	be := &finishAfterReleaseBackend{turnID: "turn_sd", first: "partial before shutdown", rest: "never",
		emitted: make(chan struct{}), release: make(chan struct{})}
	env := newCancelEnv(t, ctx, sid, be, true)
	base, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	env.impl.BaseContext = base

	// A subscriber that will never leave on its own.
	sub, err := env.client.SubscribeSessionEvents(ctx, &llmv1.SubscribeSessionEventsRequest{SessionId: sid})
	if err != nil {
		t.Fatal(err)
	}
	subDone := make(chan struct{})
	go func() {
		defer close(subDone)
		for {
			if _, err := sub.Recv(); err != nil {
				return
			}
		}
	}()

	st := env.start(t, ctx, "the question")
	<-be.emitted
	shutdown()

	hist := env.drainUntilCut(t, st)
	if len(hist) != 2 || hist[1].GetContent() != "partial before shutdown" {
		t.Fatalf("shutdown must salvage the running turn's partial, got %+v", hist)
	}
	if got := hist[1].GetFinishReason(); got != turnCutReasonShutdown {
		t.Fatalf("finish_reason=%q, want %q", got, turnCutReasonShutdown)
	}
	if !env.impl.WaitForTurns(5 * time.Second) {
		t.Fatal("turn still running after shutdown")
	}
	select {
	case <-subDone:
	case <-time.After(5 * time.Second):
		t.Fatal("session-events tail still open after shutdown: GracefulStop would wait on it forever")
	}
}

// A client returning mid-turn learns from GetSessionHistory that a turn is still
// running (so it rejoins the live stream instead of treating the transcript as
// settled), and the flag clears once the turn has committed.
func TestLlmServer_GetSessionHistory_reports_turn_in_progress(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	const sid = "sess_in_progress"

	be := &finishAfterReleaseBackend{turnID: "turn_ip", first: "a", rest: "b",
		emitted: make(chan struct{}), release: make(chan struct{})}
	env := newCancelEnv(t, ctx, sid, be, true)

	callCtx, cancel := context.WithCancel(ctx)
	st := env.start(t, callCtx, "q")
	<-be.emitted
	cancel() // the client leaves
	for {
		if _, err := st.Recv(); err != nil {
			break
		}
	}

	resp, err := env.client.GetSessionHistory(ctx, &llmv1.GetSessionHistoryRequest{SessionId: sid})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetTurnInProgress() {
		t.Fatal("turn_in_progress must be true while the detached turn is still running")
	}
	// A session load mid-turn must give back the message the user sent: it is
	// persisted at turn start, not with the reply at the end.
	if msgs := resp.GetMessages(); len(msgs) != 1 || msgs[0].GetRole() != "user" || msgs[0].GetContent() != "q" {
		t.Fatalf("mid-turn history must hold exactly the user's message; got %+v", msgs)
	}
	assertStatusOnlyMatches(t, ctx, env.client, sid, resp)

	close(be.release)
	env.awaitFinishedTurn(t)
	resp, err = env.client.GetSessionHistory(ctx, &llmv1.GetSessionHistoryRequest{SessionId: sid})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetTurnInProgress() {
		t.Fatal("turn_in_progress must clear once the turn has committed")
	}
	if n := len(resp.GetMessages()); n != 2 {
		t.Fatalf("finished turn should be in history: want 2 messages, got %d", n)
	}
	assertStatusOnlyMatches(t, ctx, env.client, sid, resp)
}

// assertStatusOnlyMatches checks that a status_only read reports the same
// turn/cursor status as the full read `full`, carries no transcript, and is
// flagged as status-only so its empty fields can't be mistaken for data.
func assertStatusOnlyMatches(t *testing.T, ctx context.Context, client llmv1.LlmServiceClient, sid string, full *llmv1.GetSessionHistoryResponse) {
	t.Helper()
	st, err := client.GetSessionHistory(ctx, &llmv1.GetSessionHistoryRequest{SessionId: sid, StatusOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !st.GetStatusOnly() {
		t.Fatal("status_only response must echo status_only=true")
	}
	if full.GetStatusOnly() {
		t.Fatal("a full read must not be flagged status_only")
	}
	if len(st.GetMessages()) != 0 || len(st.GetAttachmentRows()) != 0 || len(st.GetTrackedDocuments()) != 0 || st.GetInitialSeed() != "" {
		t.Fatalf("status_only must not load transcript data; got %+v", st)
	}
	if st.GetTurnInProgress() != full.GetTurnInProgress() ||
		st.GetEventsSeq() != full.GetEventsSeq() ||
		st.GetTurnStartSeq() != full.GetTurnStartSeq() {
		t.Fatalf("status mismatch: status_only=(%v,%d,%d) full=(%v,%d,%d)",
			st.GetTurnInProgress(), st.GetEventsSeq(), st.GetTurnStartSeq(),
			full.GetTurnInProgress(), full.GetEventsSeq(), full.GetTurnStartSeq())
	}
}

// Subprocess providers (Claude Code, Cursor) often surface a killed child as a
// clean end of stream. A Stop must still salvage the partial rather than fall
// into the "turn finished normally with no reply" path and persist nothing.
func TestLlmServer_ChatStream_stop_salvages_when_backend_returns_nil(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	const sid = "sess_nil_on_cancel"

	be := &finishAfterReleaseBackend{turnID: "turn_nil", first: "what I had so far", rest: "never sent",
		emitted: make(chan struct{}), release: make(chan struct{}), returnNilOnCancel: true}
	env := newCancelEnv(t, ctx, sid, be, true)

	st := env.start(t, ctx, "the question")
	go func() {
		<-be.emitted
		env.stop(t, ctx)
	}()

	hist := env.drainUntilCut(t, st)
	if len(hist) != 2 {
		t.Fatalf("want user + partial assistant persisted, got %d rows: %+v", len(hist), hist)
	}
	last := hist[1]
	if last.GetContent() != "what I had so far" {
		t.Fatalf("partial must be salvaged even when the backend returns nil; got content=%q", last.GetContent())
	}
	if got := last.GetFinishReason(); got != finishReasonCancelled {
		t.Fatalf("finish_reason=%q, want %q", got, finishReasonCancelled)
	}
}

func TestCutFinishReason(t *testing.T) {
	if got := cutFinishReason(context.DeadlineExceeded); got != finishReasonTimedOut {
		t.Fatalf("deadline should map to %q, got %q", finishReasonTimedOut, got)
	}
	if got := cutFinishReason(context.Canceled); got != finishReasonCancelled {
		t.Fatalf("cancel should map to %q, got %q", finishReasonCancelled, got)
	}
	// Wrapped errors still have to be recognised: the ctx error reaches this
	// through provider and gRPC layers that like to annotate.
	if got := cutFinishReason(fmt.Errorf("backend: %w", context.DeadlineExceeded)); got != finishReasonTimedOut {
		t.Fatalf("wrapped deadline should map to %q, got %q", finishReasonTimedOut, got)
	}
}

// Cancelling before the model produced anything must leave the user message
// alone in the transcript — not an empty assistant bubble that a reload would
// render as the model having answered with silence.
func TestLlmServer_ChatStream_cancel_before_output_persists_no_assistant_row(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	const sid = "sess_cancel_silent"

	be := &cancelSilentBackend{turnID: "turn_s", started: make(chan struct{})}
	env := newCancelEnv(t, ctx, sid, be, true)

	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	st := env.start(t, callCtx, "never answered")
	go func() {
		<-be.started
		env.stop(t, ctx)
	}()

	hist := env.drainUntilCut(t, st)
	if len(hist) == 0 {
		t.Fatal("the user message should still have been committed")
	}
	for _, m := range hist {
		if m.GetRole() == "assistant" {
			t.Fatalf("a turn cancelled before any output must not persist an assistant row; got %d rows: %+v",
				len(hist), hist)
		}
	}
}

// The aborting caller's stream is broken by definition, but everyone else on the
// session cancelled nothing: other windows folding hub frames, and replay after
// a reload. Both must receive a terminal frame, or they keep a turn "active"
// forever and show a spinner that never resolves.
func TestLlmServer_ChatStream_cancel_announces_terminal_frames_to_other_observers(t *testing.T) {
	ctx := testScope.OutgoingContext(context.Background())
	const sid = "sess_cancel_observers"

	be := &cancelAfterPartialBackend{turnID: "turn_o", content: "watched partial", emitted: make(chan struct{})}
	env := newCancelEnv(t, ctx, sid, be, true)

	// A second observer, subscribed before the turn starts — the other window.
	var mu sync.Mutex
	var sawFinishReason string
	var sawAborted bool
	unsubscribe := env.hub.Subscribe(func(f *llmv1.ConversationStreamFrame) {
		mu.Lock()
		defer mu.Unlock()
		if fin := f.GetAssistantTurnFinished(); fin != nil && fin.GetFinishReason() != "" {
			sawFinishReason = fin.GetFinishReason()
		}
		if f.GetConversationAborted() != nil {
			sawAborted = true
		}
	})
	defer unsubscribe()

	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	st := env.start(t, callCtx, "the question")
	go func() {
		<-be.emitted
		env.stop(t, ctx)
	}()
	env.drainUntilCut(t, st)

	mu.Lock()
	gotReason, gotAborted := sawFinishReason, sawAborted
	mu.Unlock()
	if gotReason != finishReasonCancelled {
		t.Fatalf("other observers must see the turn resolved with finish_reason=%q; got %q",
			finishReasonCancelled, gotReason)
	}
	if !gotAborted {
		t.Fatal("other observers must see a conversation_aborted frame for a cancelled turn")
	}

	// A client reloading after the turn reads the cancelled turn from history,
	// so the finished turn's events are trimmed down to the newest one.
	if !env.impl.WaitForTurns(5 * time.Second) {
		t.Fatal("turn never finished")
	}
	rows, err := env.events.GetAllEvents(context.Background(), testScope, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("event rows after the cancelled turn = %d, want 1 (trimmed)", len(rows))
	}
}

func TestAssistantMessageFromReply_carries_finish_reason(t *testing.T) {
	got := assistantMessageFromReply(&llmv1.AssistantReply{
		Content:      "partial",
		FinishReason: finishReasonCancelled,
	})
	if got.GetFinishReason() != finishReasonCancelled {
		t.Fatalf("finish_reason must survive the reply->message conversion; got %q", got.GetFinishReason())
	}
}

// TurnState.ToolCalls is a map, so a salvaged turn has to impose an order of its
// own or the same interruption yields a different transcript each time.
func TestAssistantReplyFromTurnState_orders_tool_calls_deterministically(t *testing.T) {
	state := conversationevents.IdleTurnState()
	for _, id := range []string{"tc_c", "tc_a", "tc_b"} {
		state.ToolCalls[id] = struct{ Name, ArgumentsJSON string }{Name: "read_code", ArgumentsJSON: "{}"}
	}
	for i := 0; i < 20; i++ {
		reply := assistantReplyFromTurnState(state, finishReasonCancelled)
		var ids []string
		for _, tc := range reply.GetToolCalls() {
			ids = append(ids, tc.GetId())
		}
		if len(ids) != 3 || ids[0] != "tc_a" || ids[1] != "tc_b" || ids[2] != "tc_c" {
			t.Fatalf("tool calls must come back sorted by id, got %v", ids)
		}
	}
}

func TestPartialWorthKeeping(t *testing.T) {
	cases := []struct {
		name  string
		reply *llmv1.AssistantReply
		want  bool
	}{
		{"nil", nil, false},
		{"empty", &llmv1.AssistantReply{}, false},
		{"whitespace only", &llmv1.AssistantReply{Content: "  \n\t "}, false},
		{"text", &llmv1.AssistantReply{Content: "something"}, true},
		{"thinking only", &llmv1.AssistantReply{ThinkingContent: "reasoning"}, true},
		{"tool call only", &llmv1.AssistantReply{ToolCalls: []*llmv1.ToolCall{{Id: "tc_1"}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := partialWorthKeeping(tc.reply); got != tc.want {
				t.Fatalf("partialWorthKeeping = %v, want %v", got, tc.want)
			}
		})
	}
}
