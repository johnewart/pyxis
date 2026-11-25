package server

import (
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// The policy scope is the only thing that tells a hook who a call is for. The
// principal used to be dropped here while tenant and workspace travelled, so a
// hook resolving entitlements per person refused every call with no clue why.
func TestPolicyScopeCarriesPrincipal(t *testing.T) {
	got := policyScopeOf(serverutil.Scope{
		TenantID:    "tenant_a",
		ScopeID:     "workspace_b",
		PrincipalID: "person_c",
	})
	if got.GetTenantId() != "tenant_a" {
		t.Errorf("tenant = %q, want tenant_a", got.GetTenantId())
	}
	if got.GetWorkspaceId() != "workspace_b" {
		t.Errorf("workspace = %q, want workspace_b", got.GetWorkspaceId())
	}
	if got.GetPrincipalId() != "person_c" {
		t.Errorf("principal = %q, want person_c", got.GetPrincipalId())
	}
}

// A scope that arrived without a principal keeps it empty rather than
// inventing one: the hook decides what an anonymous call means.
func TestPolicyScopeEmptyPrincipalStaysEmpty(t *testing.T) {
	if got := policyScopeOf(serverutil.Scope{TenantID: "tenant_a"}); got.GetPrincipalId() != "" {
		t.Errorf("principal = %q, want empty", got.GetPrincipalId())
	}
}
