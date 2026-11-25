package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/johnewart/pyxis/internal/agentprofile"
	"github.com/johnewart/pyxis/internal/serverutil"
)

// ErrAgentProfileNameTaken is returned by Create/Update when another profile
// in the same workspace already carries the requested name.
//
// It is produced by an explicit pre-check rather than by translating the
// driver's unique-violation error: gorm.Config in open.go does not enable
// TranslateError, so gorm.ErrDuplicatedKey is never surfaced. The unique
// index on (workspace_id, name) remains as a backstop; a raw insert error is
// still wrapped and returned.
var ErrAgentProfileNameTaken = errors.New("agent profile name already taken in workspace")

// AgentProfileRepo is the store-backed (dynamic) agent profile tier. It
// satisfies agentprofile.DynamicStore and adds the write operations the CRUD
// RPCs need.
type AgentProfileRepo struct {
	db *gorm.DB
}

var _ agentprofile.DynamicStore = (*AgentProfileRepo)(nil)

// NewAgentProfileRepo returns a repo backed by db, or nil if db is nil.
func NewAgentProfileRepo(db *gorm.DB) *AgentProfileRepo {
	if db == nil {
		return nil
	}
	return &AgentProfileRepo{db: db}
}

func (r *AgentProfileRepo) ready() error {
	if r == nil || r.db == nil {
		return errors.New("agent profile repo not configured")
	}
	return nil
}

// nameTaken reports whether a profile other than excludeID already uses name
// within scope sc.
func (r *AgentProfileRepo) nameTaken(ctx context.Context, sc serverutil.Scope, name, excludeID string) (bool, error) {
	q := r.db.WithContext(ctx).Model(&AgentProfileRow{}).
		Where("tenant_id = ? AND scope_id = ? AND name = ?", sc.TenantID, sc.ScopeID, name)
	if excludeID != "" {
		q = q.Where("id <> ?", excludeID)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return false, fmt.Errorf("agent profile %q in %s: check name: %w", name, sc, err)
	}
	return n > 0, nil
}

// Create inserts p into scope sc. A blank p.ID is replaced with a fresh
// uuid, written back into p. Timestamps are set to now (UTC) and written back
// as well.
func (r *AgentProfileRepo) Create(ctx context.Context, sc serverutil.Scope, p *agentprofile.Profile) error {
	if err := r.ready(); err != nil {
		return err
	}
	if p == nil {
		return errors.New("agent profile: nil profile")
	}
	if sc.TenantID == "" {
		return errors.New("agent profile: workspace is required")
	}
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return fmt.Errorf("agent profile %q in %s: name is required", p.ID, sc)
	}
	taken, err := r.nameTaken(ctx, sc, p.Name, "")
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("agent profile %q in %s: %w", p.Name, sc, ErrAgentProfileNameTaken)
	}
	now := time.Now().UTC()
	p.CreatedAt, p.UpdatedAt = now, now
	p.Static = false
	row, err := profileToRow(sc, p)
	if err != nil {
		return fmt.Errorf("agent profile %q in %s: encode: %w", p.ID, sc, err)
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("agent profile %q in %s: insert: %w", p.ID, sc, err)
	}
	return nil
}

// Update replaces every mutable column of the profile identified by
// (sc, p.ID) — everything except id, workspace_id and created_at.
// A missing row yields gorm.ErrRecordNotFound; a name already used by a
// different profile in the workspace yields ErrAgentProfileNameTaken.
func (r *AgentProfileRepo) Update(ctx context.Context, sc serverutil.Scope, p *agentprofile.Profile) error {
	if err := r.ready(); err != nil {
		return err
	}
	if p == nil {
		return errors.New("agent profile: nil profile")
	}
	id := strings.TrimSpace(p.ID)
	if sc.TenantID == "" || id == "" {
		return errors.New("agent profile: workspace and id are required")
	}
	p.ID = id
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return fmt.Errorf("agent profile %q in %s: name is required", id, sc)
	}
	taken, err := r.nameTaken(ctx, sc, p.Name, id)
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("agent profile %q in %s: %w", p.Name, sc, ErrAgentProfileNameTaken)
	}
	sectionsJSON, err := MarshalSystemPromptSectionsJSON(p.SystemPromptSections)
	if err != nil {
		return fmt.Errorf("agent profile %q in %s: encode sections: %w", id, sc, err)
	}
	mappingJSON, err := MarshalToolMappingJSON(p.ToolMapping)
	if err != nil {
		return fmt.Errorf("agent profile %q in %s: encode tool mapping: %w", id, sc, err)
	}
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&AgentProfileRow{}).
		Where("tenant_id = ? AND scope_id = ? AND id = ?", sc.TenantID, sc.ScopeID, id).
		Updates(map[string]any{
			"name":                        p.Name,
			"description":                 p.Description,
			"system_prompt_sections_json": sectionsJSON,
			"llm_provider_id":             strings.TrimSpace(p.LlmProviderID),
			"default_model":               strings.TrimSpace(p.DefaultModel),
			"memory_enabled":              p.MemoryEnabled,
			"tool_mapping_json":           mappingJSON,
			"tool_policy":                 agentprofile.PolicyName(p.ToolPolicy),
			"updated_at":                  now,
		})
	if res.Error != nil {
		return fmt.Errorf("agent profile %q in %s: update: %w", id, sc, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("agent profile %q in %s: %w", id, sc, gorm.ErrRecordNotFound)
	}
	p.UpdatedAt = now
	p.Static = false
	return nil
}

// Delete removes the profile identified by (sc, id). It reports
// false, nil when no row matched.
func (r *AgentProfileRepo) Delete(ctx context.Context, sc serverutil.Scope, id string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	id = strings.TrimSpace(id)
	if sc.TenantID == "" || id == "" {
		return false, errors.New("agent profile: workspace and id are required")
	}
	res := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND id = ?", sc.TenantID, sc.ScopeID, id).
		Delete(&AgentProfileRow{})
	if res.Error != nil {
		return false, fmt.Errorf("agent profile %q in %s: delete: %w", id, sc, res.Error)
	}
	return res.RowsAffected > 0, nil
}

// Get returns the profile identified by (sc, id). A missing row
// yields an error satisfying errors.Is(err, agentprofile.ErrNotFound), per the
// DynamicStore contract.
func (r *AgentProfileRepo) Get(ctx context.Context, sc serverutil.Scope, id string) (*agentprofile.Profile, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	if sc.TenantID == "" || id == "" {
		return nil, errors.New("agent profile: workspace and id are required")
	}
	var row AgentProfileRow
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND id = ?", sc.TenantID, sc.ScopeID, id).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("agent profile %q in %s: %w", id, sc, agentprofile.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("agent profile %q in %s: get: %w", id, sc, err)
	}
	p, err := rowToProfile(&row)
	if err != nil {
		return nil, fmt.Errorf("agent profile %q in %s: decode: %w", id, sc, err)
	}
	return p, nil
}

// List returns every profile in scope sc ordered by name.
func (r *AgentProfileRepo) List(ctx context.Context, sc serverutil.Scope) ([]*agentprofile.Profile, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if sc.TenantID == "" {
		return nil, errors.New("agent profile: workspace is required")
	}
	var rows []AgentProfileRow
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ?", sc.TenantID, sc.ScopeID).
		Order("name ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("agent profiles in %s: list: %w", sc, err)
	}
	out := make([]*agentprofile.Profile, 0, len(rows))
	for i := range rows {
		p, err := rowToProfile(&rows[i])
		if err != nil {
			return nil, fmt.Errorf("agent profile %q in %s: decode: %w", rows[i].ID, sc, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// profileToRow converts an in-memory profile into its persisted row. The
// caller has already trimmed ids/name and set timestamps.
func profileToRow(sc serverutil.Scope, p *agentprofile.Profile) (AgentProfileRow, error) {
	sectionsJSON, err := MarshalSystemPromptSectionsJSON(p.SystemPromptSections)
	if err != nil {
		return AgentProfileRow{}, fmt.Errorf("encode sections: %w", err)
	}
	mappingJSON, err := MarshalToolMappingJSON(p.ToolMapping)
	if err != nil {
		return AgentProfileRow{}, fmt.Errorf("encode tool mapping: %w", err)
	}
	return AgentProfileRow{
		ID:       p.ID,
		TenantID: sc.TenantID, ScopeID: sc.ScopeID,
		Name:                     p.Name,
		Description:              p.Description,
		SystemPromptSectionsJSON: sectionsJSON,
		LlmProviderID:            strings.TrimSpace(p.LlmProviderID),
		DefaultModel:             strings.TrimSpace(p.DefaultModel),
		MemoryEnabled:            p.MemoryEnabled,
		ToolMappingJSON:          mappingJSON,
		ToolPolicy:               agentprofile.PolicyName(p.ToolPolicy),
		CreatedAt:                p.CreatedAt,
		UpdatedAt:                p.UpdatedAt,
	}, nil
}

// rowToProfile converts a persisted row into the in-memory model. Rows are
// never static.
func rowToProfile(row *AgentProfileRow) (*agentprofile.Profile, error) {
	sections, err := UnmarshalSystemPromptSectionsJSON(row.SystemPromptSectionsJSON)
	if err != nil {
		return nil, fmt.Errorf("decode sections: %w", err)
	}
	mapping, err := UnmarshalToolMappingJSON(row.ToolMappingJSON)
	if err != nil {
		return nil, fmt.Errorf("decode tool mapping: %w", err)
	}
	return &agentprofile.Profile{
		ID:                   row.ID,
		Name:                 row.Name,
		Description:          row.Description,
		SystemPromptSections: sections,
		LlmProviderID:        row.LlmProviderID,
		DefaultModel:         row.DefaultModel,
		MemoryEnabled:        row.MemoryEnabled,
		ToolMapping:          mapping,
		ToolPolicy:           agentprofile.ParsePolicyName(row.ToolPolicy),
		Static:               false,
		CreatedAt:            row.CreatedAt.UTC(),
		UpdatedAt:            row.UpdatedAt.UTC(),
	}, nil
}
