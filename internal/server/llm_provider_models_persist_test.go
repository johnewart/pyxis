package server

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/store"
)

// newTestLlmServerWithModelCache builds a server whose Composite has the durable
// catalog cache wired, returning the shared DB so a test can simulate a restart
// by building a second Composite over it.
func newTestLlmServerWithModelCache(t *testing.T) (*LlmServer, *gorm.DB) {
	t.Helper()
	db := store.NewTestSQLite(t)
	comp := providers.NewComposite(nil, store.NewLlmProviderRepo(db))
	comp.Models = store.NewLlmProviderModelRepo(db)
	return &LlmServer{Providers: comp}, db
}

func registerTestAnthropic(t *testing.T, s *LlmServer, id string) {
	t.Helper()
	if _, err := s.RegisterLlmProvider(testScope.IncomingContext(context.Background()), &llmv1.RegisterLlmProviderRequest{
		ProviderId: id,
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_ANTHROPIC,
		Config: &llmv1.RegisterLlmProviderRequest_Anthropic{Anthropic: &llmv1.AnthropicLlmConfig{
			ApiKey:       "sk-ant-test",
			DefaultModel: strPtrTest("claude-x"),
		}},
	}); err != nil {
		t.Fatalf("RegisterLlmProvider(%q): %v", id, err)
	}
}

// seedCatalog writes a provider's models straight to the cache, standing in for
// a successful RefreshLlmProviderModels without doing a live provider scan
// (which would need network/CLI access).
func seedCatalog(t *testing.T, db *gorm.DB, providerID string, at time.Time, models ...store.ProviderModel) {
	t.Helper()
	if err := store.NewLlmProviderModelRepo(db).Replace(context.Background(), testScope, providerID, models, at); err != nil {
		t.Fatalf("seed catalog for %q: %v", providerID, err)
	}
}

// The regression this whole table exists for: a previously-scanned catalog must
// still be returned by a fresh process. Before persistence, a restart left the
// catalog empty until something called RefreshLlmProviderModels again, so
// consumers showed the provider id as if it were a model.
func TestListLlmCatalogModels_SurvivesRestart(t *testing.T) {
	ctx := testScope.IncomingContext(context.Background())
	s, db := newTestLlmServerWithModelCache(t)
	registerTestAnthropic(t, s, "claude")

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	seedCatalog(t, db, "claude", at,
		store.ProviderModel{ModelKey: "sonnet", Label: "Sonnet"},
		store.ProviderModel{ModelKey: "sonnet-low-enabled", Label: "Sonnet — Low (Thinking)"},
	)

	// A brand-new Composite over the same DB is what a restarted llmd looks like:
	// empty in-memory cache, nothing scanned yet this process.
	restarted := providers.NewComposite(nil, store.NewLlmProviderRepo(db))
	restarted.Models = store.NewLlmProviderModelRepo(db)
	fresh := &LlmServer{Providers: restarted}

	resp, err := fresh.ListLlmCatalogModels(ctx, &llmv1.ListLlmCatalogModelsRequest{})
	if err != nil {
		t.Fatalf("ListLlmCatalogModels: %v", err)
	}
	if len(resp.GetModels()) != 2 {
		t.Fatalf("models = %+v, want 2 after restart", resp.GetModels())
	}
	got := map[string]string{}
	for _, m := range resp.GetModels() {
		if m.GetProviderId() != "claude" {
			t.Errorf("model %q provider = %q, want claude", m.GetModelId(), m.GetProviderId())
		}
		got[m.GetModelId()] = m.GetDisplayName()
	}
	if _, ok := got["sonnet"]; !ok {
		t.Errorf("models missing key sonnet: %+v", got)
	}
	if label := got["sonnet-low-enabled"]; label != "Sonnet — Low (Thinking)" {
		t.Errorf("sonnet-low-enabled label = %q, want %q", label, "Sonnet — Low (Thinking)")
	}
}

// ListLlmProviders reports the persisted discovery time, not the time the entry
// happened to be rehydrated: consumers use it to decide whether a catalog is
// stale, so a restart must not look like a fresh scan.
func TestListLlmProviders_ReportsPersistedRefreshTimeAndCount(t *testing.T) {
	ctx := testScope.IncomingContext(context.Background())
	s, db := newTestLlmServerWithModelCache(t)
	registerTestAnthropic(t, s, "claude")

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	seedCatalog(t, db, "claude", at, store.ProviderModel{ModelKey: "sonnet", Label: "Sonnet"})

	restarted := providers.NewComposite(nil, store.NewLlmProviderRepo(db))
	restarted.Models = store.NewLlmProviderModelRepo(db)
	fresh := &LlmServer{Providers: restarted}

	resp, err := fresh.ListLlmProviders(ctx, &llmv1.ListLlmProvidersRequest{})
	if err != nil {
		t.Fatalf("ListLlmProviders: %v", err)
	}
	if len(resp.GetProviders()) != 1 {
		t.Fatalf("providers = %+v, want 1", resp.GetProviders())
	}
	p := resp.GetProviders()[0]
	if p.GetCatalogModelCount() != 1 {
		t.Errorf("catalog_model_count = %d, want 1", p.GetCatalogModelCount())
	}
	if want := at.UnixMilli(); p.GetModelsRefreshedUnixMs() != want {
		t.Errorf("models_refreshed_unix_ms = %d, want %d (persisted discovery time)", p.GetModelsRefreshedUnixMs(), want)
	}
}

// Editing any provider field invalidates its cached Entry. That used to throw
// the catalog away mid-process; hydration must restore it.
func TestListLlmCatalogModels_SurvivesProviderCacheInvalidation(t *testing.T) {
	ctx := testScope.IncomingContext(context.Background())
	s, db := newTestLlmServerWithModelCache(t)
	registerTestAnthropic(t, s, "claude")
	seedCatalog(t, db, "claude", time.Now().UTC(),
		store.ProviderModel{ModelKey: "sonnet", Label: "Sonnet"})

	// Warm the entry, then invalidate exactly as a config update does.
	if _, err := s.ListLlmCatalogModels(ctx, &llmv1.ListLlmCatalogModelsRequest{}); err != nil {
		t.Fatalf("ListLlmCatalogModels (warm): %v", err)
	}
	s.Providers.InvalidateUserProvider(testScope, "claude")

	resp, err := s.ListLlmCatalogModels(ctx, &llmv1.ListLlmCatalogModelsRequest{})
	if err != nil {
		t.Fatalf("ListLlmCatalogModels (after invalidate): %v", err)
	}
	if len(resp.GetModels()) != 1 || resp.GetModels()[0].GetModelId() != "sonnet" {
		t.Fatalf("models = %+v, want sonnet preserved across invalidation", resp.GetModels())
	}
}

// Unregistering must take the cached catalog with it, or a re-registered
// provider id (possibly a different kind) inherits a dead provider's models.
func TestUnregisterLlmProvider_DropsPersistedCatalog(t *testing.T) {
	ctx := testScope.IncomingContext(context.Background())
	s, db := newTestLlmServerWithModelCache(t)
	registerTestAnthropic(t, s, "claude")
	seedCatalog(t, db, "claude", time.Now().UTC(),
		store.ProviderModel{ModelKey: "sonnet", Label: "Sonnet"})

	if _, err := s.UnregisterLlmProvider(ctx, &llmv1.UnregisterLlmProviderRequest{ProviderId: "claude"}); err != nil {
		t.Fatalf("UnregisterLlmProvider: %v", err)
	}

	rows, err := store.NewLlmProviderModelRepo(db).List(ctx, testScope, "claude")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("persisted models = %+v, want none after unregister", rows)
	}

	// Re-registering the same id must start from an empty catalog.
	registerTestAnthropic(t, s, "claude")
	resp, err := s.ListLlmCatalogModels(ctx, &llmv1.ListLlmCatalogModelsRequest{})
	if err != nil {
		t.Fatalf("ListLlmCatalogModels: %v", err)
	}
	if len(resp.GetModels()) != 0 {
		t.Fatalf("models = %+v, want empty for re-registered provider", resp.GetModels())
	}
}

// A provider filter must not leak another provider's models.
func TestListLlmCatalogModels_FiltersByProvider(t *testing.T) {
	ctx := testScope.IncomingContext(context.Background())
	s, db := newTestLlmServerWithModelCache(t)
	registerTestAnthropic(t, s, "claude")
	registerTestAnthropic(t, s, "other")
	now := time.Now().UTC()
	seedCatalog(t, db, "claude", now, store.ProviderModel{ModelKey: "sonnet", Label: "Sonnet"})
	seedCatalog(t, db, "other", now, store.ProviderModel{ModelKey: "haiku", Label: "Haiku"})

	filter := "claude"
	resp, err := s.ListLlmCatalogModels(ctx, &llmv1.ListLlmCatalogModelsRequest{ProviderId: &filter})
	if err != nil {
		t.Fatalf("ListLlmCatalogModels: %v", err)
	}
	if len(resp.GetModels()) != 1 || resp.GetModels()[0].GetModelId() != "sonnet" {
		t.Fatalf("models = %+v, want only claude/sonnet", resp.GetModels())
	}
}

// With no cache wired the catalog is process-local (the pre-persistence
// behavior). It must degrade to empty, not fail provider resolution.
func TestListLlmCatalogModels_NoModelCacheConfiguredIsEmptyNotError(t *testing.T) {
	ctx := testScope.IncomingContext(context.Background())
	db := store.NewTestSQLite(t)
	s := &LlmServer{Providers: providers.NewComposite(nil, store.NewLlmProviderRepo(db))}
	registerTestAnthropic(t, s, "claude")

	resp, err := s.ListLlmCatalogModels(ctx, &llmv1.ListLlmCatalogModelsRequest{})
	if err != nil {
		t.Fatalf("ListLlmCatalogModels: %v", err)
	}
	if len(resp.GetModels()) != 0 {
		t.Fatalf("models = %+v, want empty", resp.GetModels())
	}
}
