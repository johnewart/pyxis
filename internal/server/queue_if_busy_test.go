package server

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// newQueueIfBusyEnv is a drainEnv with the server drainer off, so entries the
// tests queue stay put and can be inspected.
func newQueueIfBusyEnv(t *testing.T) *drainEnv {
	t.Helper()
	e := newDrainEnv(t)
	e.impl.DisableServerQueueDrain = true
	return e
}

func userMsg(text string) []*llmv1.Message {
	return []*llmv1.Message{{Role: "user", Content: text, Id: "u-" + text}}
}

func TestTakeSessionOrEnqueue_idleSessionRunsNow(t *testing.T) {
	e := newQueueIfBusyEnv(t)
	rel, queued, err := e.impl.takeSessionOrEnqueue(e.ctx, drainSessionID, userMsg("hi"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if queued != nil || rel == nil {
		t.Fatalf("idle session should run now: queued=%v release=%v", queued, rel != nil)
	}
	// The caller holds the session until it releases.
	if !e.impl.sessionTurnGate().busy(drainSessionID) {
		t.Fatal("session slot should be held by the caller")
	}
	rel()
	if q := e.queued(t); len(q) != 0 {
		t.Fatalf("nothing should be queued, got %v", q)
	}
}

func TestTakeSessionOrEnqueue_runningTurnQueues(t *testing.T) {
	e := newQueueIfBusyEnv(t)
	hold, err := e.impl.sessionTurnGate().acquire(context.Background(), drainSessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	rel, queued, err := e.impl.takeSessionOrEnqueue(e.ctx, drainSessionID, userMsg("second"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rel != nil || queued.GetContent() != "second" {
		t.Fatalf("busy session should queue: release=%v queued=%v", rel != nil, queued)
	}
	if q := e.queued(t); len(q) != 1 || q[0] != "second" {
		t.Fatalf("queue = %v, want [second]", q)
	}
}

// The reported bug: the previous turn has ended, a drainer has claimed the next
// queued entry but not yet started its turn. The session slot is free, yet a
// new message must still wait behind that entry.
func TestTakeSessionOrEnqueue_claimedButNotStartedEntryQueues(t *testing.T) {
	e := newQueueIfBusyEnv(t)
	e.enqueue(t, "first")
	if _, err := e.impl.SessionQueue.ClaimNext(e.ctx, testScope, drainSessionID, "some-drainer"); err != nil {
		t.Fatal(err)
	}
	rel, queued, err := e.impl.takeSessionOrEnqueue(e.ctx, drainSessionID, userMsg("third"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rel != nil || queued == nil {
		t.Fatal("a claimed entry in the queue must make the new message queue behind it")
	}
	if q := e.queued(t); len(q) != 2 || q[0] != "first" || q[1] != "third" {
		t.Fatalf("queue = %v, want [first third]", q)
	}
	if e.impl.sessionTurnGate().busy(drainSessionID) {
		t.Fatal("the slot taken for the check must be released when queueing")
	}
}

func TestTakeSessionOrEnqueue_pendingEntryQueues(t *testing.T) {
	e := newQueueIfBusyEnv(t)
	e.enqueue(t, "first")
	_, queued, err := e.impl.takeSessionOrEnqueue(e.ctx, drainSessionID, userMsg("next"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if queued == nil {
		t.Fatal("a non-empty queue must not be jumped")
	}
}

func TestTakeSessionOrEnqueue_pausedEmptyQueueQueues(t *testing.T) {
	e := newQueueIfBusyEnv(t)
	e.setPaused(t, true)
	rel, queued, err := e.impl.takeSessionOrEnqueue(e.ctx, drainSessionID, userMsg("later"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rel != nil || queued.GetContent() != "later" {
		t.Fatal("queue-if-busy on a paused session means: add to the paused queue")
	}
}

func TestTakeSessionOrEnqueue_carriesDisableTools(t *testing.T) {
	e := newQueueIfBusyEnv(t)
	e.setPaused(t, true)
	_, queued, err := e.impl.takeSessionOrEnqueue(e.ctx, drainSessionID, userMsg("quiet"),
		&llmv1.TurnOptions{DisableTools: true, QueueIfBusy: true})
	if err != nil {
		t.Fatal(err)
	}
	if !queued.GetTurnOptions().GetDisableTools() {
		t.Fatal("disable_tools should ride along onto the queued entry")
	}
}

func TestTurnGate_tryAcquire(t *testing.T) {
	g := newTurnGate()
	rel, ok := g.tryAcquire("s")
	if !ok {
		t.Fatal("free session should be taken")
	}
	if _, ok := g.tryAcquire("s"); ok {
		t.Fatal("held session must not be taken twice")
	}
	rel()
	rel2, ok := g.tryAcquire("s")
	if !ok {
		t.Fatal("released session should be takeable again")
	}
	rel2()
}
