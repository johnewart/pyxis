package providers

import (
	"testing"

	"github.com/johnewart/pyxis/internal/store"
)

// A Composite built the normal way must already be able to persist model
// catalogs. This is a wiring test, not a behavior test: the bug it guards
// against is a host forgetting an optional field, which made sync succeed in
// the UI while llmd_llm_provider_models stayed empty and the catalog died on
// restart — with nothing logged.
func TestNewCompositePersistsModelsWithoutExtraWiring(t *testing.T) {
	db := store.NewTestSQLite(t)

	c := NewComposite(nil, store.NewLlmProviderRepo(db))

	if c.Models == nil {
		t.Fatal("NewComposite left Models nil: model persistence must not be something a host opts into")
	}
}

// A Composite with no database has nothing to persist to, and must not pretend
// otherwise by handing back a repo with a nil handle.
func TestNewCompositeWithoutDBHasNoModelStore(t *testing.T) {
	if c := NewComposite(nil, nil); c.Models != nil {
		t.Fatal("NewComposite(nil, nil) should have no catalog store")
	}
}
