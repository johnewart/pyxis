package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

var embeddingChildTables = map[string]string{
	"ollama":        "llmd_embedding_provider_ollama",
	"openai_compat": "llmd_embedding_provider_openai_compat",
	"bedrock":       "llmd_embedding_provider_bedrock",
}

// EmbeddingProviderRepo persists embedding provider definitions as typed, per-kind rows — see
// LlmProviderRow's doc comment; same no-serialized-blob shape, keyed by
// (tenant_id, scope_id, provider_id). Reads prefer the row in the caller's
// scope and fall back to the tenant-wide (scope_id = '') row; writes and
// deletes match the exact scope.
type EmbeddingProviderRepo struct {
	db *gorm.DB
}

func NewEmbeddingProviderRepo(db *gorm.DB) *EmbeddingProviderRepo {
	return &EmbeddingProviderRepo{db: db}
}

func upsertEmbeddingProvider(ctx context.Context, db *gorm.DB, sc serverutil.Scope, id, kind, childTable string, childRow any) error {
	if db == nil {
		return fmt.Errorf("embedding provider repo not configured")
	}
	if err := requireTenant(sc); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("provider_id is required")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		parent := EmbeddingProviderRow{TenantID: sc.TenantID, ScopeID: sc.ScopeID, ProviderID: id, Kind: kind, CreatedAt: now, UpdatedAt: now}
		var existing EmbeddingProviderRow
		err := tx.Where("tenant_id = ? AND scope_id = ? AND provider_id = ?", sc.TenantID, sc.ScopeID, id).First(&existing).Error
		switch {
		case err == nil:
			parent.CreatedAt = existing.CreatedAt
		case errors.Is(err, gorm.ErrRecordNotFound):
		default:
			return fmt.Errorf("lookup embedding provider %q in %s: %w", id, sc.String(), err)
		}
		if err := tx.Save(&parent).Error; err != nil {
			return err
		}
		for k, tbl := range embeddingChildTables {
			if k == kind {
				continue
			}
			if err := tx.Exec("DELETE FROM "+tbl+" WHERE tenant_id = ? AND scope_id = ? AND provider_id = ?", sc.TenantID, sc.ScopeID, id).Error; err != nil {
				return err
			}
		}
		return tx.Table(childTable).Save(childRow).Error
	})
}

func (r *EmbeddingProviderRepo) UpsertOllama(ctx context.Context, sc serverutil.Scope, id string, cfg EmbeddingProviderOllamaRow) error {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.EmbedModel) == "" || cfg.Dimensions <= 0 {
		return fmt.Errorf("ollama embedding config requires base_url, embed_model, dimensions")
	}
	cfg.TenantID, cfg.ScopeID = sc.TenantID, sc.ScopeID
	cfg.ProviderID = strings.TrimSpace(id)
	return upsertEmbeddingProvider(ctx, r.db, sc, id, "ollama", "llmd_embedding_provider_ollama", &cfg)
}

func (r *EmbeddingProviderRepo) UpsertOpenAICompat(ctx context.Context, sc serverutil.Scope, id string, cfg EmbeddingProviderOpenAICompatRow) error {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.EmbedModel) == "" || cfg.Dimensions <= 0 {
		return fmt.Errorf("openai_compat embedding config requires base_url, embed_model, dimensions")
	}
	cfg.TenantID, cfg.ScopeID = sc.TenantID, sc.ScopeID
	cfg.ProviderID = strings.TrimSpace(id)
	return upsertEmbeddingProvider(ctx, r.db, sc, id, "openai_compat", "llmd_embedding_provider_openai_compat", &cfg)
}

func (r *EmbeddingProviderRepo) UpsertBedrock(ctx context.Context, sc serverutil.Scope, id string, cfg EmbeddingProviderBedrockRow) error {
	if strings.TrimSpace(cfg.Region) == "" || strings.TrimSpace(cfg.EmbedModel) == "" || cfg.Dimensions <= 0 {
		return fmt.Errorf("bedrock embedding config requires region, embed_model, dimensions")
	}
	cfg.TenantID, cfg.ScopeID = sc.TenantID, sc.ScopeID
	cfg.ProviderID = strings.TrimSpace(id)
	return upsertEmbeddingProvider(ctx, r.db, sc, id, "bedrock", "llmd_embedding_provider_bedrock", &cfg)
}

// UpsertConfig dispatches to the matching UpsertX method based on which field of cfg is set.
func (r *EmbeddingProviderRepo) UpsertConfig(ctx context.Context, sc serverutil.Scope, cfg *EmbeddingProviderConfig) error {
	if cfg == nil {
		return fmt.Errorf("nil provider config")
	}
	switch {
	case cfg.Ollama != nil:
		return r.UpsertOllama(ctx, sc, cfg.ProviderID, *cfg.Ollama)
	case cfg.OpenAICompat != nil:
		return r.UpsertOpenAICompat(ctx, sc, cfg.ProviderID, *cfg.OpenAICompat)
	case cfg.Bedrock != nil:
		return r.UpsertBedrock(ctx, sc, cfg.ProviderID, *cfg.Bedrock)
	default:
		return fmt.Errorf("embedding provider config has no backend block set")
	}
}

// Get resolves providerID for sc: the row in sc's exact scope wins, otherwise
// the tenant-wide row. The child row is read from the same (tenant, scope) as
// the parent it found.
func (r *EmbeddingProviderRepo) Get(ctx context.Context, sc serverutil.Scope, providerID string) (*EmbeddingProviderConfig, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("embedding provider repo not configured")
	}
	if err := requireTenant(sc); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(providerID)
	if id == "" {
		return nil, fmt.Errorf("provider_id is required")
	}
	var parent EmbeddingProviderRow
	if err := scopedOrTenantWide(r.db.WithContext(ctx), sc).
		Where("provider_id = ?", id).
		Order("scope_id DESC").
		First(&parent).Error; err != nil {
		return nil, err
	}
	cfg := &EmbeddingProviderConfig{ProviderID: id, Kind: parent.Kind}
	owner := serverutil.Scope{TenantID: parent.TenantID, ScopeID: parent.ScopeID}
	child := func() *gorm.DB {
		return scoped(r.db.WithContext(ctx), owner).Where("provider_id = ?", id)
	}
	switch parent.Kind {
	case "ollama":
		var row EmbeddingProviderOllamaRow
		if err := child().First(&row).Error; err != nil {
			return nil, err
		}
		cfg.Ollama = &row
	case "openai_compat":
		var row EmbeddingProviderOpenAICompatRow
		if err := child().First(&row).Error; err != nil {
			return nil, err
		}
		cfg.OpenAICompat = &row
	case "bedrock":
		var row EmbeddingProviderBedrockRow
		if err := child().First(&row).Error; err != nil {
			return nil, err
		}
		cfg.Bedrock = &row
	default:
		return nil, fmt.Errorf("unknown embedding provider kind %q", parent.Kind)
	}
	return cfg, nil
}

// Delete removes providerID from sc's exact scope only; it never falls through
// to the tenant-wide row.
func (r *EmbeddingProviderRepo) Delete(ctx context.Context, sc serverutil.Scope, providerID string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("embedding provider repo not configured")
	}
	if err := requireTenant(sc); err != nil {
		return err
	}
	id := strings.TrimSpace(providerID)
	if id == "" {
		return fmt.Errorf("provider_id is required")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("tenant_id = ? AND scope_id = ? AND provider_id = ?", sc.TenantID, sc.ScopeID, id).Delete(&EmbeddingProviderRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		for _, tbl := range embeddingChildTables {
			if err := tx.Exec("DELETE FROM "+tbl+" WHERE tenant_id = ? AND scope_id = ? AND provider_id = ?", sc.TenantID, sc.ScopeID, id).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ListIDs returns the de-duplicated union of provider IDs in sc's scope and
// the tenant-wide scope, sorted ascending.
func (r *EmbeddingProviderRepo) ListIDs(ctx context.Context, sc serverutil.Scope) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("embedding provider repo not configured")
	}
	if err := requireTenant(sc); err != nil {
		return nil, err
	}
	var rows []EmbeddingProviderRow
	if err := scopedOrTenantWide(r.db.WithContext(ctx), sc).Find(&rows).Error; err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(rows))
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		id := strings.TrimSpace(row.ProviderID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
