package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

// ToolResultSidecarRepo inserts and queries llmd_tool_result_sidecar rows.
type ToolResultSidecarRepo struct{ db *gorm.DB }

func NewToolResultSidecarRepo(db *gorm.DB) *ToolResultSidecarRepo {
	if db == nil {
		return nil
	}
	return &ToolResultSidecarRepo{db: db}
}

// Insert writes one sidecar row. Non-fatal on duplicate tool_call_id.
func (r *ToolResultSidecarRepo) Insert(ctx context.Context, row ToolResultSidecarRow) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.WithContext(ctx).Create(&row).Error
}

// GetByToolCallID returns the sidecar for one tool call, or (zero, nil) when not found.
func (r *ToolResultSidecarRepo) GetByToolCallID(ctx context.Context, sc serverutil.Scope, sessionID, toolCallID string) (ToolResultSidecarRow, error) {
	if r == nil || r.db == nil {
		return ToolResultSidecarRow{}, nil
	}
	var row ToolResultSidecarRow
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND tool_call_id = ?",
			sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID), strings.TrimSpace(toolCallID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ToolResultSidecarRow{}, nil
	}
	return row, err
}

// SessionCompressionSavings aggregates compression stats for one session
// directly from the sidecar rows (ground truth — populated whenever a tool
// result was actually compressed).
type SessionCompressionSavings struct {
	CompressedCount int64
	OriginalTokens  int64
	WireTokens      int64
}

// SumSavingsBySession sums original/wire token totals over all compressed
// sidecar rows for a session. Returns zero-valued struct when none exist.
func (r *ToolResultSidecarRepo) SumSavingsBySession(ctx context.Context, sc serverutil.Scope, sessionID string) (SessionCompressionSavings, error) {
	if r == nil || r.db == nil {
		return SessionCompressionSavings{}, nil
	}
	var out SessionCompressionSavings
	err := r.db.WithContext(ctx).
		Model(&ToolResultSidecarRow{}).
		Select("COUNT(*) AS compressed_count, COALESCE(SUM(original_tokens),0) AS original_tokens, COALESCE(SUM(wire_tokens),0) AS wire_tokens").
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND compressed = ?",
			sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID), true).
		Scan(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SessionCompressionSavings{}, nil
	}
	return out, err
}

// savingsSelect aggregates token totals over EVERY sidecar row (so anchor rows
// and other no-win results that were still sent count toward the "would have
// sent" total), while counting only rows that actually shrank as compressed.
// Without this, totals reflect only the dramatically-compressed subset and the
// "sent X instead of Y" ratio is wildly overstated.
const savingsSelect = "COUNT(CASE WHEN compressed THEN 1 END) AS compressed_count, " +
	"COALESCE(SUM(original_tokens),0) AS original_tokens, " +
	"COALESCE(SUM(wire_tokens),0) AS wire_tokens"

// SumSavingsScope sums original/wire token totals over ALL sidecar rows in one
// scope (spans every session in it). Powers the per-project "tokens saved"
// status indicator. Zero-valued when none exist.
func (r *ToolResultSidecarRepo) SumSavingsScope(ctx context.Context, sc serverutil.Scope) (SessionCompressionSavings, error) {
	return r.sumSavings(ctx, scoped(r.dbOrNil(ctx), sc))
}

// SumSavingsTenant sums sidecar savings across every scope of one tenant.
// Powers the "all projects" rollup on overview surfaces. Always bounded by the
// tenant: an empty tenantID returns zero rather than scanning every row.
func (r *ToolResultSidecarRepo) SumSavingsTenant(ctx context.Context, tenantID string) (SessionCompressionSavings, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return SessionCompressionSavings{}, nil
	}
	return r.sumSavings(ctx, r.dbOrNil(ctx).Where("tenant_id = ?", tenantID))
}

// dbOrNil returns a context-bound handle, or nil when the repo is unconfigured.
func (r *ToolResultSidecarRepo) dbOrNil(ctx context.Context) *gorm.DB {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.WithContext(ctx)
}

// sumSavings aggregates sidecar savings over q, which the caller has already
// bounded by tenant (and optionally scope). A nil q yields zero savings.
func (r *ToolResultSidecarRepo) sumSavings(_ context.Context, q *gorm.DB) (SessionCompressionSavings, error) {
	if q == nil {
		return SessionCompressionSavings{}, nil
	}
	var out SessionCompressionSavings
	err := q.Model(&ToolResultSidecarRow{}).
		Select(savingsSelect).
		Scan(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SessionCompressionSavings{}, nil
	}
	return out, err
}

// SavingsBucket is one time bucket of compression savings (input side).
// Bucket is the UTC hour truncated to the top of the hour; tokens are summed
// over that hour.
type SavingsBucket struct {
	Bucket         time.Time
	OriginalTokens int64
	WireTokens     int64
}

// ListSavingsBuckets returns hourly compression-savings buckets ordered ascending.
// Pass a tenant-only scope for the overview. An empty scope returns no buckets;
// this never reads every tenant's rows. Buckets are
// computed in Go (truncate created_at to the UTC hour) to stay portable across
// SQLite and Postgres rather than depend on date_trunc/strftime.
func (r *ToolResultSidecarRepo) ListSavingsBuckets(ctx context.Context, sc serverutil.Scope) ([]SavingsBucket, error) {
	if r == nil || r.db == nil || sc.TenantID == "" {
		return nil, nil
	}
	type row struct {
		CreatedAt      time.Time
		OriginalTokens int64
		WireTokens     int64
	}
	q := r.db.WithContext(ctx).
		Model(&ToolResultSidecarRow{}).
		Select("created_at, original_tokens, wire_tokens").
		Where("tenant_id = ? AND scope_id = ?", sc.TenantID, sc.ScopeID)
	var rows []row
	if err := q.Order("created_at ASC").Scan(&rows).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	byHour := map[time.Time]*SavingsBucket{}
	order := []time.Time{}
	for _, rw := range rows {
		hour := rw.CreatedAt.UTC().Truncate(time.Hour)
		b, ok := byHour[hour]
		if !ok {
			b = &SavingsBucket{Bucket: hour}
			byHour[hour] = b
			order = append(order, hour)
		}
		b.OriginalTokens += rw.OriginalTokens
		b.WireTokens += rw.WireTokens
	}
	out := make([]SavingsBucket, 0, len(order))
	for _, hour := range order {
		out = append(out, *byHour[hour])
	}
	return out, nil
}

// DeleteBySession deletes all sidecar rows for a session (cascade on session delete).
func (r *ToolResultSidecarRepo) DeleteBySession(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?",
			sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID)).
		Delete(&ToolResultSidecarRow{}).Error
}
