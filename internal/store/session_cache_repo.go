package store

import (
	"context"
	"errors"
	"github.com/johnewart/pyxis/internal/serverutil"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SessionCacheRepo reads and writes the generic session-scoped K/V cache
// (llmd_session_cache). Values are opaque bytes; this layer never interprets them.
type SessionCacheRepo struct{ db *gorm.DB }

func NewSessionCacheRepo(db *gorm.DB) *SessionCacheRepo {
	if db == nil {
		return nil
	}
	return &SessionCacheRepo{db: db}
}

// Get returns (value, true, nil) when present, (nil, false, nil) when absent.
func (r *SessionCacheRepo) Get(ctx context.Context, sc serverutil.Scope, sessionID, namespace, key string) ([]byte, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, nil
	}
	var row SessionCacheRow
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND namespace = ? AND cache_key = ?",
			sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID),
			strings.TrimSpace(namespace), strings.TrimSpace(key)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return row.Value, true, nil
}

// Set upserts one entry (replacing value + updated_at on conflict).
func (r *SessionCacheRepo) Set(ctx context.Context, sc serverutil.Scope, sessionID, namespace, key string, value []byte) error {
	if r == nil || r.db == nil {
		return nil
	}
	row := SessionCacheRow{
		TenantID: sc.TenantID, ScopeID: sc.ScopeID,
		SessionID: strings.TrimSpace(sessionID),
		Namespace: strings.TrimSpace(namespace),
		CacheKey:  strings.TrimSpace(key),
		Value:     value,
		UpdatedAt: time.Now(),
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"}, {Name: "scope_id"}, {Name: "session_id"}, {Name: "namespace"}, {Name: "cache_key"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&row).Error
}

// Delete removes one key, or the whole namespace for the session when key is empty.
// Returns the number of rows affected.
func (r *SessionCacheRepo) Delete(ctx context.Context, sc serverutil.Scope, sessionID, namespace, key string) (int64, error) {
	if r == nil || r.db == nil {
		return 0, nil
	}
	q := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND namespace = ?",
			sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID), strings.TrimSpace(namespace))
	if k := strings.TrimSpace(key); k != "" {
		q = q.Where("cache_key = ?", k)
	}
	res := q.Delete(&SessionCacheRow{})
	return res.RowsAffected, res.Error
}

// DeleteBySession removes all cache entries for a session (cascade on session delete).
func (r *SessionCacheRepo) DeleteBySession(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?",
			sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID)).
		Delete(&SessionCacheRow{}).Error
}
