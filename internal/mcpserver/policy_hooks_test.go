package mcpserver

// Policy hook enforcement on the MCP tools/call path. Needs one field the
// wiring must add: Config.PolicyHooks (a policyhook.Hooks; nil means no-op).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/policyhook"
	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/toolregistry"
)

func TestMCPToolsCallPolicyDeny(t *testing.T) {
	mgr, db := testManager(t)

	var dispatched int32
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&dispatched, 1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("probe-ok"))
	}))
	t.Cleanup(hook.Close)

	llmSrv := &server.LlmServer{
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		SessionRepo:     store.NewSessionRepo(db),
	}
	if err := llmSrv.Tools.Register(&llmv1.ToolDefinition{
		Name:                 disabledProbeTool,
		Description:          "probe",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
		Classification:       llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: hook.URL},
		},
	}); err != nil {
		t.Fatalf("register probe tool: %v", err)
	}
	if err := mgr.EnsureSession(context.Background(), &llmv1.EnsureSessionRequest{SessionId: controlAgentID},
		serverutil.Scope{TenantID: disabledToolsWorkspace}); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	var sawEntry atomic.Int32
	hooks := policyhook.Funcs{CheckToolCallFn: func(_ context.Context, r policyhook.ToolCallRequest) (policyhook.Verdict, error) {
		if r.EntryPath == llmv1.PolicyEntryPath_POLICY_ENTRY_PATH_MCP_SERVER {
			sawEntry.Add(1)
		}
		return policyhook.Verdict{Decision: policyhook.DecisionDeny, Reason: "mcp call blocked by policy"}, nil
	}}
	mcp := New(Config{DB: db, SessionRuntimes: mgr, LLM: llmSrv, PolicyHooks: hooks})
	ht := httptest.NewServer(mcp.Handler())
	t.Cleanup(ht.Close)

	status, env := postMCP(t, ht, controlAgentID, "tools/call", map[string]any{
		"name": disabledProbeTool, "arguments": map[string]any{},
	})
	isToolError := env.Error != nil
	if !isToolError && strings.Contains(string(env.Result), `"isError":true`) {
		isToolError = true
	}
	if !isToolError {
		t.Fatalf("DENY did not produce an error: status=%d result=%s", status, string(env.Result))
	}
	if !strings.Contains(string(env.Result), "mcp call blocked by policy") && (env.Error == nil || !strings.Contains(env.Error.Message, "mcp call blocked by policy")) {
		t.Fatalf("error does not carry the reason: result=%s err=%+v", string(env.Result), env.Error)
	}
	if n := atomic.LoadInt32(&dispatched); n != 0 {
		t.Fatalf("denied tool was dispatched %d times", n)
	}
	if sawEntry.Load() != 1 {
		t.Fatalf("hook saw mcp_server entry path %d times, want 1", sawEntry.Load())
	}
}
