package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/agentprofile"
	"github.com/johnewart/pyxis/internal/serverutil"
)

// AgentProfileOverlayRow persists one workspace's overlay on one static
// agent profile. The overlay is stored as its protojson encoding: the
// llm.v1.AgentProfileOverlay message is the typed contract, and the row only
// keys it by (workspace, base profile id).
type AgentProfileOverlayRow struct {
	TenantID    string `gorm:"column:tenant_id;primaryKey;size:256"`
	ScopeID     string `gorm:"column:scope_id;primaryKey;size:256;default:''"`
	BaseID      string `gorm:"column:base_id;primaryKey;size:256"`
	OverlayJSON string `gorm:"column:overlay_json;type:text;not null"`
	UpdatedAt   time.Time
}

func (AgentProfileOverlayRow) TableName() string { return "llmd_agent_profile_overlays" }

// AgentProfileOverlayRepo stores overlays. It satisfies
// agentprofile.OverlayStore and adds Set and Delete.
type AgentProfileOverlayRepo struct {
	db *gorm.DB
}

var _ agentprofile.OverlayStore = (*AgentProfileOverlayRepo)(nil)

// NewAgentProfileOverlayRepo returns a repo backed by db, or nil if db is nil.
func NewAgentProfileOverlayRepo(db *gorm.DB) *AgentProfileOverlayRepo {
	if db == nil {
		return nil
	}
	return &AgentProfileOverlayRepo{db: db}
}

func (r *AgentProfileOverlayRepo) ready(sc serverutil.Scope) error {
	if r == nil || r.db == nil {
		return errors.New("agent profile overlay repo not configured")
	}
	if strings.TrimSpace(sc.TenantID) == "" {
		return errors.New("agent profile overlay: workspace is required")
	}
	return nil
}

// Get returns the overlay for baseID, or (nil, nil) when there is none.
func (r *AgentProfileOverlayRepo) Get(ctx context.Context, sc serverutil.Scope, baseID string) (*llmv1.AgentProfileOverlay, error) {
	if err := r.ready(sc); err != nil {
		return nil, err
	}
	baseID = strings.TrimSpace(baseID)
	var row AgentProfileOverlayRow
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND base_id = ?", sc.TenantID, sc.ScopeID, baseID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agent profile overlay %q in %s: get: %w", baseID, sc, err)
	}
	return rowToOverlay(&row)
}

// List returns every overlay in the scope, ordered by base id.
func (r *AgentProfileOverlayRepo) List(ctx context.Context, sc serverutil.Scope) ([]*llmv1.AgentProfileOverlay, error) {
	if err := r.ready(sc); err != nil {
		return nil, err
	}
	var rows []AgentProfileOverlayRow
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ?", sc.TenantID, sc.ScopeID).
		Order("base_id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("agent profile overlays in %s: list: %w", sc, err)
	}
	out := make([]*llmv1.AgentProfileOverlay, 0, len(rows))
	for i := range rows {
		ov, err := rowToOverlay(&rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, ov)
	}
	return out, nil
}

// Set stores ov (replacing any overlay for ov.base_id) and returns the stored
// copy with updated_at filled in.
func (r *AgentProfileOverlayRepo) Set(ctx context.Context, sc serverutil.Scope, ov *llmv1.AgentProfileOverlay) (*llmv1.AgentProfileOverlay, error) {
	if err := r.ready(sc); err != nil {
		return nil, err
	}
	if ov == nil || strings.TrimSpace(ov.GetBaseId()) == "" {
		return nil, errors.New("agent profile overlay: base_id is required")
	}
	stored := proto.Clone(ov).(*llmv1.AgentProfileOverlay)
	stored.BaseId = strings.TrimSpace(stored.GetBaseId())
	now := time.Now().UTC()
	stored.UpdatedAt = now.Format(time.RFC3339)
	body, err := protojson.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("agent profile overlay %q in %s: encode: %w", stored.BaseId, sc, err)
	}
	row := AgentProfileOverlayRow{
		TenantID: sc.TenantID, ScopeID: sc.ScopeID, BaseID: stored.BaseId,
		OverlayJSON: string(body), UpdatedAt: now,
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "scope_id"}, {Name: "base_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"overlay_json", "updated_at"}),
	}).Create(&row).Error; err != nil {
		return nil, fmt.Errorf("agent profile overlay %q in %s: upsert: %w", stored.BaseId, sc, err)
	}
	return stored, nil
}

// Delete removes the overlay for baseID, reporting whether one existed.
func (r *AgentProfileOverlayRepo) Delete(ctx context.Context, sc serverutil.Scope, baseID string) (bool, error) {
	if err := r.ready(sc); err != nil {
		return false, err
	}
	baseID = strings.TrimSpace(baseID)
	res := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND base_id = ?", sc.TenantID, sc.ScopeID, baseID).
		Delete(&AgentProfileOverlayRow{})
	if res.Error != nil {
		return false, fmt.Errorf("agent profile overlay %q in %s: delete: %w", baseID, sc, res.Error)
	}
	return res.RowsAffected > 0, nil
}

func rowToOverlay(row *AgentProfileOverlayRow) (*llmv1.AgentProfileOverlay, error) {
	ov := &llmv1.AgentProfileOverlay{}
	if err := protojson.Unmarshal([]byte(row.OverlayJSON), ov); err != nil {
		return nil, fmt.Errorf("agent profile overlay %q: decode: %w", row.BaseID, err)
	}
	ov.BaseId = row.BaseID
	return ov, nil
}
