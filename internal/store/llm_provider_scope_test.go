package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/johnewart/pyxis/internal/serverutil"
)

func TestLlmProviderScopeFallthrough(t *testing.T) {
	ctx := context.Background()
	db := NewTestSQLite(t)
	repo := NewLlmProviderRepo(db)

	aWide := serverutil.Scope{TenantID: "tenant-a"}
	aS1 := serverutil.Scope{TenantID: "tenant-a", ScopeID: "s1"}
	bWide := serverutil.Scope{TenantID: "tenant-b"}
	bS1 := serverutil.Scope{TenantID: "tenant-b", ScopeID: "s1"}
	const x, y = "http://x.example", "http://y.example"

	ollamaURL := func(sc serverutil.Scope) string {
		t.Helper()
		cfg, err := repo.Get(ctx, sc, "p1")
		if err != nil {
			t.Fatalf("Get(%s): %v", sc, err)
		}
		if cfg.Ollama == nil {
			t.Fatalf("Get(%s): not an ollama provider: %+v", sc, cfg)
		}
		return cfg.Ollama.BaseURL
	}
	countP1 := func(sc serverutil.Scope) int {
		t.Helper()
		ids, err := repo.ListIDs(ctx, sc)
		if err != nil {
			t.Fatalf("ListIDs(%s): %v", sc, err)
		}
		n := 0
		for _, id := range ids {
			if id == "p1" {
				n++
			}
		}
		return n
	}

	// (a) tenant-wide provider is visible from a scope.
	if err := repo.UpsertOllama(ctx, aWide, "p1", LlmProviderOllamaRow{BaseURL: x}); err != nil {
		t.Fatalf("upsert tenant-wide: %v", err)
	}
	if got := ollamaURL(aS1); got != x {
		t.Fatalf("(a) Get from A/s1 = %q, want %q", got, x)
	}
	if n := countP1(aS1); n != 1 {
		t.Fatalf("(a) ListIDs A/s1 has p1 %d times, want 1", n)
	}

	// (b) a scoped provider with the same id wins inside its scope only.
	if err := repo.UpsertOllama(ctx, aS1, "p1", LlmProviderOllamaRow{BaseURL: y}); err != nil {
		t.Fatalf("upsert scoped: %v", err)
	}
	if got := ollamaURL(aS1); got != y {
		t.Fatalf("(b) Get from A/s1 = %q, want %q", got, y)
	}
	if got := ollamaURL(aWide); got != x {
		t.Fatalf("(b) Get from A tenant-wide = %q, want %q", got, x)
	}
	if n := countP1(aS1); n != 1 {
		t.Fatalf("(b) ListIDs A/s1 has p1 %d times, want 1", n)
	}

	// (c) another tenant sees neither.
	for _, sc := range []serverutil.Scope{bS1, bWide} {
		if _, err := repo.Get(ctx, sc, "p1"); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("(c) Get(%s) err = %v, want ErrRecordNotFound", sc, err)
		}
		ids, err := repo.ListIDs(ctx, sc)
		if err != nil {
			t.Fatalf("(c) ListIDs(%s): %v", sc, err)
		}
		if len(ids) != 0 {
			t.Fatalf("(c) ListIDs(%s) = %v, want empty", sc, ids)
		}
	}

	// (d) deleting the scoped row re-exposes the tenant-wide one.
	if err := repo.Delete(ctx, aS1, "p1"); err != nil {
		t.Fatalf("delete scoped: %v", err)
	}
	if got := ollamaURL(aS1); got != x {
		t.Fatalf("(d) Get from A/s1 after delete = %q, want %q", got, x)
	}

	// (e) re-registering the scoped row under a different kind must not
	// delete the tenant-wide row's child.
	if err := repo.UpsertOllama(ctx, aS1, "p1", LlmProviderOllamaRow{BaseURL: y}); err != nil {
		t.Fatalf("re-upsert scoped ollama: %v", err)
	}
	if err := repo.UpsertOpenAI(ctx, aS1, "p1", LlmProviderOpenAIRow{APIKey: "sk-test"}); err != nil {
		t.Fatalf("re-register scoped as openai: %v", err)
	}
	scopedCfg, err := repo.Get(ctx, aS1, "p1")
	if err != nil {
		t.Fatalf("(e) Get A/s1: %v", err)
	}
	if scopedCfg.OpenAI == nil {
		t.Fatalf("(e) Get A/s1 = %+v, want openai", scopedCfg)
	}
	if got := ollamaURL(aWide); got != x {
		t.Fatalf("(e) tenant-wide ollama child = %q, want %q", got, x)
	}

	// (f) the model catalog is exact-scope.
	models := NewLlmProviderModelRepo(db)
	if err := models.Replace(ctx, aS1, "p1", []ProviderModel{{ModelKey: "m1"}}, time.Now()); err != nil {
		t.Fatalf("(f) Replace: %v", err)
	}
	if got, err := models.List(ctx, aS1, "p1"); err != nil || len(got) != 1 {
		t.Fatalf("(f) List A/s1 = %v, %v; want 1 model", got, err)
	}
	for _, sc := range []serverutil.Scope{aWide, bS1} {
		got, err := models.List(ctx, sc, "p1")
		if err != nil {
			t.Fatalf("(f) List(%s): %v", sc, err)
		}
		if len(got) != 0 {
			t.Fatalf("(f) List(%s) = %v, want empty", sc, got)
		}
	}
}
