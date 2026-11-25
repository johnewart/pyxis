package store

import (
	"context"
	"errors"
	"github.com/johnewart/pyxis/internal/serverutil"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SessionDraftRepo stores the one unsent composer draft a session may have.
//
// There is no list operation and no history of drafts on purpose: a draft is
// current-value state, not an event log. The single row per session is the
// whole feature — it exists so that closing a laptop, reloading a tab or
// crashing a renderer does not lose what someone had typed.
type SessionDraftRepo struct {
	db *gorm.DB
}

// NewSessionDraftRepo returns a repo backed by db, or nil if db is nil.
func NewSessionDraftRepo(db *gorm.DB) *SessionDraftRepo {
	if db == nil {
		return nil
	}
	return &SessionDraftRepo{db: db}
}

// Upsert saves a session's draft, replacing any previous one.
//
// Blank content deletes the draft rather than storing an empty row: "the
// composer is empty" and "there is no draft" are the same state, and keeping
// them distinct would leave every session that was ever typed in with a
// permanent empty row that a UI then has to special-case.
func (r *SessionDraftRepo) Upsert(ctx context.Context, sc serverutil.Scope, sessionID, content string) error {
	if r == nil || r.db == nil {
		return errors.New("session draft repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return errors.New("draft: workspace and session are required")
	}
	if strings.TrimSpace(content) == "" {
		return r.Clear(ctx, sc, sid)
	}
	row := SessionDraftRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, SessionID: sid, Content: content}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "scope_id"}, {Name: "session_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"content", "updated_at"}),
	}).Create(&row).Error
}

// Get returns a session's draft content, or "" when there is none.
func (r *SessionDraftRepo) Get(ctx context.Context, sc serverutil.Scope, sessionID string) (string, error) {
	if r == nil || r.db == nil {
		return "", nil
	}
	var row SessionDraftRow
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?",
			sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.Content, nil
}

// Clear deletes a session's draft. Called when the message is actually sent —
// a draft that outlived its own send would reappear in the composer next to the
// message it became.
func (r *SessionDraftRepo) Clear(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?",
			sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID)).
		Delete(&SessionDraftRow{}).Error
}
