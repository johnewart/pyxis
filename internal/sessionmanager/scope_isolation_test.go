package sessionmanager

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
)

// scopeFor builds a tenant-wide Scope from a test tenant constant.
func scopeFor(tenantID string) serverutil.Scope {
	return serverutil.Scope{TenantID: tenantID}
}

// TestManager_SameSessionIDDistinctTenants checks that one session id under
// two tenants yields two independent in-memory runtimes. Row-level storage
// isolation is out of scope here (the store is still keyed on the legacy
// column); this covers runtime/map behaviour only.
func TestManager_SameSessionIDDistinctTenants(t *testing.T) {
	t.Parallel()
	mgr, _ := testManagerWithSQLite(t)
	ctx := context.Background()
	const (
		tenantA = "a"
		tenantB = "b"
		sid     = "shared-session-id"
	)
	scA, scB := scopeFor(tenantA), scopeFor(tenantB)

	for _, sc := range []serverutil.Scope{scA, scB} {
		if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: sid}, sc); err != nil {
			t.Fatalf("EnsureSession(%s): %v", sc, err)
		}
	}

	rtA, okA := mgr.Get(scA, sid)
	rtB, okB := mgr.Get(scB, sid)
	if !okA || !okB {
		t.Fatalf("Get: okA=%v okB=%v, want both loaded", okA, okB)
	}
	if rtA == rtB {
		t.Fatal("same session id under two tenants returned the same runtime")
	}
	if rtA.Scope != scA || rtB.Scope != scB {
		t.Fatalf("runtime scopes = %v / %v, want %v / %v", rtA.Scope, rtB.Scope, scA, scB)
	}
	if n := mgr.RuntimeCount(); n != 2 {
		t.Fatalf("RuntimeCount = %d, want 2", n)
	}

	if !mgr.Destroy(ctx, scA, sid) {
		t.Fatal("Destroy for tenant a returned false")
	}
	if _, ok := mgr.Get(scA, sid); ok {
		t.Fatal("tenant a runtime still loaded after Destroy")
	}
	if err := mgr.EnsureLoaded(ctx, scB, sid); err != nil {
		t.Fatalf("EnsureLoaded tenant b after tenant a's Destroy: %v", err)
	}
	gotB, ok := mgr.Get(scB, sid)
	if !ok || gotB != rtB {
		t.Fatal("tenant b runtime was disturbed by tenant a's Destroy")
	}
}
