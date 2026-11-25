package store

import (
	"context"
	"testing"
	"time"
)

func seedUsage(t *testing.T, repo *UsageRepo, base time.Time) {
	t.Helper()
	rows := []UsageRecordRow{
		{TenantID: "t1", ScopeID: "s1", Model: "m1", CreatedAt: base, InputTokens: 10, OutputTokens: 5, CostUSD: 0.5,
			Principal: "alice", ProviderID: "p1", ProviderType: "anthropic", ServedBy: "node-a", RPC: "Chat", DurationMs: 250, Outcome: "ok"},
		{TenantID: "t1", ScopeID: "s1", Model: "m1", CreatedAt: base.Add(time.Hour), InputTokens: 20, OutputTokens: 7, CacheReadTokens: 3, CostUSD: 1},
		{TenantID: "t1", ScopeID: "s1", Model: "m2", CreatedAt: base.Add(2 * time.Hour), InputTokens: 1, OutputTokens: 1},
		{TenantID: "t2", ScopeID: "s9", Model: "m1", CreatedAt: base.Add(3 * time.Hour), InputTokens: 100},
	}
	for _, r := range rows {
		if err := repo.RecordUsage(context.Background(), r); err != nil {
			t.Fatalf("RecordUsage: %v", err)
		}
	}
}

func TestUsageRepo_RecordAndQueryRange(t *testing.T) {
	repo := NewUsageRepo(NewTestSQLite(t))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedUsage(t, repo, base)
	ctx := context.Background()

	all, _, err := repo.QueryUsage(ctx, UsageFilter{})
	if err != nil || len(all) != 4 {
		t.Fatalf("QueryUsage all: n=%d err=%v", len(all), err)
	}
	first := all[0]
	if first.Outcome != "ok" || first.DurationMs != 250 || first.Principal != "alice" ||
		first.ProviderID != "p1" || first.ProviderType != "anthropic" || first.ServedBy != "node-a" || first.RPC != "Chat" {
		t.Fatalf("new fields not stored: %+v", first)
	}

	since, until := base.Add(time.Hour), base.Add(3*time.Hour)
	got, _, err := repo.QueryUsage(ctx, UsageFilter{Since: &since, Until: &until})
	if err != nil {
		t.Fatalf("QueryUsage range: %v", err)
	}
	// since inclusive, until exclusive: rows at +1h and +2h.
	if len(got) != 2 || got[0].InputTokens != 20 || got[1].Model != "m2" {
		t.Fatalf("range filter mismatch: %+v", got)
	}

	got, _, err = repo.QueryUsage(ctx, UsageFilter{TenantID: "t2"})
	if err != nil || len(got) != 1 || got[0].InputTokens != 100 {
		t.Fatalf("tenant filter: %+v err=%v", got, err)
	}
}

func TestUsageRepo_QueryUsageGroupedTotals(t *testing.T) {
	repo := NewUsageRepo(NewTestSQLite(t))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedUsage(t, repo, base)
	ctx := context.Background()

	rows, got, err := repo.QueryUsage(ctx, UsageFilter{})
	if err != nil {
		t.Fatalf("QueryUsage: %v", err)
	}
	if len(rows) != 4 || len(got) != 3 {
		t.Fatalf("want 4 rows and 3 groups, got %d rows, %+v", len(rows), got)
	}
	g := got[0] // t1/s1/m1
	if g.TenantID != "t1" || g.Model != "m1" || g.Calls != 2 || g.InputTokens != 30 ||
		g.OutputTokens != 12 || g.CacheReadTokens != 3 || g.CostUSD != 1.5 {
		t.Fatalf("t1/s1/m1 totals mismatch: %+v", g)
	}

	until := base.Add(time.Hour) // excludes everything but the first row
	_, got, err = repo.QueryUsage(ctx, UsageFilter{Until: &until})
	if err != nil || len(got) != 1 || got[0].InputTokens != 10 || got[0].Calls != 1 {
		t.Fatalf("ranged totals: %+v err=%v", got, err)
	}
}
