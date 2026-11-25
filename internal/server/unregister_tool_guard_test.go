package server

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/toolregistry"
)

// Clients reconcile their tool set before every turn and will happily ask the
// server to unregister anything they do not know about. Only tools they own
// (execution variant delegate_to_client) may actually be removed; server-installed
// tools must survive, and the call must still report success so the reconcile
// loop does not break the turn.
func TestUnregisterSessionToolRefusesServerInstalledTools(t *testing.T) {
	ctx := testScope.IncomingContext(context.Background())

	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{})
	const sid = "agent_guard"
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: sid}, testScope); err != nil {
		t.Fatal(err)
	}
	ma, ok := mgr.Get(testScope, sid)
	if !ok {
		t.Fatalf("session %q not materialized", sid)
	}
	// Server-installed tool: arrives via the agent profile / builtin path, not the client.
	if err := ma.Tools.Register(&llmv1.ToolDefinition{
		Name:                 "server_builtin",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
		Execution:            &llmv1.ToolDefinition_InlineBuiltin{InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{}},
	}); err != nil {
		t.Fatal(err)
	}
	// Client-owned tool: what clientToolToRegisterProto emits for a ClientTool with execute().
	if err := ma.Tools.Register(&llmv1.ToolDefinition{
		Name:                 "client_echo",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
		Execution:            &llmv1.ToolDefinition_DelegateToClient{DelegateToClient: &llmv1.ToolExecutionDelegateToClient{}},
	}); err != nil {
		t.Fatal(err)
	}

	srv := &LlmServer{
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		TurnRegister:    store.NewTurnRegister(db),
	}

	for _, name := range []string{"server_builtin", "client_echo", "never_registered"} {
		if _, err := srv.UnregisterSessionTool(ctx, &llmv1.UnregisterSessionToolRequest{
			SessionId: sid,
			Name:      name,
		}); err != nil {
			t.Fatalf("UnregisterSessionTool(%q) returned error, want success: %v", name, err)
		}
	}

	res, err := srv.ListSessionTools(ctx, &llmv1.ListSessionToolsRequest{SessionId: sid})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, td := range res.GetTools() {
		got[td.GetName()] = true
	}
	if !got["server_builtin"] {
		t.Errorf("server-installed tool was removed by a client unregister; tools=%v", got)
	}
	if got["client_echo"] {
		t.Errorf("client-owned tool should have been removed; tools=%v", got)
	}
}
