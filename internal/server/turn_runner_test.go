package server

import (
	"context"
	"testing"
	"time"
)

type ctxKey struct{}

func TestTurnRunner_requestCancelDoesNotCancelTurn(t *testing.T) {
	r := newTurnRunner()
	req, reqCancel := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "v"))
	ctx, finish := r.start(context.Background(), req, "s1")
	defer finish()

	reqCancel()
	if ctx.Err() != nil {
		t.Fatalf("turn ctx died with the request: %v", ctx.Err())
	}
	if got := ctx.Value(ctxKey{}); got != "v" {
		t.Fatalf("request values not carried: got %v", got)
	}
}

// A session's last finished turn stays reportable after it ends (and while the
// next one runs), so a client opening the session can show how long it took.
func TestTurnRunner_recordsStartAndLastFinishedTurn(t *testing.T) {
	r := newTurnRunner()
	if _, ok := r.lastFinished("s1"); ok {
		t.Fatal("lastFinished before any turn ran")
	}
	before := time.Now()
	_, finish := r.start(context.Background(), context.Background(), "s1")
	started, ok := r.startedAt("s1")
	if !ok || started.Before(before) {
		t.Fatalf("startedAt = %v, %v; want a time at/after %v", started, ok, before)
	}
	time.Sleep(5 * time.Millisecond)
	finish()
	if _, ok := r.startedAt("s1"); ok {
		t.Fatal("startedAt still reported after the turn finished")
	}
	ft, ok := r.lastFinished("s1")
	if !ok || !ft.startedAt.Equal(started) || !ft.endedAt.After(ft.startedAt) {
		t.Fatalf("lastFinished = %+v, %v; want start %v and a later end", ft, ok, started)
	}

	// A new turn running does not hide the last finished one.
	_, finish2 := r.start(context.Background(), context.Background(), "s1")
	defer finish2()
	if got, ok := r.lastFinished("s1"); !ok || got != ft {
		t.Fatalf("lastFinished during next turn = %+v, %v; want %+v", got, ok, ft)
	}
	if _, ok := r.lastFinished("other"); ok {
		t.Fatal("lastFinished leaked to another session")
	}
}

func TestTurnRunner_explicitCancelCarriesReason(t *testing.T) {
	r := newTurnRunner()
	ctx, finish := r.start(context.Background(), context.Background(), "s1")
	defer finish()

	if !r.running("s1") {
		t.Fatal("turn not registered")
	}
	if !r.cancel("s1", "") {
		t.Fatal("cancel did not find the turn")
	}
	if ctx.Err() == nil {
		t.Fatal("turn ctx not cancelled")
	}
	if got := turnCutReason(ctx); got != turnCutReasonCancelled {
		t.Fatalf("reason = %q, want %q", got, turnCutReasonCancelled)
	}
}

func TestTurnRunner_cancelUnknownSessionIsNoop(t *testing.T) {
	r := newTurnRunner()
	if r.cancel("nope", "") {
		t.Fatal("cancel reported a turn for an idle session")
	}
}

func TestTurnRunner_baseCancelIsShutdown(t *testing.T) {
	r := newTurnRunner()
	base, baseCancel := context.WithCancel(context.Background())
	ctx, finish := r.start(base, context.Background(), "s1")
	defer finish()

	baseCancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("base cancel did not reach the turn")
	}
	if got := turnCutReason(ctx); got != turnCutReasonShutdown {
		t.Fatalf("reason = %q, want %q", got, turnCutReasonShutdown)
	}
}

func TestTurnRunner_finishUnregistersAndWaitReturns(t *testing.T) {
	r := newTurnRunner()
	_, finish := r.start(context.Background(), context.Background(), "s1")

	if r.wait(20 * time.Millisecond) {
		t.Fatal("wait returned true while a turn was in flight")
	}
	finish()
	finish() // idempotent
	if r.running("s1") {
		t.Fatal("turn still registered after finish")
	}
	if !r.wait(time.Second) {
		t.Fatal("wait timed out after finish")
	}
	if r.cancel("s1", "") {
		t.Fatal("cancel found a finished turn")
	}
}

func TestTurnRunner_staleFinishDoesNotUnregisterNewerTurn(t *testing.T) {
	r := newTurnRunner()
	_, finishOld := r.start(context.Background(), context.Background(), "s1")
	_, finishNew := r.start(context.Background(), context.Background(), "s1")
	defer finishNew()

	finishOld()
	if !r.running("s1") {
		t.Fatal("finishing the older turn unregistered the newer one")
	}
}
