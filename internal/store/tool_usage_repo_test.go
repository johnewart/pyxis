package store

import (
	"context"
	"testing"
	"time"
)

func TestToolUsage_AggregateMedianOddEven(t *testing.T) {
	repo := NewToolUsageRepo(NewTestSQLite(t))
	ctx := context.Background()
	for _, d := range []int64{10, 30, 20} {
		if err := repo.Record(ctx, ToolUsageRow{TenantID: "t", ToolName: "odd", Source: "chat", Success: true, DurationMs: d}); err != nil {
			t.Fatal(err)
		}
	}
	for i, d := range []int64{10, 20, 30, 50} {
		if err := repo.Record(ctx, ToolUsageRow{TenantID: "t", ToolName: "even", Source: "mcp", Success: i != 0, DurationMs: d}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.AggregateByTool(ctx, ToolUsageFilter{TenantID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("stats=%+v", got)
	}
	wantEven := ToolStats{ToolName: "even", Calls: 4, Errors: 1, MinMs: 10, MedianMs: 25, MeanMs: 27.5, MaxMs: 50}
	wantOdd := ToolStats{ToolName: "odd", Calls: 3, Errors: 0, MinMs: 10, MedianMs: 20, MeanMs: 20, MaxMs: 30}
	if got[0] != wantEven {
		t.Fatalf("even=%+v want %+v", got[0], wantEven)
	}
	if got[1] != wantOdd {
		t.Fatalf("odd=%+v want %+v", got[1], wantOdd)
	}
}

func TestToolUsage_AggregateByTool_TimeWindow(t *testing.T) {
	repo := NewToolUsageRepo(NewTestSQLite(t))
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// before Since, at Since, inside, at Until, after Until.
	for i, off := range []time.Duration{-time.Hour, 0, time.Minute, time.Hour, 2 * time.Hour} {
		row := ToolUsageRow{TenantID: "t", ToolName: "a", Success: true, DurationMs: int64(10 * (i + 1)), CreatedAt: base.Add(off)}
		if err := repo.Record(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	since, until := base, base.Add(time.Hour)
	got, err := repo.AggregateByTool(ctx, ToolUsageFilter{TenantID: "t", Since: &since, Until: &until})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Calls != 2 || got[0].MinMs != 20 || got[0].MaxMs != 30 || got[0].MedianMs != 25 {
		t.Fatalf("stats=%+v", got)
	}
}

func TestToolUsage_AggregateByTool_ScopeFilter(t *testing.T) {
	repo := NewToolUsageRepo(NewTestSQLite(t))
	ctx := context.Background()
	for scope, d := range map[string]int64{"s1": 10, "s2": 500} {
		if err := repo.Record(ctx, ToolUsageRow{TenantID: "t", ScopeID: scope, ToolName: "a", Success: true, DurationMs: d}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.AggregateByTool(ctx, ToolUsageFilter{TenantID: "t", ScopeID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Calls != 1 || got[0].MaxMs != 10 {
		t.Fatalf("stats=%+v", got)
	}
}

func TestToolUsage_AggregateByTool_TenantIsolation(t *testing.T) {
	repo := NewToolUsageRepo(NewTestSQLite(t))
	ctx := context.Background()
	for tenant, d := range map[string]int64{"t1": 10, "t2": 500} {
		if err := repo.Record(ctx, ToolUsageRow{TenantID: tenant, ToolName: "a", Success: true, DurationMs: d}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.AggregateByTool(ctx, ToolUsageFilter{TenantID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Calls != 1 || got[0].MaxMs != 10 {
		t.Fatalf("stats=%+v", got)
	}
}

func TestToolUsage_Prune(t *testing.T) {
	db := NewTestSQLite(t)
	repo := NewToolUsageRepo(db)
	ctx := context.Background()
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	if err := repo.Record(ctx, ToolUsageRow{TenantID: "t", ToolName: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Record(ctx, ToolUsageRow{TenantID: "t", ToolName: "a", CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	n, err := repo.Prune(ctx, time.Now().UTC().Add(-ToolUsageRetention))
	if err != nil || n != 1 {
		t.Fatalf("pruned=%d err=%v", n, err)
	}
	var left int64
	if err := db.Model(&ToolUsageRow{}).Count(&left).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 1 {
		t.Fatalf("left=%d", left)
	}
}

func TestToolUsage_FirstRecordAfterStartupPrunes(t *testing.T) {
	db := NewTestSQLite(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-ToolUsageRetention - time.Hour)
	if err := db.Create(&ToolUsageRow{TenantID: "t", ToolName: "old", CreatedAt: old}).Error; err != nil {
		t.Fatal(err)
	}
	// A fresh repo (simulating a restart) prunes on its first Record.
	repo := NewToolUsageRepo(db)
	if err := repo.Record(ctx, ToolUsageRow{TenantID: "t", ToolName: "new"}); err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := db.Model(&ToolUsageRow{}).Pluck("tool_name", &names).Error; err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "new" {
		t.Fatalf("rows=%v", names)
	}
}

func TestToolUsage_OldSchemaDBOpens(t *testing.T) {
	db := NewTestSQLite(t)
	if err := db.Migrator().DropTable(&ToolUsageRow{}); err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate over DB without llmd_tool_usage: %v", err)
	}
	if !db.Migrator().HasTable(&ToolUsageRow{}) {
		t.Fatal("llmd_tool_usage not created")
	}
}
