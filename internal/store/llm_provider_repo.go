package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// llmChildTables lists every LlmProvider<Kind>Row table, keyed by Kind. Used to clean up the
// OTHER kind tables when a provider is re-registered under a different kind, and to fully remove
// a provider on Delete.
//
// Keyed by the enum rather than by a kind id string: a typo'd key used to mean a child table was
// silently never cleaned up, and nothing would fail.
var llmChildTables = map[providerkind.Kind]string{
	providerkind.Anthropic:    "llmd_llm_provider_anthropic",
	providerkind.OpenAI:       "llmd_llm_provider_openai",
	providerkind.Ollama:       "llmd_llm_provider_ollama",
	providerkind.CursorCLI:    "llmd_llm_provider_cursor_cli",
	providerkind.ClaudeCodeV2: "llmd_llm_provider_claude_code",
	providerkind.Bedrock:      "llmd_llm_provider_bedrock",
	providerkind.Gemini:       "llmd_llm_provider_gemini",
	providerkind.OpenRouter:   "llmd_llm_provider_openrouter",
}

// LlmProviderRepo persists LLM provider definitions as typed, per-kind rows (see
// LlmProviderRow's doc comment). Rows are keyed by (tenant_id, scope_id, provider_id):
// reads fall through from the caller's scope to the tenant-wide (scope_id = ”) row,
// writes and deletes are exact-scope.
type LlmProviderRepo struct {
	db *gorm.DB
}

func NewLlmProviderRepo(db *gorm.DB) *LlmProviderRepo {
	return &LlmProviderRepo{db: db}
}

// ModelCatalog returns the per-provider model catalog cache backed by the SAME
// database as this repo. It exists so nothing outside store has to re-wire a
// second repo by hand to make model persistence work: a host that can resolve
// providers can always persist their catalogs. Forgetting that wiring used to
// silently downgrade sync to memory-only, so the catalog vanished on restart
// with nothing logged and an empty llmd_llm_provider_models table.
func (r *LlmProviderRepo) ModelCatalog() *LlmProviderModelRepo {
	if r == nil || r.db == nil {
		return nil
	}
	return NewLlmProviderModelRepo(r.db)
}

func requireTenant(sc serverutil.Scope) error {
	if strings.TrimSpace(sc.TenantID) == "" {
		return fmt.Errorf("tenant_id is required")
	}
	return nil
}

// upsertLlmProvider writes the parent row (preserving CreatedAt across updates), removes any
// stale row for this id from every OTHER kind's child table in the SAME (tenant, scope)
// (handles a provider re-registered under a different kind), then upserts childRow into
// childTable. childRow must already carry TenantID/ScopeID/ProviderID.
func upsertLlmProvider(ctx context.Context, db *gorm.DB, sc serverutil.Scope, id string, kind providerkind.Kind, childRow any) error {
	if db == nil {
		return fmt.Errorf("llm provider repo not configured")
	}
	if err := requireTenant(sc); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("provider_id is required")
	}
	// The child table is derived from the kind, not passed alongside it: the two
	// arguments could previously disagree.
	childTable, ok := llmChildTables[kind]
	if !ok {
		return fmt.Errorf("no child table for provider kind %v", kind)
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		// Kind.String() is the ONLY place a kind becomes a stored id.
		parent := LlmProviderRow{
			TenantID: sc.TenantID, ScopeID: sc.ScopeID, ProviderID: id,
			Kind: kind.String(), CreatedAt: now, UpdatedAt: now,
		}
		var existing LlmProviderRow
		err := scoped(tx, sc).Where("provider_id = ?", id).First(&existing).Error
		switch {
		case err == nil:
			parent.CreatedAt = existing.CreatedAt
		case errors.Is(err, gorm.ErrRecordNotFound):
		default:
			return fmt.Errorf("look up existing provider %q (tenant %q scope %q): %w", id, sc.TenantID, sc.ScopeID, err)
		}
		// Explicit upsert, not tx.Save: Save treats a zero-valued PK column as
		// "new row" and INSERTs, and scope_id = "" (tenant-wide) is a zero value,
		// so re-saving a tenant-wide provider would hit the unique constraint.
		upsert := clause.OnConflict{UpdateAll: true}
		if err := tx.Clauses(upsert).Create(&parent).Error; err != nil {
			return err
		}
		for k, tbl := range llmChildTables {
			if k == kind {
				continue
			}
			if err := tx.Exec("DELETE FROM "+tbl+" WHERE tenant_id = ? AND scope_id = ? AND provider_id = ?",
				sc.TenantID, sc.ScopeID, id).Error; err != nil {
				return err
			}
		}
		return tx.Table(childTable).Clauses(upsert).Create(childRow).Error
	})
}

func (r *LlmProviderRepo) UpsertAnthropic(ctx context.Context, sc serverutil.Scope, id string, cfg LlmProviderAnthropicRow) error {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return fmt.Errorf("anthropic.api_key is required")
	}
	cfg.TenantID, cfg.ScopeID, cfg.ProviderID = sc.TenantID, sc.ScopeID, strings.TrimSpace(id)
	return upsertLlmProvider(ctx, r.db, sc, id, providerkind.Anthropic, &cfg)
}

func (r *LlmProviderRepo) UpsertOpenAI(ctx context.Context, sc serverutil.Scope, id string, cfg LlmProviderOpenAIRow) error {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return fmt.Errorf("openai.api_key is required")
	}
	cfg.TenantID, cfg.ScopeID, cfg.ProviderID = sc.TenantID, sc.ScopeID, strings.TrimSpace(id)
	return upsertLlmProvider(ctx, r.db, sc, id, providerkind.OpenAI, &cfg)
}

func (r *LlmProviderRepo) UpsertOllama(ctx context.Context, sc serverutil.Scope, id string, cfg LlmProviderOllamaRow) error {
	cfg.TenantID, cfg.ScopeID, cfg.ProviderID = sc.TenantID, sc.ScopeID, strings.TrimSpace(id)
	return upsertLlmProvider(ctx, r.db, sc, id, providerkind.Ollama, &cfg)
}

func (r *LlmProviderRepo) UpsertCursorCLI(ctx context.Context, sc serverutil.Scope, id string, cfg LlmProviderCursorCLIRow) error {
	cfg.TenantID, cfg.ScopeID, cfg.ProviderID = sc.TenantID, sc.ScopeID, strings.TrimSpace(id)
	return upsertLlmProvider(ctx, r.db, sc, id, providerkind.CursorCLI, &cfg)
}

func (r *LlmProviderRepo) UpsertClaudeCode(ctx context.Context, sc serverutil.Scope, id string, cfg LlmProviderClaudeCodeRow) error {
	cfg.TenantID, cfg.ScopeID, cfg.ProviderID = sc.TenantID, sc.ScopeID, strings.TrimSpace(id)
	return upsertLlmProvider(ctx, r.db, sc, id, providerkind.ClaudeCodeV2, &cfg)
}

func (r *LlmProviderRepo) UpsertBedrock(ctx context.Context, sc serverutil.Scope, id string, cfg LlmProviderBedrockRow) error {
	if strings.TrimSpace(cfg.Region) == "" {
		return fmt.Errorf("bedrock.region is required")
	}
	cfg.TenantID, cfg.ScopeID, cfg.ProviderID = sc.TenantID, sc.ScopeID, strings.TrimSpace(id)
	// An empty external_id means "unset": keep the stored secret rather than
	// letting the upsert overwrite it with "".
	if cfg.ExternalID == "" {
		var existing LlmProviderBedrockRow
		err := r.db.WithContext(ctx).
			Where("tenant_id = ? AND scope_id = ? AND provider_id = ?", sc.TenantID, sc.ScopeID, cfg.ProviderID).
			First(&existing).Error
		switch {
		case err == nil:
			cfg.ExternalID = existing.ExternalID
		case errors.Is(err, gorm.ErrRecordNotFound):
		default:
			return fmt.Errorf("look up existing bedrock row for provider %q: %w", cfg.ProviderID, err)
		}
	}
	return upsertLlmProvider(ctx, r.db, sc, id, providerkind.Bedrock, &cfg)
}

func (r *LlmProviderRepo) UpsertGemini(ctx context.Context, sc serverutil.Scope, id string, cfg LlmProviderGeminiRow) error {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return fmt.Errorf("gemini.api_key is required")
	}
	cfg.TenantID, cfg.ScopeID, cfg.ProviderID = sc.TenantID, sc.ScopeID, strings.TrimSpace(id)
	return upsertLlmProvider(ctx, r.db, sc, id, providerkind.Gemini, &cfg)
}

func (r *LlmProviderRepo) UpsertOpenRouter(ctx context.Context, sc serverutil.Scope, id string, cfg LlmProviderOpenRouterRow) error {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return fmt.Errorf("openrouter.api_key is required")
	}
	cfg.TenantID, cfg.ScopeID, cfg.ProviderID = sc.TenantID, sc.ScopeID, strings.TrimSpace(id)
	return upsertLlmProvider(ctx, r.db, sc, id, providerkind.OpenRouter, &cfg)
}

// UpsertConfig dispatches to the matching UpsertX method based on which field of cfg is set.
// The write lands in sc's exact (tenant, scope).
func (r *LlmProviderRepo) UpsertConfig(ctx context.Context, sc serverutil.Scope, cfg *LlmProviderConfig) error {
	if cfg == nil {
		return fmt.Errorf("nil provider config")
	}
	if err := requireTenant(sc); err != nil {
		return err
	}
	switch {
	case cfg.Anthropic != nil:
		return r.UpsertAnthropic(ctx, sc, cfg.ProviderID, *cfg.Anthropic)
	case cfg.OpenAI != nil:
		return r.UpsertOpenAI(ctx, sc, cfg.ProviderID, *cfg.OpenAI)
	case cfg.Ollama != nil:
		return r.UpsertOllama(ctx, sc, cfg.ProviderID, *cfg.Ollama)
	case cfg.CursorCLI != nil:
		return r.UpsertCursorCLI(ctx, sc, cfg.ProviderID, *cfg.CursorCLI)
	case cfg.ClaudeCode != nil:
		return r.UpsertClaudeCode(ctx, sc, cfg.ProviderID, *cfg.ClaudeCode)
	case cfg.Bedrock != nil:
		return r.UpsertBedrock(ctx, sc, cfg.ProviderID, *cfg.Bedrock)
	case cfg.Gemini != nil:
		return r.UpsertGemini(ctx, sc, cfg.ProviderID, *cfg.Gemini)
	case cfg.OpenRouter != nil:
		return r.UpsertOpenRouter(ctx, sc, cfg.ProviderID, *cfg.OpenRouter)
	default:
		return fmt.Errorf("provider config has no backend block set")
	}
}

// Get resolves the parent row visible from sc — the row in sc's own scope if there is
// one, otherwise the tenant-wide row — and then its typed child row under the exact
// (tenant, scope) of the parent it found.
func (r *LlmProviderRepo) Get(ctx context.Context, sc serverutil.Scope, providerID string) (*LlmProviderConfig, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("llm provider repo not configured")
	}
	if err := requireTenant(sc); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(providerID)
	if id == "" {
		return nil, fmt.Errorf("provider_id is required")
	}
	var parent LlmProviderRow
	if err := scopedOrTenantWide(r.db.WithContext(ctx), sc).
		Where("provider_id = ?", id).
		Order("scope_id DESC").
		First(&parent).Error; err != nil {
		return nil, err
	}
	// THE one place a stored kind id becomes the enum. Strict: a value that is not
	// exactly one of Kind.String()'s ids is a corrupt row, not something to guess at.
	kind, ok := providerkind.Parse(parent.Kind)
	if !ok {
		return nil, fmt.Errorf(
			"provider %q has unknown kind %q in %s.kind; expected one of the ids in providerkind.Kind.String()",
			id, parent.Kind, LlmProviderRow{}.TableName())
	}
	cfg := &LlmProviderConfig{ProviderID: id, Kind: kind}
	owner := serverutil.Scope{TenantID: parent.TenantID, ScopeID: parent.ScopeID}
	child := func() *gorm.DB {
		return scoped(r.db.WithContext(ctx), owner).Where("provider_id = ?", id)
	}
	switch kind {
	case providerkind.Anthropic:
		var row LlmProviderAnthropicRow
		if err := child().First(&row).Error; err != nil {
			return nil, err
		}
		cfg.Anthropic = &row
	case providerkind.OpenAI:
		var row LlmProviderOpenAIRow
		if err := child().First(&row).Error; err != nil {
			return nil, err
		}
		cfg.OpenAI = &row
	case providerkind.Ollama:
		var row LlmProviderOllamaRow
		if err := child().First(&row).Error; err != nil {
			return nil, err
		}
		cfg.Ollama = &row
	case providerkind.CursorCLI:
		var row LlmProviderCursorCLIRow
		if err := child().First(&row).Error; err != nil {
			return nil, err
		}
		cfg.CursorCLI = &row
	case providerkind.ClaudeCodeV2:
		var row LlmProviderClaudeCodeRow
		if err := child().First(&row).Error; err != nil {
			return nil, err
		}
		cfg.ClaudeCode = &row
	case providerkind.Bedrock:
		var row LlmProviderBedrockRow
		if err := child().First(&row).Error; err != nil {
			return nil, err
		}
		cfg.Bedrock = &row
	case providerkind.Gemini:
		var row LlmProviderGeminiRow
		if err := child().First(&row).Error; err != nil {
			return nil, err
		}
		cfg.Gemini = &row
	case providerkind.OpenRouter:
		var row LlmProviderOpenRouterRow
		if err := child().First(&row).Error; err != nil {
			return nil, err
		}
		cfg.OpenRouter = &row
	default:
		// Parse said this is a real kind, so reaching here means a kind was added
		// to providerkind without a child table here.
		return nil, fmt.Errorf("provider kind %v has no child table wired up in %s", kind, "llm_provider_repo.go")
	}
	return cfg, nil
}

// Delete removes a provider's parent row and every child-table row for it in sc's exact
// (tenant, scope). It never touches a tenant-wide row with the same id.
func (r *LlmProviderRepo) Delete(ctx context.Context, sc serverutil.Scope, providerID string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("llm provider repo not configured")
	}
	if err := requireTenant(sc); err != nil {
		return err
	}
	id := strings.TrimSpace(providerID)
	if id == "" {
		return fmt.Errorf("provider_id is required")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := scoped(tx, sc).Where("provider_id = ?", id).Delete(&LlmProviderRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		for _, tbl := range llmChildTables {
			// Same exact-scope predicate as scoped(), spelled out for a raw table delete.
			if err := tx.Exec("DELETE FROM "+tbl+" WHERE tenant_id = ? AND scope_id = ? AND provider_id = ?",
				sc.TenantID, sc.ScopeID, id).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ListIDs returns the de-duplicated, sorted union of provider ids registered in sc's
// scope and tenant-wide in sc's tenant.
func (r *LlmProviderRepo) ListIDs(ctx context.Context, sc serverutil.Scope) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("llm provider repo not configured")
	}
	if err := requireTenant(sc); err != nil {
		return nil, err
	}
	var rows []LlmProviderRow
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
