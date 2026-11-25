package providers

import (
	"context"
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
)

// TestCompositeScopeFallbackThroughCache proves scoped-first, tenant-wide
// fallback holds through Composite's cache and that a write evicts entries
// shadowed by it.
func TestCompositeScopeFallbackThroughCache(t *testing.T) {
	ctx := context.Background()
	repo := store.NewLlmProviderRepo(store.NewTestSQLite(t))
	comp := NewComposite(nil, repo)

	aWide := serverutil.Scope{TenantID: "tenant-a"}
	aS1 := serverutil.Scope{TenantID: "tenant-a", ScopeID: "s1"}
	bS1 := serverutil.Scope{TenantID: "tenant-b", ScopeID: "s1"}
	const id = "p1"
	const wideURL, scopedURL = "http://wide.example/v1", "http://scoped.example/v1"

	baseOf := func(sc serverutil.Scope) string {
		t.Helper()
		e, ok := comp.Get(ctx, sc, id)
		if !ok || e == nil {
			t.Fatalf("Get(%s, %q): not resolved", sc, id)
		}
		return e.openAIBase
	}

	// (a) tenant-wide row resolves from a scope (and is now cached).
	if err := repo.UpsertOpenAI(ctx, aWide, id, store.LlmProviderOpenAIRow{APIKey: "k", BaseURL: wideURL}); err != nil {
		t.Fatalf("upsert tenant-wide: %v", err)
	}
	if got := baseOf(aS1); got != wideURL {
		t.Fatalf("(a) A/s1 base = %q, want %q", got, wideURL)
	}
	if got := baseOf(aWide); got != wideURL {
		t.Fatalf("(a) A base = %q, want %q", got, wideURL)
	}

	// (b) a scoped write + invalidate evicts the cached fallback.
	if err := repo.UpsertOpenAI(ctx, aS1, id, store.LlmProviderOpenAIRow{APIKey: "k", BaseURL: scopedURL}); err != nil {
		t.Fatalf("upsert scoped: %v", err)
	}
	comp.InvalidateUserProvider(aS1, id)
	if got := baseOf(aS1); got != scopedURL {
		t.Fatalf("(b) A/s1 base = %q, want scoped %q", got, scopedURL)
	}
	if got := baseOf(aWide); got != wideURL {
		t.Fatalf("(b) A tenant-wide base = %q, want %q", got, wideURL)
	}

	// (c) another tenant sees neither row.
	if e, ok := comp.Get(ctx, bS1, id); ok || e != nil {
		t.Fatalf("(c) tenant B resolved %q: %+v", id, e)
	}

	// (d) deleting the scoped row + invalidate falls back to tenant-wide.
	if err := repo.Delete(ctx, aS1, id); err != nil {
		t.Fatalf("delete scoped: %v", err)
	}
	comp.InvalidateUserProvider(aS1, id)
	if got := baseOf(aS1); got != wideURL {
		t.Fatalf("(d) A/s1 base = %q, want tenant-wide %q", got, wideURL)
	}
}
