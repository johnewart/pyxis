package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TurnMetadataRepo upserts and queries llmd_turn_metadata rows.
type TurnMetadataRepo struct{ db *gorm.DB }

func NewTurnMetadataRepo(db *gorm.DB) *TurnMetadataRepo {
	if db == nil {
		return nil
	}
	return &TurnMetadataRepo{db: db}
}

// Upsert writes a row, updating all non-PK columns on conflict.
func (r *TurnMetadataRepo) Upsert(ctx context.Context, row TurnMetadataRow) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&row).Error
}

// GetByTurn returns the metadata row for one turn (zero-value + nil when not found).
func (r *TurnMetadataRepo) GetByTurn(ctx context.Context, sc serverutil.Scope, sessionID, turnID string) (TurnMetadataRow, error) {
	if r == nil || r.db == nil {
		return TurnMetadataRow{}, nil
	}
	var row TurnMetadataRow
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND turn_id = ?", sc.TenantID, sc.ScopeID, sessionID, turnID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TurnMetadataRow{}, nil
	}
	return row, err
}

// SessionTotals aggregates token counts across all turns for a session.
type SessionTotals struct {
	TotalProviderIn  int64
	TotalProviderOut int64
	TotalCacheRead   int64
	TotalCacheWrite  int64
	TotalOriginal    int64
	TotalCompressed  int64
}

// GetSessionTotals returns summed token counts for a session.
func (r *TurnMetadataRepo) GetSessionTotals(ctx context.Context, sc serverutil.Scope, sessionID string) (SessionTotals, error) {
	if r == nil || r.db == nil {
		return SessionTotals{}, nil
	}
	var row struct {
		ProvIn, ProvOut, Orig, Comp *int64
	}
	err := r.db.WithContext(ctx).Model(&TurnMetadataRow{}).
		Select("SUM(provider_in_toks) as prov_in, SUM(provider_out_toks) as prov_out, "+
			"SUM(original_tokens) as orig, SUM(compressed_tokens) as comp").
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sessionID).
		Scan(&row).Error
	deref := func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	}
	return SessionTotals{
		TotalProviderIn:  deref(row.ProvIn),
		TotalProviderOut: deref(row.ProvOut),
		TotalOriginal:    deref(row.Orig),
		TotalCompressed:  deref(row.Comp),
	}, err
}

// GetScopeTotals returns summed token counts across ALL sessions in one
// scope. Powers the per-project token rollup.
func (r *TurnMetadataRepo) GetScopeTotals(ctx context.Context, sc serverutil.Scope) (SessionTotals, error) {
	if strings.TrimSpace(sc.TenantID) == "" || r == nil || r.db == nil {
		return SessionTotals{}, nil
	}
	return r.sumTotals(scoped(r.db.WithContext(ctx), sc))
}

// GetTenantTotals returns summed token counts across every scope of one
// tenant. Powers the "all projects" overview rollup. It is always bounded by
// the tenant: an empty tenantID returns zero totals rather than scanning
// every row.
func (r *TurnMetadataRepo) GetTenantTotals(ctx context.Context, tenantID string) (SessionTotals, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || r == nil || r.db == nil {
		return SessionTotals{}, nil
	}
	return r.sumTotals(r.db.WithContext(ctx).Where("tenant_id = ?", tenantID))
}

// sumTotals aggregates turn-metadata token counts over q, which the caller
// has already bounded by tenant (and optionally scope).
func (r *TurnMetadataRepo) sumTotals(q *gorm.DB) (SessionTotals, error) {
	var row struct {
		ProvIn, ProvOut, CacheRead, CacheWrite, Orig, Comp *int64
	}
	q = q.Model(&TurnMetadataRow{}).
		Select("SUM(provider_in_toks) as prov_in, SUM(provider_out_toks) as prov_out, " +
			"SUM(provider_cache_read_toks) as cache_read, SUM(provider_cache_write_toks) as cache_write, " +
			"SUM(original_tokens) as orig, SUM(compressed_tokens) as comp")
	err := q.Scan(&row).Error
	deref := func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	}
	return SessionTotals{
		TotalProviderIn:  deref(row.ProvIn),
		TotalProviderOut: deref(row.ProvOut),
		TotalCacheRead:   deref(row.CacheRead),
		TotalCacheWrite:  deref(row.CacheWrite),
		TotalOriginal:    deref(row.Orig),
		TotalCompressed:  deref(row.Comp),
	}, err
}

// UsageBucket is one hourly bucket of actual provider token usage (measured).
type UsageBucket struct {
	Bucket          time.Time
	ProviderInToks  int64
	ProviderOutToks int64
	CacheReadToks   int64
	CacheWriteToks  int64
}

// ListUsageBuckets returns hourly provider-token usage buckets ordered ascending.
// Pass a tenant-only scope for the overview. An empty scope returns no buckets;
// this never reads every tenant's rows. Buckets are truncated to
// the UTC hour in Go to stay portable across SQLite/Postgres; callers may
// re-aggregate to coarser granularity (6h/day/week) based on the overall span.
func (r *TurnMetadataRepo) ListUsageBuckets(ctx context.Context, sc serverutil.Scope) ([]UsageBucket, error) {
	if r == nil || r.db == nil || sc.TenantID == "" {
		return nil, nil
	}
	type row struct {
		CreatedAt  time.Time
		ProvIn     *int64 `gorm:"column:provider_in_toks"`
		ProvOut    *int64 `gorm:"column:provider_out_toks"`
		CacheRead  *int64 `gorm:"column:provider_cache_read_toks"`
		CacheWrite *int64 `gorm:"column:provider_cache_write_toks"`
	}
	q := r.db.WithContext(ctx).Model(&TurnMetadataRow{}).
		Select("created_at, provider_in_toks, provider_out_toks, provider_cache_read_toks, provider_cache_write_toks").
		Where("tenant_id = ? AND scope_id = ?", sc.TenantID, sc.ScopeID)
	var rows []row
	if err := q.Order("created_at ASC").Scan(&rows).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	deref := func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	}
	byHour := map[time.Time]*UsageBucket{}
	order := []time.Time{}
	for _, rw := range rows {
		h := rw.CreatedAt.UTC().Truncate(time.Hour)
		b, ok := byHour[h]
		if !ok {
			b = &UsageBucket{Bucket: h}
			byHour[h] = b
			order = append(order, h)
		}
		b.ProviderInToks += deref(rw.ProvIn)
		b.ProviderOutToks += deref(rw.ProvOut)
		b.CacheReadToks += deref(rw.CacheRead)
		b.CacheWriteToks += deref(rw.CacheWrite)
	}
	out := make([]UsageBucket, 0, len(order))
	for _, h := range order {
		out = append(out, *byHour[h])
	}
	return out, nil
}

// ListBySession returns all turn metadata rows for a session ordered by creation time.
func (r *TurnMetadataRepo) ListBySession(ctx context.Context, sc serverutil.Scope, sessionID string) ([]TurnMetadataRow, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	var rows []TurnMetadataRow
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sessionID).
		Order("created_at ASC").
		Find(&rows).Error
	return rows, err
}

// MessageTurnSeqs maps assistant message_id -> turn_seq for a session, read from
// llmd_chat_messages (the canonical per-row turn stamp). Lets the turn breakdown
// carry turn_seq so callers can join graph decorations (keyed by turn_seq) onto
// the same bubble as the token badge. Rows with NULL/0 turn_seq are skipped.
func (r *TurnMetadataRepo) MessageTurnSeqs(ctx context.Context, sc serverutil.Scope, sessionID string) (map[string]int64, error) {
	out := map[string]int64{}
	if r == nil || r.db == nil {
		return out, nil
	}
	type row struct {
		MessageID string `gorm:"column:message_id"`
		TurnSeq   int64  `gorm:"column:turn_seq"`
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Table("llmd_chat_messages").
		Select("message_id, MAX(turn_seq) AS turn_seq").
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND message_id != '' AND turn_seq IS NOT NULL", sc.TenantID, sc.ScopeID, sessionID).
		Group("message_id").
		Scan(&rows).Error
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		if r.TurnSeq > 0 {
			out[r.MessageID] = r.TurnSeq
		}
	}
	return out, nil
}

// MessageUsageEntry pairs a persisted assistant message id with its token counts.
type MessageUsageEntry struct {
	MessageID              string
	ProviderInToks         int64
	ProviderOutToks        int64
	ProviderCacheReadToks  int64
	ProviderCacheWriteToks int64
	// Compression: non-zero only when tool-result compression fired this turn.
	OriginalTokens   int64
	CompressedTokens int64
	// ModelDurationMs is the model call's wall time; nil when not recorded.
	ModelDurationMs *int64
}

// ListMessageUsage returns per-turn token counts joined with the message_id from
// llmd_chat_messages. It does NOT require an in-memory agent — reads DB only.
//
// Join strategy: prefer tm.message_id (populated for turns recorded after the
// message_id column was added). Fall back to the old turn_id join for legacy rows
// that pre-date the column. A UNION covers both so old and new rows work.
func (r *TurnMetadataRepo) ListMessageUsage(ctx context.Context, sc serverutil.Scope, sessionID string) ([]MessageUsageEntry, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	type raw struct {
		MessageID              string    `gorm:"column:message_id"`
		ProviderInToks         *int64    `gorm:"column:provider_in_toks"`
		ProviderOutToks        *int64    `gorm:"column:provider_out_toks"`
		ProviderCacheReadToks  *int64    `gorm:"column:provider_cache_read_toks"`
		ProviderCacheWriteToks *int64    `gorm:"column:provider_cache_write_toks"`
		OriginalTokens         *int64    `gorm:"column:original_tokens"`
		CompressedTokens       *int64    `gorm:"column:compressed_tokens"`
		ModelDurationMs        *int64    `gorm:"column:model_duration_ms"`
		CreatedAt              time.Time `gorm:"column:created_at"`
	}
	var rows []raw
	// Wrap the UNION in a subquery so we can ORDER BY created_at across both
	// branches. Without ordering, SQLite returns UNION rows in an unspecified
	// order; the frontend takes turns[last] as the current context size, so a
	// non-deterministic order would show stale data from an earlier turn.
	err := r.db.WithContext(ctx).Raw(`
		SELECT u.message_id,
		       u.provider_in_toks,
		       u.provider_out_toks,
		       u.provider_cache_read_toks,
		       u.provider_cache_write_toks,
		       u.original_tokens,
		       u.compressed_tokens,
		       u.model_duration_ms
		FROM (
			SELECT tm.message_id,
			       tm.provider_in_toks,
			       tm.provider_out_toks,
			       tm.provider_cache_read_toks,
			       tm.provider_cache_write_toks,
			       tm.original_tokens,
			       tm.compressed_tokens,
			       tm.model_duration_ms,
			       tm.created_at
			FROM  llmd_turn_metadata tm
			WHERE tm.tenant_id = ? AND tm.scope_id = ?
			  AND tm.session_id  = ?
			  AND tm.message_id != ''
			  AND (tm.provider_in_toks IS NOT NULL OR tm.provider_out_toks IS NOT NULL
			       OR tm.provider_cache_read_toks IS NOT NULL OR tm.provider_cache_write_toks IS NOT NULL)
			UNION
			SELECT DISTINCT cm.message_id,
			                tm2.provider_in_toks,
			                tm2.provider_out_toks,
			                tm2.provider_cache_read_toks,
			                tm2.provider_cache_write_toks,
			                tm2.original_tokens,
			                tm2.compressed_tokens,
			                tm2.model_duration_ms,
			                tm2.created_at
			FROM  llmd_turn_metadata   tm2
			JOIN  llmd_chat_messages   cm
			  ON  cm.tenant_id   = tm2.tenant_id
			 AND  cm.scope_id    = tm2.scope_id
			 AND  cm.session_id  = tm2.session_id
			 AND  cm.turn_id     = tm2.turn_id
			WHERE tm2.tenant_id = ? AND tm2.scope_id = ?
			  AND tm2.session_id  = ?
			  AND (tm2.message_id IS NULL OR tm2.message_id = '')
			  AND cm.kind IN ('assistant','thinking','activity','tool')
			  AND (tm2.provider_in_toks IS NOT NULL OR tm2.provider_out_toks IS NOT NULL
			       OR tm2.provider_cache_read_toks IS NOT NULL OR tm2.provider_cache_write_toks IS NOT NULL)
		) u
		ORDER BY u.created_at ASC
	`, sc.TenantID, sc.ScopeID, sessionID, sc.TenantID, sc.ScopeID, sessionID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	deref := func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	}
	out := make([]MessageUsageEntry, 0, len(rows))
	for _, r := range rows {
		if r.MessageID == "" {
			continue
		}
		out = append(out, MessageUsageEntry{
			MessageID:              r.MessageID,
			ProviderInToks:         deref(r.ProviderInToks),
			ProviderOutToks:        deref(r.ProviderOutToks),
			ProviderCacheReadToks:  deref(r.ProviderCacheReadToks),
			ProviderCacheWriteToks: deref(r.ProviderCacheWriteToks),
			OriginalTokens:         deref(r.OriginalTokens),
			CompressedTokens:       deref(r.CompressedTokens),
			ModelDurationMs:        copyInt64(r.ModelDurationMs),
		})
	}
	return out, nil
}

// DeleteBySession deletes all turn_metadata rows for a session (cascade on session delete).
func (r *TurnMetadataRepo) DeleteBySession(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sessionID).
		Delete(&TurnMetadataRow{}).Error
}
