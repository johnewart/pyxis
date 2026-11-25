package store

import (
	"context"
	"strings"

	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

// TurnDecorationRepo persists typed turn/tool-result decorations
// (llmd_turn_decorations). Best-effort presentation metadata: rows are written
// from the toolsession seam and read back for display; they never influence
// prompt content.
type TurnDecorationRepo struct {
	db *gorm.DB
}

// NewTurnDecorationRepo returns nil when db is nil (persistence disabled).
func NewTurnDecorationRepo(db *gorm.DB) *TurnDecorationRepo {
	if db == nil {
		return nil
	}
	return &TurnDecorationRepo{db: db}
}

// Insert appends decoration rows. No-op for a nil repo/db or an empty batch.
func (r *TurnDecorationRepo) Insert(ctx context.Context, rows []TurnDecorationRow) error {
	if r == nil || r.db == nil || len(rows) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&rows).Error
}

// ListBySession returns a session's decorations ordered by insertion (oldest
// first, so presentation can replay them in turn order via turn_seq).
func (r *TurnDecorationRepo) ListBySession(ctx context.Context, sc serverutil.Scope, sessionID string) ([]TurnDecorationRow, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil, nil
	}
	q := scoped(r.db.WithContext(ctx), sc).Where("session_id = ?", sid)
	var out []TurnDecorationRow
	err := q.Order("id ASC").Find(&out).Error
	return out, err
}

// ListByToolCall returns the decorations attached to one tool result.
func (r *TurnDecorationRepo) ListByToolCall(ctx context.Context, sc serverutil.Scope, sessionID, toolCallID string) ([]TurnDecorationRow, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	sid := strings.TrimSpace(sessionID)
	tcid := strings.TrimSpace(toolCallID)
	if sid == "" || tcid == "" {
		return nil, nil
	}
	q := scoped(r.db.WithContext(ctx), sc).Where("session_id = ? AND tool_call_id = ?", sid, tcid)
	var out []TurnDecorationRow
	err := q.Order("id ASC").Find(&out).Error
	return out, err
}

// DeleteBySession removes all decorations for a session (cascade on session
// delete — keep in step with the sidecar/turn-metadata sweeps).
func (r *TurnDecorationRepo) DeleteBySession(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if r == nil || r.db == nil {
		return nil
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil
	}
	q := scoped(r.db.WithContext(ctx), sc).Where("session_id = ?", sid)
	return q.Delete(&TurnDecorationRow{}).Error
}
