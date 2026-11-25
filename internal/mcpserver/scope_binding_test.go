package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/tooldelegate"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolregistry"
)

// TestMCP_toolsCall_stampsScopeOnInvocation is a regression test for
// the bug where the MCP tools/call JSON-RPC handler built toolexec.Invocation
// without ever setting Scope, so every inline-builtin handler
// dispatched through this transport (enumerate, traverse_graph, read_code,
// ...) saw an empty inv.Scope.TenantID even though the caller's
// workspace/workspace was known (resolveAgentScope already validated it, and
// respond() stamps the very same value onto the IngestDelegatedResult
// envelope). It registers an inline-builtin handler that records
// inv.Scope.TenantID and asserts it equals the resolved workspace.
func TestMCP_toolsCall_stampsScopeOnInvocation(t *testing.T) {
	const workspace = "ws-inline-builtin-binding"

	mgr, db := testManager(t)
	ctx := context.Background()
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: "agent_ws_binding"}, serverutil.Scope{TenantID: workspace}); err != nil {
		t.Fatal(err)
	}
	ma, ok := mgr.Get(serverutil.Scope{TenantID: workspace}, "agent_ws_binding")
	if !ok {
		t.Fatal("agent not found")
	}
	if err := ma.Tools.Register(&llmv1.ToolDefinition{
		Name:                 "capture_workspace",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
		Execution: &llmv1.ToolDefinition_InlineBuiltin{
			InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var gotWorkspaceID string
	handlers := toolexec.NewHandlers()
	if err := handlers.Register("capture_workspace", func(_ context.Context, _ json.RawMessage, inv toolexec.Invocation) (toolexec.Result, error) {
		gotWorkspaceID = inv.Scope.TenantID
		return toolexec.Result{Content: "captured"}, nil
	}); err != nil {
		t.Fatal(err)
	}

	llm := &server.LlmServer{
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		Handlers:        handlers,
		ToolWaits:       tooldelegate.NewRegistry(),
	}
	srv := New(Config{DB: db, SessionRuntimes: mgr, LLM: llm})
	ht := httptest.NewServer(srv.Handler())
	t.Cleanup(ht.Close)

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "capture_workspace",
			"arguments": map[string]any{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ht.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Llmd-Agent-Id", "agent_ws_binding")
	serverutil.Scope{TenantID: workspace}.ApplyHTTPHeader(req.Header)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *jsonRPCError `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		t.Fatalf("rpc error: %+v", out.Error)
	}
	if out.Result.IsError {
		t.Fatalf("unexpected isError: %+v", out.Result)
	}
	if len(out.Result.Content) != 1 || out.Result.Content[0].Text != "captured" {
		t.Fatalf("unexpected content: %+v", out.Result.Content)
	}
	if gotWorkspaceID != workspace {
		t.Fatalf("inv.Scope.TenantID = %q, want workspace %q — MCP tools/call dispatch is not stamping Scope onto Invocation", gotWorkspaceID, workspace)
	}
}
