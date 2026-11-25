package store

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

// ToolUsageRetention is how long llmd_tool_usage rows are kept.
const ToolUsageRetention = 90 * 24 * time.Hour

// toolUsagePruneEvery is the number of Record calls between automatic prunes,
// so pruning never runs on every write.
const toolUsagePruneEvery = 500

// ToolUsageRow is one tool execution (llmd_tool_usage).
type ToolUsageRow struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement"`
	TenantID   string    `gorm:"column:tenant_id;index:idx_tool_usage_scope_created,priority:1;size:256;not null"`
	ScopeID    string    `gorm:"column:scope_id;index:idx_tool_usage_scope_created,priority:2;size:256;not null;default:''"`
	SessionID  string    `gorm:"column:session_id;index:idx_tool_usage_session;size:256;not null;default:''"`
	ToolName   string    `gorm:"column:tool_name;index:idx_tool_usage_tool_name;size:256;not null"`
	Source     string    `gorm:"column:source;size:64;not null;default:''"`
	Success    bool      `gorm:"column:success;not null"`
	DurationMs int64     `gorm:"column:duration_ms;not null;default:0"`
	CreatedAt  time.Time `gorm:"column:created_at;index:idx_tool_usage_scope_created,priority:3;index:idx_tool_usage_created_at;not null"`
}

func (ToolUsageRow) TableName() string { return "llmd_tool_usage" }

// ToolUsageFilter filters tool usage rows; empty strings mean no filter, nil
// Since/Until mean unbounded (Since inclusive, Until exclusive).
type ToolUsageFilter struct {
	TenantID string
	ScopeID  string
	ToolName string
	Source   string
	Since    *time.Time
	Until    *time.Time
}

// ToolUsageRepo persists and queries tool usage.
type ToolUsageRepo struct {
	db     *gorm.DB
	writes atomic.Int64
}

// NewToolUsageRepo returns nil when db is nil (persistence disabled).
func NewToolUsageRepo(db *gorm.DB) *ToolUsageRepo {
	if db == nil {
		return nil
	}
	return &ToolUsageRepo{db: db}
}

// Record writes one row (CreatedAt defaults to now) and prunes rows older than
// ToolUsageRetention every toolUsagePruneEvery writes. A prune failure is
// returned after a successful insert.
func (r *ToolUsageRepo) Record(ctx context.Context, row ToolUsageRow) error {
	if r == nil || r.db == nil {
		return nil
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("record tool usage: %w", err)
	}
	// Prune on the first write after startup (so short-lived processes still
	// prune) and then every toolUsagePruneEvery writes.
	if n := r.writes.Add(1); n == 1 || n%toolUsagePruneEvery == 0 {
		if _, err := r.Prune(ctx, time.Now().UTC().Add(-ToolUsageRetention)); err != nil {
			return err
		}
	}
	return nil
}

// Prune deletes rows created before olderThan and returns how many.
func (r *ToolUsageRepo) Prune(ctx context.Context, olderThan time.Time) (int64, error) {
	if r == nil || r.db == nil {
		return 0, nil
	}
	res := r.db.WithContext(ctx).Where("created_at < ?", olderThan).Delete(&ToolUsageRow{})
	if res.Error != nil {
		return 0, fmt.Errorf("prune tool usage: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// filtered returns a query over ToolUsageRow rows matching f.
func (r *ToolUsageRepo) filtered(ctx context.Context, f ToolUsageFilter) *gorm.DB {
	tx := r.db.WithContext(ctx).Model(&ToolUsageRow{})
	if f.TenantID != "" {
		tx = tx.Where("tenant_id = ?", f.TenantID)
	}
	if f.ScopeID != "" {
		tx = tx.Where("scope_id = ?", f.ScopeID)
	}
	if f.ToolName != "" {
		tx = tx.Where("tool_name = ?", f.ToolName)
	}
	if f.Source != "" {
		tx = tx.Where("source = ?", f.Source)
	}
	if f.Since != nil {
		tx = tx.Where("created_at >= ?", *f.Since)
	}
	if f.Until != nil {
		tx = tx.Where("created_at < ?", *f.Until)
	}
	return tx
}

// ToolStats summarizes one tool across all sources.
type ToolStats struct {
	ToolName string
	Calls    int64
	Errors   int64
	MinMs    int64
	MedianMs float64
	MeanMs   float64
	MaxMs    int64
}

// AggregateByTool returns per-tool call/error counts and min/median/mean/max
// duration over matching rows, sorted by tool name. Counts, min, max and mean
// come from one GROUP BY query; each tool's median reads at most two rows.
func (r *ToolUsageRepo) AggregateByTool(ctx context.Context, f ToolUsageFilter) ([]ToolStats, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	type agg struct {
		ToolName string
		Calls    int64
		Errors   int64
		MinMs    int64
		MaxMs    int64
		MeanMs   float64
	}
	var aggs []agg
	err := r.filtered(ctx, f).
		Select("tool_name AS tool_name, COUNT(*) AS calls, " +
			"SUM(CASE WHEN success THEN 0 ELSE 1 END) AS errors, " +
			"MIN(duration_ms) AS min_ms, MAX(duration_ms) AS max_ms, AVG(duration_ms) AS mean_ms").
		Group("tool_name").Order("tool_name").
		Scan(&aggs).Error
	if err != nil {
		return nil, fmt.Errorf("aggregate tool usage by tool: %w", err)
	}
	out := make([]ToolStats, 0, len(aggs))
	for _, a := range aggs {
		// Median: rows (n-1)/2 and, for an even n, the next one.
		limit := 1
		if a.Calls%2 == 0 {
			limit = 2
		}
		var mid []int64
		if err := r.filtered(ctx, f).Where("tool_name = ?", a.ToolName).
			Order("duration_ms").Limit(limit).Offset(int((a.Calls-1)/2)).
			Pluck("duration_ms", &mid).Error; err != nil {
			return nil, fmt.Errorf("tool usage median for %q: %w", a.ToolName, err)
		}
		if len(mid) == 0 {
			continue // rows removed between the two queries
		}
		var sum int64
		for _, v := range mid {
			sum += v
		}
		out = append(out, ToolStats{
			ToolName: a.ToolName, Calls: a.Calls, Errors: a.Errors,
			MinMs: a.MinMs, MaxMs: a.MaxMs, MeanMs: a.MeanMs,
			MedianMs: float64(sum) / float64(len(mid)),
		})
	}
	return out, nil
}
