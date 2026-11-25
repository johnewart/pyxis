package server

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
)

func seedLeftoverEvents(t *testing.T, s *LlmServer, sc serverutil.Scope, sid string, n int) {
	t.Helper()
	body := `{"pad":"` + strings.Repeat("y", 4000) + `"}`
	for i := 0; i < n; i++ {
		if _, err := s.SessionEvents.AppendEvent(context.Background(), sc, sid, body); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompact_TrimsVacuumsAndSerializes(t *testing.T) {
	s, db := storageTestServer(t)
	sc := serverutil.Scope{TenantID: "compact_tenant", ScopeID: "sub/folder"}
	seedSessionWithMessages(t, db, sc, "a", "A", 1, 10)
	seedSessionWithMessages(t, db, sc, "b", "B", 1, 10)
	seedLeftoverEvents(t, s, sc, "a", 200)
	seedLeftoverEvents(t, s, sc, "b", 100)
	ctx := sc.IncomingContext(context.Background())

	// A caller that gives up immediately must not stop the run.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, _ = s.Compact(cancelled, &llmv1.CompactRequest{})

	// The run must finish on its own: wait until a and b are trimmed.
	for _, sid := range []string{"a", "b"} {
		var c int64
		deadline := time.Now().Add(10 * time.Second)
		for {
			var err error
			c, err = s.SessionEvents.CountEventsForSession(context.Background(), sc, sid)
			if err != nil {
				t.Fatal(err)
			}
			if c == 1 || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if c != 1 {
			t.Fatalf("session %q has %d events after cancelled compact, want 1 (the cancelled caller must not stop the run)", sid, c)
		}
	}

	// Fresh leftover events give the concurrent run real work so callers overlap.
	seedSessionWithMessages(t, db, sc, "c", "C", 1, 10)
	seedLeftoverEvents(t, s, sc, "c", 2000)

	// Two concurrent callers share one run.
	var wg sync.WaitGroup
	results := make([]*llmv1.CompactResponse, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.Compact(ctx, &llmv1.CompactRequest{})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}

	for _, sid := range []string{"a", "b"} {
		var c int64
		deadline := time.Now().Add(10 * time.Second)
		for {
			var err error
			c, err = s.SessionEvents.CountEventsForSession(context.Background(), sc, sid)
			if err != nil {
				t.Fatal(err)
			}
			if c == 1 || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if c != 1 {
			t.Fatalf("session %q has %d events after compact, want 1 (the cancelled caller must not stop the run)", sid, c)
		}
	}
	for i, r := range results {
		if r.GetBytesAfter() > r.GetBytesBefore() {
			t.Errorf("caller %d: bytes_after %d > bytes_before %d", i, r.GetBytesAfter(), r.GetBytesBefore())
		}
		if r.GetBytesBefore() <= 0 {
			t.Errorf("caller %d: bytes_before = %d, want > 0", i, r.GetBytesBefore())
		}
	}
	if results[0].GetRunId() == "" || results[0].GetRunId() != results[1].GetRunId() {
		t.Fatalf("concurrent callers ran separately: run ids %q and %q", results[0].GetRunId(), results[1].GetRunId())
	}
}
