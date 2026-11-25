package store

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// Subscribers follow the event log by seq (after_seq cursors, rejoining a
// running turn), so seq must be unique and gap-free per session even when
// several writers append at once — a ChatStream turn and an MCP tool-event
// publisher do exactly that. Seq is assigned as MAX(seq)+1; unserialized,
// two appenders read the same MAX and one insert fails on the primary key,
// silently losing an event.
func TestSessionEventRepo_concurrentAppendsGetUniqueGapFreeSeqs(t *testing.T) {
	db := NewTestSQLite(t)
	repo := NewSessionEventRepo(db)
	sc := serverutil.Scope{TenantID: "t1", ScopeID: "s1"}
	const sid = "sess-concurrent"
	const writers, perWriter = 8, 25

	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := repo.AppendEvent(context.Background(), sc, sid, fmt.Sprintf(`{"w":%d,"i":%d}`, w, i)); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("append failed under concurrency: %v", err)
	}

	rows, err := repo.GetAllEvents(context.Background(), sc, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != writers*perWriter {
		t.Fatalf("got %d events, want %d (events lost)", len(rows), writers*perWriter)
	}
	for i, r := range rows {
		if r.Seq != int64(i+1) {
			t.Fatalf("row %d has seq %d, want %d (gap or duplicate)", i, r.Seq, i+1)
		}
	}
}

func TestSessionEventRepo_LastSeqIsTheSubscriptionCursor(t *testing.T) {
	db := NewTestSQLite(t)
	repo := NewSessionEventRepo(db)
	sc := serverutil.Scope{TenantID: "t1", ScopeID: "s1"}
	ctx := context.Background()

	if got, err := repo.LastSeq(ctx, sc, "empty"); err != nil || got != 0 {
		t.Fatalf("LastSeq on an empty session = %d, %v; want 0, nil", got, err)
	}
	for i := 0; i < 3; i++ {
		if _, err := repo.AppendEvent(ctx, sc, "s", `{}`); err != nil {
			t.Fatal(err)
		}
	}
	head, err := repo.LastSeq(ctx, sc, "s")
	if err != nil || head != 3 {
		t.Fatalf("LastSeq = %d, %v; want 3, nil", head, err)
	}
	// Subscribing from the head yields only what comes next.
	if _, err := repo.AppendEvent(ctx, sc, "s", `{"next":true}`); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.GetEventsSince(ctx, sc, "s", head)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Seq != 4 {
		t.Fatalf("events after head = %+v; want exactly seq 4", rows)
	}
	// Scoped: another tenant's session with the same id is independent.
	if got, _ := repo.LastSeq(ctx, serverutil.Scope{TenantID: "t2", ScopeID: "s1"}, "s"); got != 0 {
		t.Fatalf("LastSeq leaked across tenants: %d", got)
	}
}

// PruneEventsBefore drops only rows strictly below the cursor, keeps seq
// monotonic (even when the cursor IS the head), and never touches other
// sessions.
func TestSessionEventRepo_PruneEventsBeforeKeepsSeqMonotonic(t *testing.T) {
	db := NewTestSQLite(t)
	repo := NewSessionEventRepo(db)
	sc := serverutil.Scope{TenantID: "t1", ScopeID: "s1"}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := repo.AppendEvent(ctx, sc, "s", `{}`); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.AppendEvent(ctx, sc, "other", `{}`); err != nil {
			t.Fatal(err)
		}
	}
	// Cursor == head: a turn that appended nothing. Only seq 1..4 go.
	n, err := repo.PruneEventsBefore(ctx, sc, "s", 5)
	if err != nil || n != 4 {
		t.Fatalf("prune = %d, %v; want 4, nil", n, err)
	}
	if head, _ := repo.LastSeq(ctx, sc, "s"); head != 5 {
		t.Fatalf("head after prune = %d; want 5 (the cursor row must survive)", head)
	}
	if seq, err := repo.AppendEvent(ctx, sc, "s", `{}`); err != nil || seq != 6 {
		t.Fatalf("next append = %d, %v; want 6 (seq must never go backwards)", seq, err)
	}
	if rows, _ := repo.GetEventsSince(ctx, sc, "s", 5); len(rows) != 1 || rows[0].Seq != 6 {
		t.Fatalf("subscriber at the cursor must still see what follows; got %+v", rows)
	}
	if c, _ := repo.CountEventsForSession(ctx, sc, "other"); c != 5 {
		t.Fatalf("prune leaked into another session: %d rows left, want 5", c)
	}
}
