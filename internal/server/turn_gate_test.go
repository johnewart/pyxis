package server

import (
	"context"
	"sync"
	"testing"
	"time"
)

// waitForQueued blocks until the gate reports n waiters for sid, so a test can
// establish arrival order deterministically instead of by sleeping.
func waitForQueued(t *testing.T, g *turnGate, sid string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if g.waiting(sid) == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("gate never reached %d queued waiters for %q (have %d)", n, sid, g.waiting(sid))
}

// Arrival order is the whole point: a message that was sent second must not be
// answered before one that was sent first.
func TestTurnGate_servesWaitersInArrivalOrder(t *testing.T) {
	g := newTurnGate()
	const sid = "s1"

	release, err := g.acquire(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var order []int
	releases := make([]func(), 3)
	var wg sync.WaitGroup
	for i := 1; i <= 3; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, aerr := g.acquire(context.Background(), sid)
			if aerr != nil {
				t.Errorf("waiter %d: %v", i, aerr)
				return
			}
			mu.Lock()
			order = append(order, i)
			releases[i-1] = rel
			mu.Unlock()
		}()
		// Each waiter is fully queued before the next one is started, so the
		// arrival order under test is the one we intended.
		waitForQueued(t, g, sid, i)
	}

	// Hand the slot down the line one at a time.
	release()
	for i := 0; i < 3; i++ {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			got := len(order)
			rel := releases[i]
			mu.Unlock()
			if got > i && rel != nil {
				rel()
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("waiters must run in arrival order, got %v", order)
	}
}

// A caller that gives up while queued must take itself out of the line without
// stranding the turns behind it.
func TestTurnGate_abandonedWaiterDoesNotBlockTheLine(t *testing.T) {
	g := newTurnGate()
	const sid = "s1"

	release, err := g.acquire(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}

	quitCtx, quit := context.WithCancel(context.Background())
	abandoned := make(chan error, 1)
	go func() {
		_, aerr := g.acquire(quitCtx, sid)
		abandoned <- aerr
	}()
	waitForQueued(t, g, sid, 1)

	survivor := make(chan func(), 1)
	go func() {
		rel, aerr := g.acquire(context.Background(), sid)
		if aerr != nil {
			t.Errorf("survivor: %v", aerr)
			return
		}
		survivor <- rel
	}()
	waitForQueued(t, g, sid, 2)

	quit()
	select {
	case aerr := <-abandoned:
		if aerr == nil {
			t.Fatal("a caller whose ctx died while queued must not be handed a turn")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("abandoned waiter never returned")
	}

	release()
	select {
	case rel := <-survivor:
		rel()
	case <-time.After(2 * time.Second):
		t.Fatal("the turn behind an abandoned waiter never ran")
	}
}

// Whichever way the abandon/handoff race falls, the slot must not be lost: the
// next turn always gets to run.
func TestTurnGate_abandonRacingHandoffNeverStrandsTheSlot(t *testing.T) {
	for i := 0; i < 200; i++ {
		g := newTurnGate()
		const sid = "s1"
		release, err := g.acquire(context.Background(), sid)
		if err != nil {
			t.Fatal(err)
		}

		raceCtx, quit := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rel, aerr := g.acquire(raceCtx, sid); aerr == nil {
				rel()
			}
		}()
		waitForQueued(t, g, sid, 1)

		// Release and abandon at the same moment.
		go release()
		go quit()
		wg.Wait()

		// The session must be usable again no matter who won.
		done := make(chan struct{})
		go func() {
			rel, aerr := g.acquire(context.Background(), sid)
			if aerr == nil {
				rel()
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: slot was stranded after an abandon/handoff race", i)
		}
	}
}

// The gate is per session: one session's long turn must not hold up another's.
func TestTurnGate_isPerSession(t *testing.T) {
	g := newTurnGate()
	releaseA, err := g.acquire(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()

	done := make(chan struct{})
	go func() {
		rel, aerr := g.acquire(context.Background(), "b")
		if aerr == nil {
			rel()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a turn in session b blocked behind session a")
	}
}

// A sessionless call has no transcript to order, so it must never queue.
func TestTurnGate_emptySessionIDNeverQueues(t *testing.T) {
	g := newTurnGate()
	first, err := g.acquire(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		rel, aerr := g.acquire(context.Background(), "   ")
		if aerr == nil {
			rel()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sessionless calls must not serialize against each other")
	}
	first()
}

// Release is the caller's defer, and defers can run on paths that already
// released; a double release must not hand the slot out twice.
func TestTurnGate_releaseIsIdempotent(t *testing.T) {
	g := newTurnGate()
	const sid = "s1"
	release, err := g.acquire(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()

	// Two turns arrive; the second must still have to wait for the first.
	firstRelease, err := g.acquire(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	second := make(chan struct{})
	go func() {
		rel, aerr := g.acquire(context.Background(), sid)
		if aerr == nil {
			rel()
		}
		close(second)
	}()
	waitForQueued(t, g, sid, 1)
	select {
	case <-second:
		t.Fatal("a double release let a second turn run while the first still held the session")
	case <-time.After(50 * time.Millisecond):
	}
	firstRelease()
	select {
	case <-second:
	case <-time.After(2 * time.Second):
		t.Fatal("second turn never ran")
	}
}

// An idle session must not keep state around.
func TestTurnGate_forgetsIdleSessions(t *testing.T) {
	g := newTurnGate()
	release, err := g.acquire(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	release()
	g.mu.Lock()
	n := len(g.sessions)
	g.mu.Unlock()
	if n != 0 {
		t.Fatalf("idle sessions must be dropped, %d still tracked", n)
	}
}
