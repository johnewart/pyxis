package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"gorm.io/gorm"

	"github.com/johnewart/pyxis/internal/serverutil"
)

func TestEmbeddingProviderScopeFallthrough(t *testing.T) {
	ctx := context.Background()
	db := NewTestSQLite(t)
	repo := NewEmbeddingProviderRepo(db)
	src := repo.Source()

	aWide := serverutil.Scope{TenantID: "tenant-a"}
	aS1 := serverutil.Scope{TenantID: "tenant-a", ScopeID: "s1"}
	bWide := serverutil.Scope{TenantID: "tenant-b"}
	bS1 := serverutil.Scope{TenantID: "tenant-b", ScopeID: "s1"}

	// dims returns the resolved dimensions via both the repo and Source, asserting they agree.
	dims := func(sc serverutil.Scope) int {
		t.Helper()
		cfg, err := repo.Get(ctx, sc, "emb")
		if err != nil {
			t.Fatalf("Get(%s): %v", sc.String(), err)
		}
		pc, err := src.Get(ctx, sc, "emb")
		if err != nil {
			t.Fatalf("Source.Get(%s): %v", sc.String(), err)
		}
		var d, pd int
		switch {
		case cfg.Ollama != nil:
			d, pd = cfg.Ollama.Dimensions, pc.Ollama.Dimensions
		case cfg.OpenAICompat != nil:
			d, pd = cfg.OpenAICompat.Dimensions, pc.OpenAICompat.Dimensions
		default:
			t.Fatalf("unexpected kind %q", cfg.Kind)
		}
		if d != pd {
			t.Fatalf("repo/Source disagree for %s: %d vs %d", sc.String(), d, pd)
		}
		return d
	}
	notFound := func(sc serverutil.Scope) {
		t.Helper()
		if _, err := repo.Get(ctx, sc, "emb"); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("Get(%s): want ErrRecordNotFound, got %v", sc.String(), err)
		}
		if _, err := src.Get(ctx, sc, "emb"); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("Source.Get(%s): want ErrRecordNotFound, got %v", sc.String(), err)
		}
		ids, err := repo.ListIDs(ctx, sc)
		if err != nil || len(ids) != 0 {
			t.Fatalf("ListIDs(%s) = %v, %v; want empty", sc.String(), ids, err)
		}
	}
	listed := func(sc serverutil.Scope) {
		t.Helper()
		ids, err := repo.ListIDs(ctx, sc)
		if err != nil || !reflect.DeepEqual(ids, []string{"emb"}) {
			t.Fatalf("ListIDs(%s) = %v, %v; want [emb]", sc.String(), ids, err)
		}
	}

	// (1) tenant-wide provider visible from a scope.
	if err := repo.UpsertOllama(ctx, aWide, "emb", EmbeddingProviderOllamaRow{BaseURL: "http://x", EmbedModel: "m", Dimensions: 768}); err != nil {
		t.Fatal(err)
	}
	if d := dims(aS1); d != 768 {
		t.Fatalf("aS1 want tenant-wide 768, got %d", d)
	}
	listed(aS1)

	// (2) scoped provider of a different kind wins in its scope only.
	if err := repo.UpsertOpenAICompat(ctx, aS1, "emb", EmbeddingProviderOpenAICompatRow{BaseURL: "http://y", EmbedModel: "m2", Dimensions: 1536}); err != nil {
		t.Fatal(err)
	}
	if d := dims(aS1); d != 1536 {
		t.Fatalf("aS1 want scoped 1536, got %d", d)
	}
	if d := dims(aWide); d != 768 {
		t.Fatalf("aWide want 768, got %d", d)
	}
	listed(aS1)
	listed(aWide)

	// (3) other tenant sees nothing.
	notFound(bS1)
	notFound(bWide)

	// (4) delete scoped exposes tenant-wide; delete from other tenant is NotFound.
	if err := repo.Delete(ctx, aS1, "emb"); err != nil {
		t.Fatal(err)
	}
	if d := dims(aS1); d != 768 {
		t.Fatalf("after delete aS1 want 768, got %d", d)
	}
	if err := repo.Delete(ctx, bWide, "emb"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("Delete(bWide): want ErrRecordNotFound, got %v", err)
	}
	if d := dims(aWide); d != 768 {
		t.Fatalf("aWide row removed by other tenant's delete")
	}

	// Missing tenant is rejected.
	if _, err := repo.Get(ctx, serverutil.Scope{}, "emb"); err == nil {
		t.Fatal("Get with empty tenant: want error")
	}
}
