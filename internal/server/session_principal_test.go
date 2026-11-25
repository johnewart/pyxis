package server

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolregistry"
)

// A deployment that identifies the acting person sends a principal with every
// call. Session runtimes deliberately ignore it — a session belongs to a
// tenant and scope, not to whoever loaded it — and the two facts used to
// collide: ensureKnownSession compared a runtime's principal-less scope
// against the request's scope raw, so every call after EnsureSession came back
// "unknown session_id" for a session that plainly existed.
func TestSessionCallsWorkWhenScopeCarriesPrincipal(t *testing.T) {
	sc := serverutil.Scope{TenantID: "tenant_principal", PrincipalID: "person-1"}
	ctx := sc.IncomingContext(context.Background())

	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{})
	s := &LlmServer{
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		SessionRepo:     store.NewSessionRepo(db),
		Handlers:        toolexec.NewHandlers(),
	}

	const sid = "principal-session"
	if _, err := s.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: sid}); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	if _, err := s.ListSessionTools(ctx, &llmv1.ListSessionToolsRequest{SessionId: sid}); err != nil {
		t.Fatalf("ListSessionTools for a principal-carrying caller: %v", err)
	}

	// Another person in the same tenant reaches the same session: the
	// principal must not partition runtimes.
	other := serverutil.Scope{TenantID: sc.TenantID, PrincipalID: "person-2"}.IncomingContext(context.Background())
	if _, err := s.ListSessionTools(other, &llmv1.ListSessionToolsRequest{SessionId: sid}); err != nil {
		t.Fatalf("ListSessionTools for a second principal in the same tenant: %v", err)
	}

	// Tenancy still isolates: a different tenant must not see it.
	foreign := serverutil.Scope{TenantID: "other_tenant", PrincipalID: "person-1"}.IncomingContext(context.Background())
	if _, err := s.ListSessionTools(foreign, &llmv1.ListSessionToolsRequest{SessionId: sid}); err == nil {
		t.Error("a session leaked across tenants")
	}
}
