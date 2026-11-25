package server

import (
	"context"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/toolregistry"
	"gorm.io/gorm"
)

const drainSessionID = "sess_drain"

type drainEnv struct {
	impl *LlmServer
	ma   *sessionmanager.SessionRuntime
	ctx  context.Context
	db   *gorm.DB
}

func newDrainEnv(t *testing.T) *drainEnv {
	t.Helper()
	ctx := testScope.IncomingContext(context.Background())
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: drainSessionID}, testScope); err != nil {
		t.Fatal(err)
	}
	ma, ok := mgr.Get(testScope, drainSessionID)
	if !ok {
		t.Fatal("missing session runtime")
	}
	impl := &LlmServer{
		LLM:             &stubBackend{},
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		SessionEvents:   store.NewSessionEventRepo(db),
		SessionQueue:    store.NewSessionQueueRepo(db),
	}
	return &drainEnv{impl: impl, ma: ma, ctx: ctx, db: db}
}

func (e *drainEnv) enqueue(t *testing.T, content string) {
	t.Helper()
	if _, err := e.impl.EnqueueMessage(e.ctx, &llmv1.EnqueueMessageRequest{SessionId: drainSessionID, Content: content}); err != nil {
		t.Fatal(err)
	}
}

func (e *drainEnv) setPaused(t *testing.T, paused bool) {
	t.Helper()
	if _, err := e.impl.SetQueuePaused(e.ctx, &llmv1.SetQueuePausedRequest{SessionId: drainSessionID, Paused: paused}); err != nil {
		t.Fatal(err)
	}
}

func (e *drainEnv) settle(t *testing.T) {
	t.Helper()
	if !e.impl.WaitForQueueDrains(10 * time.Second) {
		t.Fatal("queue drain did not finish")
	}
}

func (e *drainEnv) queued(t *testing.T) []string {
	t.Helper()
	res, err := e.impl.ListQueue(e.ctx, &llmv1.ListQueueRequest{SessionId: drainSessionID})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, q := range res.GetQueue() {
		out = append(out, q.GetContent())
	}
	return out
}

func (e *drainEnv) userTurns() []string {
	var out []string
	for _, m := range e.ma.Core.History().History(drainSessionID, 0) {
		if m.GetRole() == "user" {
			out = append(out, m.GetContent())
		}
	}
	return out
}

// The point of the feature: queue a message, walk away, and it runs with no
// client attached.
func TestQueueDrain_idleSessionRunsEnqueuedMessage(t *testing.T) {
	e := newDrainEnv(t)
	e.enqueue(t, "keep going")
	e.settle(t)

	if q := e.queued(t); len(q) != 0 {
		t.Fatalf("queue should be drained, still has %v", q)
	}
	if got := e.userTurns(); len(got) != 1 || got[0] != "keep going" {
		t.Fatalf("want the queued message sent as a user turn, got %v", got)
	}
	hist := e.ma.Core.History().History(drainSessionID, 0)
	if last := hist[len(hist)-1]; last.GetRole() != "assistant" {
		t.Fatalf("want an assistant reply after the queued turn, got role=%q", last.GetRole())
	}
}

// A queued entry's attachments ride along when it is drained: persisted
// anchored to the user message the entry becomes, and listed on the entry
// while it waits.
func TestQueueDrain_entryAttachmentsPersistOnTheSentMessage(t *testing.T) {
	e := newDrainEnv(t)
	attRepo := store.NewChatAttachmentRepo(e.db)
	e.impl.Attachments = attRepo

	e.setPaused(t, true)
	if _, err := e.impl.EnqueueMessage(e.ctx, &llmv1.EnqueueMessageRequest{
		SessionId: drainSessionID, Content: "with context",
		Attachments: []*llmv1.MessageAttachment{{Id: "qa1", Kind: "composer_text", PayloadJson: `{}`, Content: "QUEUED-CTX"}},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := e.impl.ListQueue(e.ctx, &llmv1.ListQueueRequest{SessionId: drainSessionID})
	if err != nil {
		t.Fatal(err)
	}
	if q := res.GetQueue(); len(q) != 1 || len(q[0].GetAttachments()) != 1 || q[0].GetAttachments()[0].GetId() != "qa1" {
		t.Fatalf("queued entry should list its attachment, got %+v", q)
	}
	entryID := res.GetQueue()[0].GetId()

	e.setPaused(t, false)
	e.settle(t)

	rows, err := attRepo.LoadRows(e.ctx, testScope, drainSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 persisted attachment, got %d", len(rows))
	}
	if want := `"anchor_message_id":"queued_` + entryID + `"`; !strings.Contains(rows[0].Content, want) {
		t.Fatalf("attachment not anchored to the sent message: %s", rows[0].Content)
	}
}

func TestQueueDrain_runsEntriesInOrder(t *testing.T) {
	e := newDrainEnv(t)
	e.setPaused(t, true)
	for _, c := range []string{"first", "second", "third"} {
		e.enqueue(t, c)
	}
	e.settle(t)
	if got := e.userTurns(); len(got) != 0 {
		t.Fatalf("paused queue must not run, got turns %v", got)
	}

	e.setPaused(t, false)
	e.settle(t)
	got := e.userTurns()
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
	if q := e.queued(t); len(q) != 0 {
		t.Fatalf("queue should be drained, still has %v", q)
	}
}

// A server-started turn has no client to run client-owned tools, so such
// sessions are left to a client drainer.
func TestQueueDrain_skipsSessionsWithClientOwnedTools(t *testing.T) {
	e := newDrainEnv(t)
	if err := e.ma.Tools.Register(&llmv1.ToolDefinition{
		Name:                 "client_echo",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
		Execution:            &llmv1.ToolDefinition_DelegateToClient{DelegateToClient: &llmv1.ToolExecutionDelegateToClient{}},
	}); err != nil {
		t.Fatal(err)
	}
	e.enqueue(t, "needs the client")
	e.settle(t)

	if q := e.queued(t); len(q) != 1 || q[0] != "needs the client" {
		t.Fatalf("entry must stay queued for a client drainer, got %v", q)
	}
	if got := e.userTurns(); len(got) != 0 {
		t.Fatalf("server must not run turns on a client-tool session, got %v", got)
	}
}

func TestQueueDrain_disabledLeavesQueueAlone(t *testing.T) {
	e := newDrainEnv(t)
	e.impl.DisableServerQueueDrain = true
	e.enqueue(t, "stay")
	e.settle(t)
	if q := e.queued(t); len(q) != 1 {
		t.Fatalf("disabled drain must leave the entry, got %v", q)
	}
}

// Stale leftover work is paused at startup, not run and not dropped.
func TestQueueDrain_startupSweepPausesStaleLeftoverWork(t *testing.T) {
	e := newDrainEnv(t)
	e.impl.DisableServerQueueDrain = true
	e.enqueue(t, "from long ago")
	e.impl.DisableServerQueueDrain = false

	// Negative window: everything counts as stale.
	e.impl.SweepQueuesOnStartup(context.Background(), -1)
	e.settle(t)
	if got := e.userTurns(); len(got) != 0 {
		t.Fatalf("stale leftover queue must not run, got %v", got)
	}
	res, err := e.impl.ListQueue(e.ctx, &llmv1.ListQueueRequest{SessionId: drainSessionID})
	if err != nil {
		t.Fatal(err)
	}
	if !res.GetPaused() || len(res.GetQueue()) != 1 {
		t.Fatalf("want 1 entry kept and the queue paused, got paused=%v queue=%d", res.GetPaused(), len(res.GetQueue()))
	}

	// Resuming runs it.
	e.setPaused(t, false)
	e.settle(t)
	if got := e.userTurns(); len(got) != 1 || got[0] != "from long ago" {
		t.Fatalf("resumed queue should run, got %v", got)
	}
}

// Recent work queued before a restart runs once the host sweeps at startup.
func TestQueueDrain_startupSweepDrainsLeftoverWork(t *testing.T) {
	e := newDrainEnv(t)
	e.impl.DisableServerQueueDrain = true
	e.enqueue(t, "from before the restart")
	e.impl.DisableServerQueueDrain = false

	e.impl.SweepQueuesOnStartup(context.Background(), 12*time.Hour)
	e.settle(t)
	if got := e.userTurns(); len(got) != 1 || got[0] != "from before the restart" {
		t.Fatalf("startup sweep should run leftover work, got %v", got)
	}
}
