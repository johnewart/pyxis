package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// UsageRecordRow is one recorded LLM call's usage (llmd_usage).
type UsageRecordRow struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement"`
	TenantID         string    `gorm:"column:tenant_id;index:idx_usage_tenant;size:256;not null"`
	ScopeID          string    `gorm:"column:scope_id;index:idx_usage_scope;size:256;not null;default:''"`
	Principal        string    `gorm:"column:principal;size:256;not null;default:''"`
	ClientID         string    `gorm:"column:client_id;size:256;not null;default:''"`
	ProviderID       string    `gorm:"column:provider_id;size:256;not null;default:''"`
	ProviderType     string    `gorm:"column:provider_type;size:64;not null;default:''"`
	Model            string    `gorm:"column:model;size:256;not null;default:''"`
	ServedBy         string    `gorm:"column:served_by;size:256;not null;default:''"`
	RPC              string    `gorm:"column:rpc;size:128;not null;default:''"`
	DurationMs       int64     `gorm:"column:duration_ms;not null;default:0"`
	Outcome          string    `gorm:"column:outcome;size:64;not null;default:''"`
	CreatedAt        time.Time `gorm:"column:created_at;index:idx_usage_created_at;not null"`
	InputTokens      int64     `gorm:"column:input_tokens;not null;default:0"`
	OutputTokens     int64     `gorm:"column:output_tokens;not null;default:0"`
	CacheReadTokens  int64     `gorm:"column:cache_read_tokens;not null;default:0"`
	CacheWriteTokens int64     `gorm:"column:cache_write_tokens;not null;default:0"`
	ReasoningTokens  int64     `gorm:"column:reasoning_tokens;not null;default:0"`
	CostUSD          float64   `gorm:"column:cost_usd;not null;default:0"`
}

func (UsageRecordRow) TableName() string { return "llmd_usage" }

// UsageFilter filters usage rows. Empty strings mean no filter on that field;
// nil Since/Until mean unbounded. Since is inclusive, Until is exclusive.
type UsageFilter struct {
	TenantID string
	ScopeID  string
	Model    string
	Since    *time.Time
	Until    *time.Time
}

// UsageTotals is the summed usage for one (tenant, scope, model).
type UsageTotals struct {
	TenantID         string
	ScopeID          string
	Model            string
	Calls            int64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	CostUSD          float64
}

// UsageRepo persists and queries usage.
type UsageRepo struct {
	db *gorm.DB
}

// NewUsageRepo returns nil when db is nil (persistence disabled).
func NewUsageRepo(db *gorm.DB) *UsageRepo {
	if db == nil {
		return nil
	}
	return &UsageRepo{db: db}
}

// RecordUsage writes one usage row. CreatedAt defaults to now when zero.
// No-op for a nil repo.
func (r *UsageRepo) RecordUsage(ctx context.Context, row UsageRecordRow) error {
	if r == nil || r.db == nil {
		return nil
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("record usage: %w", err)
	}
	return nil
}

func (r *UsageRepo) filtered(ctx context.Context, f UsageFilter) *gorm.DB {
	tx := r.db.WithContext(ctx).Model(&UsageRecordRow{})
	if f.TenantID != "" {
		tx = tx.Where("tenant_id = ?", f.TenantID)
	}
	if f.ScopeID != "" {
		tx = tx.Where("scope_id = ?", f.ScopeID)
	}
	if f.Model != "" {
		tx = tx.Where("model = ?", f.Model)
	}
	if f.Since != nil {
		tx = tx.Where("created_at >= ?", *f.Since)
	}
	if f.Until != nil {
		tx = tx.Where("created_at < ?", *f.Until)
	}
	return tx
}

// QueryUsage returns matching usage rows (oldest first) and the sums grouped
// by (tenant, scope, model) over the same rows.
func (r *UsageRepo) QueryUsage(ctx context.Context, f UsageFilter) ([]UsageRecordRow, []UsageTotals, error) {
	if r == nil || r.db == nil {
		return nil, nil, nil
	}
	var rows []UsageRecordRow
	if err := r.filtered(ctx, f).Order("created_at ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("query usage rows: %w", err)
	}
	var totals []UsageTotals
	err := r.filtered(ctx, f).
		Select("tenant_id, scope_id, model, COUNT(*) AS calls, " +
			"SUM(input_tokens) AS input_tokens, SUM(output_tokens) AS output_tokens, " +
			"SUM(cache_read_tokens) AS cache_read_tokens, SUM(cache_write_tokens) AS cache_write_tokens, " +
			"SUM(reasoning_tokens) AS reasoning_tokens, SUM(cost_usd) AS cost_usd").
		Group("tenant_id, scope_id, model").
		Order("tenant_id, scope_id, model").
		Scan(&totals).Error
	if err != nil {
		return nil, nil, fmt.Errorf("aggregate usage: %w", err)
	}
	return rows, totals, nil
}
