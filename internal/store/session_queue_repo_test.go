package store

import (
	"context"
	"errors"
	"github.com/johnewart/pyxis/internal/serverutil"
	"sync"
	"testing"
	"time"
)

const (
	qWorkspace = "ws_queue"
	qSession   = "sess_queue"
)

// newQueueRepo returns a queue repo over a fresh DB, with the session row the
// pause flag lives on already present.
func newQueueRepo(t *testing.T) *SessionQueueRepo {
	t.Helper()
	db := NewTestSQLite(t)
	if err := db.Create(&SessionRow{TenantID: qWorkspace, SessionID: qSession}).Error; err != nil {
		t.Fatal(err)
	}
	return NewSessionQueueRepo(db)
}

func mustEnqueue(t *testing.T, r *SessionQueueRepo, content string) *SessionQueuedMessageRow {
	t.Helper()
	row, err := r.Enqueue(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, content)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func queueContents(t *testing.T, r *SessionQueueRepo) []string {
	t.Helper()
	rows, err := r.List(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Content)
	}
	return out
}

func TestSessionQueue_enqueueAppendsAndListsInOrder(t *testing.T) {
	r := newQueueRepo(t)
	mustEnqueue(t, r, "first")
	mustEnqueue(t, r, "second")
	mustEnqueue(t, r, "third")

	if got, want := queueContents(t, r), []string{"first", "second", "third"}; !equalStrings(got, want) {
		t.Fatalf("queue order: got %v want %v", got, want)
	}
}

// The point of the queue is that exactly one drainer runs each message. Several
// clients can be looping at once (a host process plus an open tab), so the claim has
// to be decided in the database, not by whoever asked first.
func TestSessionQueue_concurrentDrainersEachClaimDistinctEntries(t *testing.T) {
	r := newQueueRepo(t)
	const n = 12
	for i := 0; i < n; i++ {
		mustEnqueue(t, r, "msg")
	}

	var mu sync.Mutex
	claims := map[string]int{}
	var wg sync.WaitGroup
	// Each "drainer" claims, then retires, so the next entry becomes head.
	for d := 0; d < 4; d++ {
		owner := "drainer-" + string(rune('a'+d))
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				row, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, owner)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if row == nil {
					return
				}
				mu.Lock()
				claims[row.ID]++
				mu.Unlock()
				if err := r.Retire(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, row.ID, owner); err != nil {
					t.Errorf("retire: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(claims) != n {
		t.Fatalf("every entry must be claimed exactly once: %d of %d entries claimed", len(claims), n)
	}
	for id, times := range claims {
		if times != 1 {
			t.Fatalf("entry %s was claimed %d times — two drainers ran the same message", id, times)
		}
	}
}

// A live claim at the head means "busy", not "empty": a second drainer must get
// nothing rather than skipping ahead and running messages out of order.
func TestSessionQueue_liveClaimBlocksTheHead(t *testing.T) {
	r := newQueueRepo(t)
	mustEnqueue(t, r, "first")
	mustEnqueue(t, r, "second")

	first, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || first.Content != "first" {
		t.Fatalf("expected to claim the head, got %+v", first)
	}

	next, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "owner-b")
	if err != nil {
		t.Fatal(err)
	}
	if next != nil {
		t.Fatalf("a second drainer must not skip past a running head, got %q", next.Content)
	}
}

// A drainer that dies cannot release its claim. Without expiry the queue would
// be wedged forever — the exact crash this feature exists to survive.
func TestSessionQueue_expiredClaimIsReclaimable(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	r := newQueueRepo(t).WithLeaseForTest(time.Minute, clock)
	mustEnqueue(t, r, "abandoned")

	claimed, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "dead-drainer")
	if err != nil || claimed == nil {
		t.Fatalf("initial claim failed: %v %+v", err, claimed)
	}

	// Still leased: nobody else may take it.
	if row, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "other"); err != nil || row != nil {
		t.Fatalf("claim during live lease: got %+v err %v", row, err)
	}

	now = now.Add(2 * time.Minute) // lease lapses; the drainer never came back
	reclaimed, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "fresh-drainer")
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed == nil || reclaimed.ID != claimed.ID {
		t.Fatalf("an abandoned entry must become claimable again, got %+v", reclaimed)
	}
	if reclaimed.ClaimOwner != "fresh-drainer" {
		t.Fatalf("reclaim must transfer ownership, got %q", reclaimed.ClaimOwner)
	}
}

// Heartbeating is how a drainer proves its turn is still running, so a long
// CLI-backed turn is not reclaimed out from under it.
func TestSessionQueue_heartbeatKeepsAClaimAlive(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	r := newQueueRepo(t).WithLeaseForTest(time.Minute, clock)
	mustEnqueue(t, r, "long turn")

	claimed, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "owner-a")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	for i := 0; i < 5; i++ {
		now = now.Add(30 * time.Second)
		if err := r.Heartbeat(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, claimed.ID, "owner-a"); err != nil {
			t.Fatalf("heartbeat %d: %v", i, err)
		}
		if row, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "thief"); err != nil || row != nil {
			t.Fatalf("a heartbeat-ed claim must not be reclaimable: got %+v err %v", row, err)
		}
	}
}

// A drainer that lost its lease must find out, so it stops streaming into work
// somebody else may now own.
func TestSessionQueue_heartbeatAfterLosingLeaseFails(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	r := newQueueRepo(t).WithLeaseForTest(time.Minute, clock)
	mustEnqueue(t, r, "taken over")

	claimed, _ := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "owner-a")
	now = now.Add(2 * time.Minute)
	if _, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "owner-b"); err != nil {
		t.Fatal(err)
	}
	err := r.Heartbeat(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, claimed.ID, "owner-a")
	if !errors.Is(err, ErrQueueEntryNotClaimed) {
		t.Fatalf("stale owner heartbeat should fail with ErrQueueEntryNotClaimed, got %v", err)
	}
}

// The duplicate-send trap: a drainer claims, starts the turn, then dies. The
// message HAS been delivered, so reclaiming must not send it a second time.
func TestSessionQueue_entryThatAlreadyStartedATurnIsNeverRerun(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	r := newQueueRepo(t).WithLeaseForTest(time.Minute, clock)
	mustEnqueue(t, r, "already sent")
	mustEnqueue(t, r, "still waiting")

	claimed, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "doomed")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	// The drainer got as far as minting a turn, then vanished.
	if err := r.MarkStarted(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, claimed.ID, "doomed", "turn_abc"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)

	next, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "recovering")
	if err != nil {
		t.Fatal(err)
	}
	if next == nil {
		t.Fatal("the following entry should still be claimable")
	}
	if next.ID == claimed.ID {
		t.Fatal("an entry whose turn already started must never be handed out again — that duplicates the user's message")
	}
	if next.Content != "still waiting" {
		t.Fatalf("expected the next queued message, got %q", next.Content)
	}
	// And the delivered entry is gone from the queue rather than lingering.
	for _, c := range queueContents(t, r) {
		if c == "already sent" {
			t.Fatal("a delivered entry should be retired, not left in the queue")
		}
	}
}

// Pause has to be enforced here, not in the drainer: there can be several
// drainers and an old one must not be able to ignore it.
func TestSessionQueue_pauseIsEnforcedOnClaim(t *testing.T) {
	r := newQueueRepo(t)
	mustEnqueue(t, r, "held")

	if err := r.SetPaused(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, true); err != nil {
		t.Fatal(err)
	}
	if paused, err := r.Paused(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession); err != nil || !paused {
		t.Fatalf("paused: %v %v", paused, err)
	}
	_, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "eager")
	if !errors.Is(err, ErrQueuePaused) {
		t.Fatalf("claim while paused must report ErrQueuePaused (not just 'nothing to do'), got %v", err)
	}

	if err := r.SetPaused(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, false); err != nil {
		t.Fatal(err)
	}
	row, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "eager")
	if err != nil || row == nil {
		t.Fatalf("resume should make the queue claimable again: %+v %v", row, err)
	}
}

func TestSessionQueue_removeDropsAnEntry(t *testing.T) {
	r := newQueueRepo(t)
	mustEnqueue(t, r, "keep")
	doomed := mustEnqueue(t, r, "drop")

	ok, err := r.Remove(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, doomed.ID)
	if err != nil || !ok {
		t.Fatalf("remove: %v %v", ok, err)
	}
	if got, want := queueContents(t, r), []string{"keep"}; !equalStrings(got, want) {
		t.Fatalf("after remove: got %v want %v", got, want)
	}
	if ok, _ := r.Remove(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, doomed.ID); ok {
		t.Fatal("removing a missing entry should report false")
	}
}

func TestSessionQueue_reorderAppliesRequestedOrder(t *testing.T) {
	r := newQueueRepo(t)
	a := mustEnqueue(t, r, "a")
	b := mustEnqueue(t, r, "b")
	c := mustEnqueue(t, r, "c")

	if err := r.Reorder(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, []string{c.ID, a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	if got, want := queueContents(t, r), []string{"c", "a", "b"}; !equalStrings(got, want) {
		t.Fatalf("reordered queue: got %v want %v", got, want)
	}
	// ...and the new order is what a drainer sees.
	row, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "owner")
	if err != nil || row == nil {
		t.Fatalf("claim: %+v %v", row, err)
	}
	if row.Content != "c" {
		t.Fatalf("reorder must change what runs next, got %q", row.Content)
	}
}

// A partial reorder (the UI listed only some entries, or one ran meanwhile) must
// never silently drop queued work.
func TestSessionQueue_reorderKeepsUnmentionedEntries(t *testing.T) {
	r := newQueueRepo(t)
	a := mustEnqueue(t, r, "a")
	mustEnqueue(t, r, "b")
	c := mustEnqueue(t, r, "c")

	if err := r.Reorder(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, []string{c.ID, a.ID, "nonexistent-id"}); err != nil {
		t.Fatal(err)
	}
	if got, want := queueContents(t, r), []string{"c", "a", "b"}; !equalStrings(got, want) {
		t.Fatalf("unmentioned entries must survive, ordered after the listed ones: got %v want %v", got, want)
	}
}

// Shutting a tab cleanly should hand work back where it was, not send it to the
// back of the queue.
func TestSessionQueue_releaseReturnsEntryAtSamePosition(t *testing.T) {
	r := newQueueRepo(t)
	mustEnqueue(t, r, "first")
	mustEnqueue(t, r, "second")

	claimed, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "quitting")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if err := r.Release(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, claimed.ID, "quitting"); err != nil {
		t.Fatal(err)
	}
	if got, want := queueContents(t, r), []string{"first", "second"}; !equalStrings(got, want) {
		t.Fatalf("after release: got %v want %v", got, want)
	}
	again, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "next-drainer")
	if err != nil || again == nil || again.Content != "first" {
		t.Fatalf("a released entry should be immediately claimable at its old position, got %+v", again)
	}
}

func TestSessionQueue_emptyQueueClaimsNothing(t *testing.T) {
	r := newQueueRepo(t)
	row, err := r.ClaimNext(context.Background(), serverutil.Scope{TenantID: qWorkspace}, qSession, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if row != nil {
		t.Fatalf("empty queue should claim nothing, got %+v", row)
	}
}

func equalStrings(a, b []string) bool {
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
