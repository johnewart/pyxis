package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// Async (streamed deltas) and sync (tool events, turn status) appends to the
// same session must come out in the order they were made, with no seq gaps,
// however the writer batches them.
func TestAppendEventAsyncKeepsOrderWithSyncAppends(t *testing.T) {
	db := openFileStore(t)
	repo := NewSessionEventRepo(db)
	sc := serverutil.Scope{TenantID: "tenant", ScopeID: "scope"}
	ctx := context.Background()

	const n = 500
	for i := 0; i < n; i++ {
		ev := fmt.Sprintf(`{"i":%d}`, i)
		if i%50 == 49 {
			if _, err := repo.AppendEvent(ctx, sc, "s", ev); err != nil {
				t.Fatalf("sync append %d: %v", i, err)
			}
			continue
		}
		if err := repo.AppendEventAsync(sc, "s", ev); err != nil {
			t.Fatalf("async append %d: %v", i, err)
		}
	}
	if err := repo.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.GetAllEvents(ctx, sc, "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != n {
		t.Fatalf("got %d rows, want %d", len(rows), n)
	}
	for i, row := range rows {
		var ev struct{ I int }
		if err := json.Unmarshal([]byte(row.EventJSON), &ev); err != nil {
			t.Fatal(err)
		}
		if row.Seq != int64(i+1) || ev.I != i {
			t.Fatalf("row %d: seq=%d event=%d, want seq=%d event=%d", i, row.Seq, ev.I, i+1, i)
		}
	}
}

// Subscribers see every session's rows in seq order, batched or not.
func TestAppendEventAsyncPublishesInOrder(t *testing.T) {
	db := openFileStore(t)
	repo := NewSessionEventRepo(db)
	sc := serverutil.Scope{TenantID: "tenant", ScopeID: "scope"}

	var mu sync.Mutex
	last := map[string]int64{}
	var bad []string
	unsub := repo.SubscribeAll(func(_ serverutil.Scope, sid string, row SessionEventRow) {
		mu.Lock()
		defer mu.Unlock()
		if row.Seq != last[sid]+1 {
			bad = append(bad, fmt.Sprintf("%s: seq %d after %d", sid, row.Seq, last[sid]))
		}
		last[sid] = row.Seq
	})
	defer unsub()

	var wg sync.WaitGroup
	for s := 0; s < 4; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if err := repo.AppendEventAsync(sc, fmt.Sprintf("s%d", s), `{}`); err != nil {
					t.Error(err)
				}
			}
		}(s)
	}
	wg.Wait()
	if err := repo.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bad) > 0 {
		t.Fatalf("out of order: %v", bad[:min(5, len(bad))])
	}
	for s := 0; s < 4; s++ {
		if got := last[fmt.Sprintf("s%d", s)]; got != 200 {
			t.Fatalf("s%d: last published seq %d, want 200", s, got)
		}
	}
}

// A delete covers events queued before it, even ones not yet written.
func TestDeleteEventsForSessionCoversQueuedAppends(t *testing.T) {
	db := openFileStore(t)
	repo := NewSessionEventRepo(db)
	sc := serverutil.Scope{TenantID: "tenant", ScopeID: "scope"}
	ctx := context.Background()

	for i := 0; i < 300; i++ {
		if err := repo.AppendEventAsync(sc, "s", `{}`); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.DeleteEventsForSession(ctx, sc, "s"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	n, err := repo.CountEventsForSession(ctx, sc, "s")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d events survived the delete", n)
	}
}

func TestAppendEventAsyncRejectsMissingIDs(t *testing.T) {
	repo := NewSessionEventRepo(openFileStore(t))
	if err := repo.AppendEventAsync(serverutil.Scope{}, "s", `{}`); err == nil {
		t.Fatal("expected an error without a tenant")
	}
	if err := repo.AppendEventAsync(serverutil.Scope{TenantID: "t"}, "", `{}`); err == nil {
		t.Fatal("expected an error without a session")
	}
}
