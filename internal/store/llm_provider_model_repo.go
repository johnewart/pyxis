package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

// ProviderModel is one cached catalog entry for a provider, as stored.
// Mirrors LlmProviderModelRow without the persistence tags so callers in
// internal/llm/providers don't import GORM row shapes.
type ProviderModel struct {
	ModelKey     string
	Label        string
	DiscoveredAt time.Time
}

// LlmProviderModelRepo persists the per-provider model catalog cache. See
// LlmProviderModelRow for why this is a cache and not a registry.
type LlmProviderModelRepo struct {
	db *gorm.DB
}

func NewLlmProviderModelRepo(db *gorm.DB) *LlmProviderModelRepo {
	return &LlmProviderModelRepo{db: db}
}

// Replace swaps a provider's entire cached catalog for models, stamping every
// row with discoveredAt. Wholesale replacement inside one transaction: a model
// the provider no longer offers must disappear, and a partially-applied scan
// would be worse than the pre-scan state.
//
// An empty models slice clears the provider's rows. Callers must NOT call this
// after a FAILED scan: a failure means "we don't know", which is not the same
// as "there are none", and clearing on failure would make one flaky network
// call erase a good catalog.
//
// The catalog is exact-scope: rows land in sc's (tenant, scope) and are never
// read through from another scope.
func (r *LlmProviderModelRepo) Replace(ctx context.Context, sc serverutil.Scope, providerID string, models []ProviderModel, discoveredAt time.Time) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("llm provider model repo not configured")
	}
	if err := requireTenant(sc); err != nil {
		return err
	}
	id := strings.TrimSpace(providerID)
	if id == "" {
		return fmt.Errorf("provider_id is required")
	}
	stamp := discoveredAt.UTC()
	rows := make([]LlmProviderModelRow, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, m := range models {
		key := strings.TrimSpace(m.ModelKey)
		if key == "" {
			continue
		}
		// The composite PK makes a duplicate key a constraint error that would
		// fail the whole scan; scans dedupe already, so drop rather than fail.
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		label := strings.TrimSpace(m.Label)
		if label == "" {
			label = key
		}
		rows = append(rows, LlmProviderModelRow{
			TenantID:     sc.TenantID,
			ScopeID:      sc.ScopeID,
			ProviderID:   id,
			ModelKey:     key,
			Label:        label,
			DiscoveredAt: stamp,
		})
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := scoped(tx, sc).Where("provider_id = ?", id).Delete(&LlmProviderModelRow{}).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}

// List returns a provider's cached models ordered by model_key. No rows means
// the provider has never been scanned successfully (or was cleared) — callers
// distinguish that from "scanned, found none" via the empty result plus a zero
// RefreshedAt, not by an error.
func (r *LlmProviderModelRepo) List(ctx context.Context, sc serverutil.Scope, providerID string) ([]ProviderModel, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("llm provider model repo not configured")
	}
	if err := requireTenant(sc); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(providerID)
	if id == "" {
		return nil, fmt.Errorf("provider_id is required")
	}
	var rows []LlmProviderModelRow
	if err := scoped(r.db.WithContext(ctx), sc).
		Where("provider_id = ?", id).
		Order("model_key asc").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ProviderModel, 0, len(rows))
	for _, row := range rows {
		out = append(out, ProviderModel{
			ModelKey:     row.ModelKey,
			Label:        row.Label,
			DiscoveredAt: row.DiscoveredAt,
		})
	}
	return out, nil
}

// Delete removes every cached model row for a provider. Called when a provider
// is unregistered so its models don't outlive it.
func (r *LlmProviderModelRepo) Delete(ctx context.Context, sc serverutil.Scope, providerID string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("llm provider model repo not configured")
	}
	if err := requireTenant(sc); err != nil {
		return err
	}
	id := strings.TrimSpace(providerID)
	if id == "" {
		return fmt.Errorf("provider_id is required")
	}
	return scoped(r.db.WithContext(ctx), sc).
		Where("provider_id = ?", id).
		Delete(&LlmProviderModelRow{}).Error
}
