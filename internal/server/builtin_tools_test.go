package server

import (
	"context"
	"encoding/json"
	"github.com/johnewart/pyxis/internal/serverutil"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/internal/toolresult"
	"github.com/johnewart/pyxis/toolregistry"
)

func builtinDef(t *testing.T, reg *toolregistry.Registry, name string) *llmv1.ToolDefinition {
	t.Helper()
	for _, d := range reg.Snapshot() {
		if d.GetName() == name {
			if d.GetInlineBuiltin() == nil {
				t.Fatalf("%s is registered but not as an inline builtin", name)
			}
			return d
		}
	}
	t.Fatalf("tool %q is not registered", name)
	return nil
}

// TestBuiltinTools_DispatchThroughServerSurface dispatches a host-registered
// inline builtin the way a turn does — registry lookup plus the server's tool
// execution path — and pins that the workspace bound to the request reaches the
// registered handler's Env.Invocation.Scope untouched.
func TestBuiltinTools_DispatchThroughServerSurface(t *testing.T) {
	const ws = "user-builtins"
	ctx := serverutil.Scope{TenantID: ws}.IncomingContext(context.Background())

	relatedByWorkspace := map[string]map[string][]string{ws: {"src/a.go": {"src/b.go"}}}

	reg := toolregistry.New()
	handlers := toolexec.NewHandlers()
	if _, err := InstallBuiltinTools(reg, handlers, registerFakeRelatedTool(relatedByWorkspace)); err != nil {
		t.Fatal(err)
	}

	srv := &LlmServer{
		Tools:           reg,
		Handlers:        handlers,
		SessionRuntimes: &sessionmanager.Manager{},
	}

	dispatch := func(name, args string) string {
		t.Helper()
		def := builtinDef(t, reg, name)
		content, ok, err := srv.executeToolForStream(ctx, "agent-1", "sess-1", "", def,
			&llmv1.ToolCall{Id: "tc-" + name, Name: name, ArgumentsJson: args},
			false, nil, toolresult.Settings{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !ok {
			t.Fatalf("%s failed: %s", name, content)
		}
		return content
	}

	// The workspace bound to the request must reach the handler, and the data
	// seeded for that workspace must come back unmodified.
	got := dispatch("host_related", `{"key":"src/a.go"}`)
	var payload fakeRelatedPayload
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("host_related decode: %v", err)
	}
	if payload.Status == "" {
		t.Fatalf("host_related: no status in %s", got)
	}
	if payload.WorkspaceID != ws {
		t.Fatalf("host_related: expected workspace %q bound, got %q", ws, payload.WorkspaceID)
	}
	if len(payload.Related) != 1 || payload.Related[0] != "src/b.go" {
		t.Fatalf("host_related: expected related [src/b.go], got %v", payload.Related)
	}
}
