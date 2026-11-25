package sessionmanager

import (
	"context"
	"sort"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/store"
)

func builtin(name, desc string) *llmv1.ToolDefinition {
	return &llmv1.ToolDefinition{
		Name: name, Description: desc, ParametersSchemaJson: `{"type":"object"}`,
		Execution: &llmv1.ToolDefinition_InlineBuiltin{InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{}},
	}
}

func names(defs []*llmv1.ToolDefinition) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.GetName())
	}
	sort.Strings(out)
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// replace_tools makes initial_tools the session's set on an existing
// session: stale server-installed tools go (they cannot be unregistered any
// other way), changed ones are replaced, delegated client tools stay, and
// the result is persisted so it survives a restore.
func TestEnsureSession_ReplaceTools(t *testing.T) {
	t.Parallel()
	mgr, repo := testManagerWithSQLite(t)
	ctx := context.Background()
	sc := scopeFor("workspace-1")
	const sid = "sess-replace"

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:    sid,
		InitialTools: []*llmv1.ToolDefinition{builtin("keep", "v1"), builtin("stale", "v1")},
	}, sc); err != nil {
		t.Fatal(err)
	}
	rt, _ := mgr.Get(sc, sid)
	client := &llmv1.ToolDefinition{
		Name: "client_tool", ParametersSchemaJson: `{"type":"object"}`,
		Execution: &llmv1.ToolDefinition_DelegateToClient{DelegateToClient: &llmv1.ToolExecutionDelegateToClient{}},
	}
	if err := rt.Tools.Register(client); err != nil {
		t.Fatal(err)
	}

	// Without the flag, initial_tools on an existing session changes nothing.
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId: sid, InitialTools: []*llmv1.ToolDefinition{builtin("keep", "v2")},
	}, sc); err != nil {
		t.Fatal(err)
	}
	if got := names(rt.Tools.Snapshot()); !eq(got, []string{"client_tool", "keep", "stale"}) {
		t.Fatalf("without replace_tools: %v", got)
	}

	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:    sid,
		ReplaceTools: true,
		InitialTools: []*llmv1.ToolDefinition{builtin("keep", "v2"), builtin("added", "v1")},
	}, sc); err != nil {
		t.Fatal(err)
	}
	if got := names(rt.Tools.Snapshot()); !eq(got, []string{"added", "client_tool", "keep"}) {
		t.Fatalf("after replace_tools: %v", got)
	}
	if k, _ := rt.Tools.Get("keep"); k.GetDescription() != "v2" {
		t.Fatalf("keep not replaced: %q", k.GetDescription())
	}

	row, err := repo.GetSession(ctx, sc, sid)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.UnmarshalToolsJSON(row.ToolsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(persisted); !eq(got, []string{"added", "client_tool", "keep"}) {
		t.Fatalf("persisted tools: %v", got)
	}
}

// A bad schema refuses the whole replacement and leaves the session as it was.
func TestEnsureSession_ReplaceTools_InvalidLeavesSessionAlone(t *testing.T) {
	t.Parallel()
	mgr, _ := testManagerWithSQLite(t)
	ctx := context.Background()
	sc := scopeFor("workspace-1")
	const sid = "sess-replace-bad"
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId: sid, InitialTools: []*llmv1.ToolDefinition{builtin("keep", "v1")},
	}, sc); err != nil {
		t.Fatal(err)
	}
	bad := builtin("broken", "x")
	bad.ParametersSchemaJson = `{not json`
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId: sid, ReplaceTools: true, InitialTools: []*llmv1.ToolDefinition{bad},
	}, sc); err == nil {
		t.Fatal("invalid schema accepted")
	}
	rt, _ := mgr.Get(sc, sid)
	if got := names(rt.Tools.Snapshot()); !eq(got, []string{"keep"}) {
		t.Fatalf("session changed by a refused replace: %v", got)
	}
}
