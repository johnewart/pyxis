package server

import (
	"context"
	"net"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const queueSessionID = "sess_q"

type queueEnv struct {
	client llmv1.LlmServiceClient
	events *store.SessionEventRepo
	hub    *session.Hub
}

func newQueueEnv(t *testing.T, ctx context.Context) *queueEnv {
	t.Helper()
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: queueSessionID}, testScope); err != nil {
		t.Fatal(err)
	}
	ma, ok := mgr.Get(testScope, queueSessionID)
	if !ok {
		t.Fatal("missing session runtime")
	}
	events := store.NewSessionEventRepo(db)
	impl := &LlmServer{
		LLM:             &stubBackend{},
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		SessionEvents:   events,
		SessionDrafts:   store.NewSessionDraftRepo(db),
		SessionQueue:    store.NewSessionQueueRepo(db),
		// These tests drive the queue as a client drainer would and need entries
		// to stay put; server draining is covered in queue_drainer_test.go.
		DisableServerQueueDrain: true,
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

	return &queueEnv{
		client: llmv1.NewLlmServiceClient(conn),
		events: events,
		hub:    ma.Core.Session(queueSessionID),
	}
}

func queueCtx() context.Context {
	return testScope.OutgoingContext(context.Background())
}

func (e *queueEnv) enqueue(t *testing.T, ctx context.Context, content string) *llmv1.QueuedMessage {
	t.Helper()
	res, err := e.client.EnqueueMessage(ctx, &llmv1.EnqueueMessageRequest{
		SessionId: queueSessionID, Content: content,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res.GetEntry()
}

func (e *queueEnv) contents(t *testing.T, ctx context.Context) []string {
	t.Helper()
	res, err := e.client.ListQueue(ctx, &llmv1.ListQueueRequest{SessionId: queueSessionID})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(res.GetQueue()))
	for _, q := range res.GetQueue() {
		out = append(out, q.GetContent())
	}
	return out
}

func TestQueueRPCs_enqueueListRemoveReorder(t *testing.T) {
	ctx := queueCtx()
	env := newQueueEnv(t, ctx)

	a := env.enqueue(t, ctx, "a")
	b := env.enqueue(t, ctx, "b")
	c := env.enqueue(t, ctx, "c")
	if got, want := env.contents(t, ctx), []string{"a", "b", "c"}; !equalStringSlices(got, want) {
		t.Fatalf("queue: got %v want %v", got, want)
	}

	reordered, err := env.client.ReorderQueue(ctx, &llmv1.ReorderQueueRequest{
		SessionId: queueSessionID, EntryIds: []string{c.GetId(), a.GetId(), b.GetId()},
	})
	if err != nil {
		t.Fatal(err)
	}
	var gotOrder []string
	for _, q := range reordered.GetQueue() {
		gotOrder = append(gotOrder, q.GetContent())
	}
	if want := []string{"c", "a", "b"}; !equalStringSlices(gotOrder, want) {
		t.Fatalf("reorder response: got %v want %v", gotOrder, want)
	}

	rem, err := env.client.RemoveQueuedMessage(ctx, &llmv1.RemoveQueuedMessageRequest{
		SessionId: queueSessionID, EntryId: a.GetId(),
	})
	if err != nil || !rem.GetRemoved() {
		t.Fatalf("remove: %v %v", rem.GetRemoved(), err)
	}
	if got, want := env.contents(t, ctx), []string{"c", "b"}; !equalStringSlices(got, want) {
		t.Fatalf("after remove: got %v want %v", got, want)
	}

	// Removing something already gone is reported, not an error: two windows can
	// remove the same entry.
	rem2, err := env.client.RemoveQueuedMessage(ctx, &llmv1.RemoveQueuedMessageRequest{
		SessionId: queueSessionID, EntryId: a.GetId(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rem2.GetRemoved() {
		t.Fatal("removing an absent entry should report removed=false")
	}
}

// The drainer protocol, over the wire: claim, report the turn, retire.
func TestQueueRPCs_drainerLifecycle(t *testing.T) {
	ctx := queueCtx()
	env := newQueueEnv(t, ctx)
	env.enqueue(t, ctx, "do the thing")

	claim, err := env.client.ClaimNextQueuedMessage(ctx, &llmv1.ClaimNextQueuedMessageRequest{
		SessionId: queueSessionID, Owner: "host-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := claim.GetEntry()
	if entry == nil || entry.GetContent() != "do the thing" {
		t.Fatalf("claim: %+v", claim)
	}
	if entry.GetState() != string(store.QueuedRunning) || entry.GetClaimOwner() != "host-1" {
		t.Fatalf("claimed entry should be running and owned: %+v", entry)
	}
	if entry.GetClaimExpiresAtUnix() == 0 {
		t.Fatal("a claim must carry an expiry, or a dead drainer wedges the queue")
	}

	if _, err := env.client.HeartbeatQueuedMessage(ctx, &llmv1.HeartbeatQueuedMessageRequest{
		SessionId: queueSessionID, EntryId: entry.GetId(), Owner: "host-1",
	}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, err := env.client.MarkQueuedMessageStarted(ctx, &llmv1.MarkQueuedMessageStartedRequest{
		SessionId: queueSessionID, EntryId: entry.GetId(), Owner: "host-1", TurnId: "turn_1",
	}); err != nil {
		t.Fatalf("mark started: %v", err)
	}
	if _, err := env.client.RetireQueuedMessage(ctx, &llmv1.RetireQueuedMessageRequest{
		SessionId: queueSessionID, EntryId: entry.GetId(), Owner: "host-1",
	}); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if got := env.contents(t, ctx); len(got) != 0 {
		t.Fatalf("a retired entry should leave the queue empty, got %v", got)
	}
}

// A drainer that is not the owner must be refused, and told so in a way that
// says "you were superseded" rather than "something broke".
func TestQueueRPCs_nonOwnerIsRefused(t *testing.T) {
	ctx := queueCtx()
	env := newQueueEnv(t, ctx)
	env.enqueue(t, ctx, "mine")

	claim, err := env.client.ClaimNextQueuedMessage(ctx, &llmv1.ClaimNextQueuedMessageRequest{
		SessionId: queueSessionID, Owner: "owner-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.client.RetireQueuedMessage(ctx, &llmv1.RetireQueuedMessageRequest{
		SessionId: queueSessionID, EntryId: claim.GetEntry().GetId(), Owner: "owner-b",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("a non-owner retire should be FailedPrecondition, got %v", err)
	}
}

// Pause is reported distinctly from an empty queue: a drainer loop has to keep
// polling a paused queue rather than conclude it is drained and exit.
func TestQueueRPCs_pausedClaimIsDistinctFromEmpty(t *testing.T) {
	ctx := queueCtx()
	env := newQueueEnv(t, ctx)
	env.enqueue(t, ctx, "held back")

	if _, err := env.client.SetQueuePaused(ctx, &llmv1.SetQueuePausedRequest{
		SessionId: queueSessionID, Paused: true,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := env.client.ClaimNextQueuedMessage(ctx, &llmv1.ClaimNextQueuedMessageRequest{
		SessionId: queueSessionID, Owner: "drainer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetEntry() != nil {
		t.Fatal("a paused queue must not hand out work")
	}
	if !res.GetPaused() {
		t.Fatal("a paused claim must say so, or a drainer cannot tell it from a drained queue")
	}
	if listed, err := env.client.ListQueue(ctx, &llmv1.ListQueueRequest{SessionId: queueSessionID}); err != nil || !listed.GetPaused() {
		t.Fatalf("ListQueue should report paused: %v %v", listed.GetPaused(), err)
	}

	// An empty, unpaused queue is the other case: no entry, not paused.
	if _, err := env.client.SetQueuePaused(ctx, &llmv1.SetQueuePausedRequest{
		SessionId: queueSessionID, Paused: false,
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := env.client.ClaimNextQueuedMessage(ctx, &llmv1.ClaimNextQueuedMessageRequest{
		SessionId: queueSessionID, Owner: "drainer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.GetEntry() == nil {
		t.Fatal("resume should release the held entry")
	}
	if _, err := env.client.RetireQueuedMessage(ctx, &llmv1.RetireQueuedMessageRequest{
		SessionId: queueSessionID, EntryId: claimed.GetEntry().GetId(), Owner: "drainer",
	}); err != nil {
		t.Fatal(err)
	}
	drained, err := env.client.ClaimNextQueuedMessage(ctx, &llmv1.ClaimNextQueuedMessageRequest{
		SessionId: queueSessionID, Owner: "drainer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if drained.GetEntry() != nil || drained.GetPaused() {
		t.Fatalf("a drained queue is neither busy nor paused: %+v", drained)
	}
}

// The queue is shared state that several windows edit, so an edit has to reach
// the others rather than only the client that made it.
func TestQueueRPCs_editsAreAnnouncedToOtherWindows(t *testing.T) {
	ctx := queueCtx()
	env := newQueueEnv(t, ctx)

	seen := make(chan *llmv1.EvQueueChanged, 16)
	unsubscribe := env.hub.Subscribe(func(f *llmv1.ConversationStreamFrame) {
		if qc := f.GetQueueChanged(); qc != nil {
			select {
			case seen <- qc:
			default:
			}
		}
	})
	defer unsubscribe()

	env.enqueue(t, ctx, "watch me")

	select {
	case qc := <-seen:
		if len(qc.GetQueue()) != 1 || qc.GetQueue()[0].GetContent() != "watch me" {
			t.Fatalf("queue-changed should carry the whole queue, got %+v", qc.GetQueue())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("enqueue was never announced to other windows")
	}

	if _, err := env.client.SetQueuePaused(ctx, &llmv1.SetQueuePausedRequest{
		SessionId: queueSessionID, Paused: true,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case qc := <-seen:
			if qc.GetPaused() {
				return
			}
		case <-deadline:
			t.Fatal("pause was never announced to other windows")
		}
	}
}

func TestDraftRPCs_roundTripAndClear(t *testing.T) {
	ctx := queueCtx()
	env := newQueueEnv(t, ctx)

	got, err := env.client.GetSessionDraft(ctx, &llmv1.GetSessionDraftRequest{SessionId: queueSessionID})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetContent() != "" {
		t.Fatalf("no draft should read empty, got %q", got.GetContent())
	}

	if _, err := env.client.UpsertSessionDraft(ctx, &llmv1.UpsertSessionDraftRequest{
		SessionId: queueSessionID, Content: "unsent thought",
	}); err != nil {
		t.Fatal(err)
	}
	got, err = env.client.GetSessionDraft(ctx, &llmv1.GetSessionDraftRequest{SessionId: queueSessionID})
	if err != nil || got.GetContent() != "unsent thought" {
		t.Fatalf("draft round trip: %q %v", got.GetContent(), err)
	}

	if _, err := env.client.ClearSessionDraft(ctx, &llmv1.ClearSessionDraftRequest{SessionId: queueSessionID}); err != nil {
		t.Fatal(err)
	}
	got, err = env.client.GetSessionDraft(ctx, &llmv1.GetSessionDraftRequest{SessionId: queueSessionID})
	if err != nil || got.GetContent() != "" {
		t.Fatalf("cleared draft: %q %v", got.GetContent(), err)
	}
}

func TestQueueRPCs_unknownSessionIsRejected(t *testing.T) {
	ctx := queueCtx()
	env := newQueueEnv(t, ctx)
	_, err := env.client.EnqueueMessage(ctx, &llmv1.EnqueueMessageRequest{
		SessionId: "no-such-session", Content: "hello",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown session should be NotFound, got %v", err)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
