package store

import (
	"context"
	"testing"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
)

var modelRepoTestScope = serverutil.Scope{TenantID: "model-repo-tenant"}

func TestLlmProviderModelRepo_ReplaceThenListRoundTrips(t *testing.T) {
	ctx := context.Background()
	repo := NewLlmProviderModelRepo(NewTestSQLite(t))
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	if err := repo.Replace(ctx, modelRepoTestScope, "claude", []ProviderModel{
		{ModelKey: "sonnet-low-enabled", Label: "Sonnet — Low (Thinking)"},
		{ModelKey: "sonnet", Label: "Sonnet"},
	}, at); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	got, err := repo.List(ctx, modelRepoTestScope, "claude")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Ordered by model_key, so "sonnet" precedes "sonnet-low-enabled".
	if len(got) != 2 {
		t.Fatalf("len(models) = %d, want 2: %+v", len(got), got)
	}
	if got[0].ModelKey != "sonnet" || got[0].Label != "Sonnet" {
		t.Errorf("models[0] = %+v, want key=sonnet label=Sonnet", got[0])
	}
	if got[1].ModelKey != "sonnet-low-enabled" || got[1].Label != "Sonnet — Low (Thinking)" {
		t.Errorf("models[1] = %+v, want key=sonnet-low-enabled label=Sonnet — Low (Thinking)", got[1])
	}
	for _, m := range got {
		if !m.DiscoveredAt.Equal(at) {
			t.Errorf("model %q DiscoveredAt = %v, want %v", m.ModelKey, m.DiscoveredAt, at)
		}
	}
}

// Replace is wholesale, not a merge: a model the provider stopped offering must
// disappear rather than linger in the catalog forever.
func TestLlmProviderModelRepo_ReplaceDropsModelsNoLongerOffered(t *testing.T) {
	ctx := context.Background()
	repo := NewLlmProviderModelRepo(NewTestSQLite(t))
	now := time.Now().UTC()

	if err := repo.Replace(ctx, modelRepoTestScope, "openai", []ProviderModel{
		{ModelKey: "gpt-4o", Label: "GPT-4o"},
		{ModelKey: "gpt-retired", Label: "Retired"},
	}, now); err != nil {
		t.Fatalf("Replace (first): %v", err)
	}
	if err := repo.Replace(ctx, modelRepoTestScope, "openai", []ProviderModel{
		{ModelKey: "gpt-4o", Label: "GPT-4o"},
	}, now); err != nil {
		t.Fatalf("Replace (second): %v", err)
	}

	got, err := repo.List(ctx, modelRepoTestScope, "openai")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ModelKey != "gpt-4o" {
		t.Fatalf("models = %+v, want only gpt-4o", got)
	}
}

func TestLlmProviderModelRepo_ReplaceIsScopedToOneProvider(t *testing.T) {
	ctx := context.Background()
	repo := NewLlmProviderModelRepo(NewTestSQLite(t))
	now := time.Now().UTC()

	if err := repo.Replace(ctx, modelRepoTestScope, "a", []ProviderModel{{ModelKey: "m-a"}}, now); err != nil {
		t.Fatalf("Replace a: %v", err)
	}
	if err := repo.Replace(ctx, modelRepoTestScope, "b", []ProviderModel{{ModelKey: "m-b"}}, now); err != nil {
		t.Fatalf("Replace b: %v", err)
	}
	// Re-scanning a must not disturb b.
	if err := repo.Replace(ctx, modelRepoTestScope, "a", []ProviderModel{{ModelKey: "m-a2"}}, now); err != nil {
		t.Fatalf("Replace a again: %v", err)
	}

	bModels, err := repo.List(ctx, modelRepoTestScope, "b")
	if err != nil {
		t.Fatalf("List b: %v", err)
	}
	if len(bModels) != 1 || bModels[0].ModelKey != "m-b" {
		t.Fatalf("provider b models = %+v, want only m-b", bModels)
	}
}

// A model with no label is still selectable: the key stands in, so a picker
// never renders a blank row.
func TestLlmProviderModelRepo_ReplaceDefaultsLabelToModelKey(t *testing.T) {
	ctx := context.Background()
	repo := NewLlmProviderModelRepo(NewTestSQLite(t))

	if err := repo.Replace(ctx, modelRepoTestScope, "ollama", []ProviderModel{
		{ModelKey: "llama3", Label: "   "},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	got, err := repo.List(ctx, modelRepoTestScope, "ollama")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Label != "llama3" {
		t.Fatalf("models = %+v, want label defaulted to llama3", got)
	}
}

// Duplicate keys within one scan are dropped rather than failing the whole
// Replace on the composite primary key.
func TestLlmProviderModelRepo_ReplaceDedupesAndSkipsBlankKeys(t *testing.T) {
	ctx := context.Background()
	repo := NewLlmProviderModelRepo(NewTestSQLite(t))

	if err := repo.Replace(ctx, modelRepoTestScope, "p", []ProviderModel{
		{ModelKey: "dup", Label: "First"},
		{ModelKey: "dup", Label: "Second"},
		{ModelKey: "  ", Label: "blank key"},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	got, err := repo.List(ctx, modelRepoTestScope, "p")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ModelKey != "dup" || got[0].Label != "First" {
		t.Fatalf("models = %+v, want single dup row keeping the first label", got)
	}
}

func TestLlmProviderModelRepo_DeleteRemovesProviderCatalog(t *testing.T) {
	ctx := context.Background()
	repo := NewLlmProviderModelRepo(NewTestSQLite(t))

	if err := repo.Replace(ctx, modelRepoTestScope, "gone", []ProviderModel{{ModelKey: "m"}}, time.Now().UTC()); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if err := repo.Delete(ctx, modelRepoTestScope, "gone"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.List(ctx, modelRepoTestScope, "gone")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("models = %+v, want empty after Delete", got)
	}
}

// An unscanned provider is an empty list, not an error: callers distinguish
// "never scanned" from "scanned, found none" via the provider's refresh
// timestamp, so List must not make absence look like a failure.
func TestLlmProviderModelRepo_ListUnknownProviderIsEmptyNotError(t *testing.T) {
	ctx := context.Background()
	repo := NewLlmProviderModelRepo(NewTestSQLite(t))

	got, err := repo.List(ctx, modelRepoTestScope, "never-scanned")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("models = %+v, want empty", got)
	}
}

func TestLlmProviderModelRepo_RequiresProviderID(t *testing.T) {
	ctx := context.Background()
	repo := NewLlmProviderModelRepo(NewTestSQLite(t))

	if err := repo.Replace(ctx, modelRepoTestScope, "  ", nil, time.Now().UTC()); err == nil {
		t.Error("Replace with blank provider_id: want error, got nil")
	}
	if _, err := repo.List(ctx, modelRepoTestScope, ""); err == nil {
		t.Error("List with blank provider_id: want error, got nil")
	}
	if err := repo.Delete(ctx, modelRepoTestScope, ""); err == nil {
		t.Error("Delete with blank provider_id: want error, got nil")
	}
}
