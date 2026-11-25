package store

import (
	"context"
	"testing"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
)

func TestTurnRegister_ScopesDoNotShareCounters(t *testing.T) {
	reg := NewTurnRegister(nil)
	ctx := context.Background()
	a := serverutil.Scope{TenantID: "tenant-a"}
	b := serverutil.Scope{TenantID: "tenant-b"}
	if _, err := reg.Advance(ctx, a, "shared", time.Now().UTC(), "turn-a"); err != nil {
		t.Fatalf("advance a: %v", err)
	}
	if got := reg.Current(b, "shared"); got != 0 {
		t.Fatalf("tenant b must not see tenant a's turn, got %d", got)
	}
	if got := reg.CurrentTurnID(b, "shared"); got != "" {
		t.Fatalf("tenant b must not see tenant a's turn id, got %q", got)
	}
	if got := reg.Current(a, "shared"); got != 1 {
		t.Fatalf("tenant a current: want 1, got %d", got)
	}
}

func TestTurnSeq(t *testing.T) {
	t.Run("AdvancePersists", func(t *testing.T) {
		db := NewTestSQLite(t)
		reg := NewTurnRegister(db)
		ctx := context.Background()

		seq, err := reg.Advance(ctx, serverutil.Scope{}, "sess-1", time.Now().UTC(), "")
		if err != nil {
			t.Fatalf("advance: %v", err)
		}
		if seq != 1 {
			t.Fatalf("expected seq 1, got %d", seq)
		}
		seq2, err := reg.Advance(ctx, serverutil.Scope{}, "sess-1", time.Now().UTC(), "ext-2")
		if err != nil {
			t.Fatalf("advance: %v", err)
		}
		if seq2 != 2 {
			t.Fatalf("expected seq 2, got %d", seq2)
		}

		var count int64
		if err := db.Model(&SessionTurnRow{}).Where("session_id = ?", "sess-1").Count(&count).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 2 {
			t.Fatalf("expected 2 persisted rows, got %d", count)
		}
	})

	t.Run("BootstrapReconstructsAfterRestart", func(t *testing.T) {
		db := NewTestSQLite(t)
		ctx := context.Background()

		reg1 := NewTurnRegister(db)
		for i := 0; i < 3; i++ {
			if _, err := reg1.Advance(ctx, serverutil.Scope{}, "sess-2", time.Now().UTC(), ""); err != nil {
				t.Fatalf("advance: %v", err)
			}
		}

		// Simulate a restart: a fresh in-memory register over the same persisted db.
		reg2 := NewTurnRegister(db)
		if got := reg2.Current(serverutil.Scope{}, "sess-2"); got != 0 {
			t.Fatalf("expected fresh register to start at 0, got %d", got)
		}
		bootstrapped, err := reg2.Bootstrap(ctx, serverutil.Scope{}, "sess-2")
		if err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		if bootstrapped != 3 {
			t.Fatalf("expected bootstrap to reconstruct 3, got %d", bootstrapped)
		}
		if got := reg2.Current(serverutil.Scope{}, "sess-2"); got != 3 {
			t.Fatalf("expected current 3 after bootstrap, got %d", got)
		}
	})

	t.Run("NoCollisionAfterBootstrap", func(t *testing.T) {
		db := NewTestSQLite(t)
		ctx := context.Background()

		reg1 := NewTurnRegister(db)
		for i := 0; i < 2; i++ {
			if _, err := reg1.Advance(ctx, serverutil.Scope{}, "sess-3", time.Now().UTC(), ""); err != nil {
				t.Fatalf("advance: %v", err)
			}
		}

		// Simulate a restart, bootstrap, then advance again — must not collide
		// with the PRIMARY KEY(session_id, turn_seq) rows already persisted.
		reg2 := NewTurnRegister(db)
		if _, err := reg2.Bootstrap(ctx, serverutil.Scope{}, "sess-3"); err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		next, err := reg2.Advance(ctx, serverutil.Scope{}, "sess-3", time.Now().UTC(), "")
		if err != nil {
			t.Fatalf("advance after bootstrap: %v", err)
		}
		if next != 3 {
			t.Fatalf("expected next seq 3, got %d", next)
		}

		var count int64
		if err := db.Model(&SessionTurnRow{}).Where("session_id = ?", "sess-3").Count(&count).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 3 {
			t.Fatalf("expected 3 persisted rows total, got %d", count)
		}
	})

	t.Run("AdvanceSelfHealsAcrossRestartWithoutBootstrap", func(t *testing.T) {
		// Production never called Bootstrap: a fresh register over a db with
		// existing turns must NOT collide on turn_seq=1 — Advance auto-bootstraps
		// from MAX(turn_seq) and resumes numbering. Regression for turn_seq being
		// silently NULL on every long/restarted session.
		db := NewTestSQLite(t)
		ctx := context.Background()

		reg1 := NewTurnRegister(db)
		for i := 0; i < 4; i++ {
			if _, err := reg1.Advance(ctx, serverutil.Scope{}, "sess-4", time.Now().UTC(), ""); err != nil {
				t.Fatalf("advance: %v", err)
			}
		}

		// Simulate a restart: brand-new in-memory register, no explicit Bootstrap.
		reg2 := NewTurnRegister(db)
		next, err := reg2.Advance(ctx, serverutil.Scope{}, "sess-4", time.Now().UTC(), "")
		if err != nil {
			t.Fatalf("advance after restart without bootstrap: %v", err)
		}
		if next != 5 {
			t.Fatalf("expected next seq 5 (resumed past persisted MAX 4), got %d", next)
		}
		if got := reg2.Current(serverutil.Scope{}, "sess-4"); got != 5 {
			t.Fatalf("expected current 5, got %d", got)
		}
	})
}
