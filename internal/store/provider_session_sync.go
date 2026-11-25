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

// ProviderSessionSyncRow tracks how much transcript a stateful backend (e.g. Cursor CLI --resume) has
// consumed for one (tenant, scope, chat session, LLM provider) key.
type ProviderSessionSyncRow struct {
	TenantID           string `gorm:"column:tenant_id;primaryKey;size:256;not null"`
	ScopeID            string `gorm:"column:scope_id;primaryKey;size:256;not null;default:''"`
	SessionID          string `gorm:"primaryKey;size:256;not null"`
	ProviderID         string `gorm:"primaryKey;size:128;not null"`
	AgentID            string `gorm:"size:128;not null;default:''"`
	BackendSessionID   string `gorm:"size:256"`
	SystemFingerprint  string `gorm:"size:128;not null"` // sha256 hex over ordered system message contents
	SyncedMessageCount int    `gorm:"not null"`
	UpdatedAt          time.Time
}

func (ProviderSessionSyncRow) TableName() string { return "llmd_provider_session_sync" }

// ProviderSessionSync is a resolved row for application logic.
type ProviderSessionSync struct {
	Scope              serverutil.Scope
	AgentID            string
	SessionID          string
	ProviderID         string
	BackendSessionID   string
	SystemFingerprint  string
	SyncedMessageCount int
}

// ProviderSessionSyncRepo loads and updates provider sync watermarks.
type ProviderSessionSyncRepo struct {
	db *gorm.DB
}

func NewProviderSessionSyncRepo(db *gorm.DB) *ProviderSessionSyncRepo {
	if db == nil {
		return nil
	}
	return &ProviderSessionSyncRepo{db: db}
}

func (r *ProviderSessionSyncRepo) Get(ctx context.Context, sc serverutil.Scope, agentID, sessionID, providerID string) (*ProviderSessionSync, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	uid := strings.TrimSpace(sc.TenantID)
	aid := strings.TrimSpace(agentID)
	sid := strings.TrimSpace(sessionID)
	pid := strings.TrimSpace(providerID)
	if uid == "" || aid == "" || sid == "" || pid == "" {
		return nil, nil
	}
	var row ProviderSessionSyncRow
	err := scoped(r.db.WithContext(ctx), sc).Where(
		"session_id = ? AND provider_id = ?",
		sid, pid,
	).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ProviderSessionSync{
		Scope:              serverutil.Scope{TenantID: row.TenantID, ScopeID: row.ScopeID},
		AgentID:            row.AgentID,
		SessionID:          row.SessionID,
		ProviderID:         row.ProviderID,
		BackendSessionID:   strings.TrimSpace(row.BackendSessionID),
		SystemFingerprint:  strings.TrimSpace(row.SystemFingerprint),
		SyncedMessageCount: row.SyncedMessageCount,
	}, nil
}

func (r *ProviderSessionSyncRepo) Upsert(ctx context.Context, s *ProviderSessionSync) error {
	if r == nil || r.db == nil || s == nil {
		return nil
	}
	uid := strings.TrimSpace(s.Scope.TenantID)
	aid := strings.TrimSpace(s.AgentID)
	sid := strings.TrimSpace(s.SessionID)
	pid := strings.TrimSpace(s.ProviderID)
	if uid == "" || aid == "" || sid == "" || pid == "" {
		return nil
	}
	row := ProviderSessionSyncRow{
		TenantID:           uid,
		ScopeID:            s.Scope.ScopeID,
		AgentID:            sid,
		SessionID:          sid,
		ProviderID:         pid,
		BackendSessionID:   strings.TrimSpace(s.BackendSessionID),
		SystemFingerprint:  strings.TrimSpace(s.SystemFingerprint),
		SyncedMessageCount: s.SyncedMessageCount,
		UpdatedAt:          time.Now().UTC(),
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "scope_id"},
			{Name: "session_id"},
			{Name: "provider_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"agent_id",
			"backend_session_id",
			"system_fingerprint",
			"synced_message_count",
			"updated_at",
		}),
	}).Create(&row).Error
}

// DeleteForSession removes provider sync watermarks for one chat session (e.g. after transcript truncate).
func (r *ProviderSessionSyncRepo) DeleteForSession(ctx context.Context, sc serverutil.Scope, agentID, sessionID string) error {
	if r == nil || r.db == nil {
		return nil
	}
	uid := strings.TrimSpace(sc.TenantID)
	aid := strings.TrimSpace(agentID)
	sid := strings.TrimSpace(sessionID)
	if uid == "" || aid == "" || sid == "" {
		return nil
	}
	return scoped(r.db.WithContext(ctx), sc).Where(
		"session_id = ?", sid,
	).Delete(&ProviderSessionSyncRow{}).Error
}
