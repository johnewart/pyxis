package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"gorm.io/gorm"
)

const failingProbeTool = "probe_fail_tool"

// usageHarness is the disabled-tools harness plus a failing webhook tool and a
// ToolUsage repo on its own database.
func usageHarness(t *testing.T) (*httptest.Server, *server.LlmServer, *gorm.DB) {
	t.Helper()
	ht, _, llm := mcpDisabledToolsHarnessWithLLM(t)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(bad.Close)
	if err := llm.Tools.Register(&llmv1.ToolDefinition{
		Name:                 failingProbeTool,
		Description:          "always fails",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
		Classification:       llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: bad.URL},
		},
	}); err != nil {
		t.Fatal(err)
	}
	db := store.NewTestSQLite(t)
	llm.ToolUsage = store.NewToolUsageRepo(db)
	return ht, llm, db
}

func callTool(t *testing.T, ht *httptest.Server, name string) (int, mcpEnvelope) {
	t.Helper()
	return postMCP(t, ht, controlAgentID, "tools/call", map[string]any{"name": name, "arguments": map[string]any{}})
}

// callToolWithSession is callTool with the X-Llmd-Session-Id header set.
func callToolWithSession(t *testing.T, ht *httptest.Server, name, sessionID string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ht.URL+"/mcp", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Llmd-Agent-Id", controlAgentID)
	req.Header.Set("X-Llmd-Session-Id", sessionID)
	serverutil.Scope{TenantID: disabledToolsWorkspace}.ApplyHTTPHeader(req.Header)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestMCP_ToolsCall_RecordsUsageRow(t *testing.T) {
	ht, _, db := usageHarness(t)
	callToolWithSession(t, ht, disabledProbeTool, "sess-mcp")
	callToolWithSession(t, ht, failingProbeTool, "sess-mcp")
	var rows []store.ToolUsageRow
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d: %+v", len(rows), rows)
	}
	if r := rows[0]; r.ToolName != disabledProbeTool || r.Source != server.ToolUsageSourceMCP || !r.Success || r.SessionID != "sess-mcp" {
		t.Fatalf("ok row: %+v", r)
	}
	if r := rows[1]; r.ToolName != failingProbeTool || r.Source != server.ToolUsageSourceMCP || r.Success || r.SessionID != "sess-mcp" {
		t.Fatalf("failing row: %+v", r)
	}
}

func TestMCP_ToolsCall_FailingUsageStoreDoesNotChangeResult(t *testing.T) {
	goodHT, _, _ := usageHarness(t)
	badHT, _, badDB := usageHarness(t)
	if err := badDB.Migrator().DropTable(&store.ToolUsageRow{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{disabledProbeTool, failingProbeTool} {
		wantStatus, want := callTool(t, goodHT, name)
		gotStatus, got := callTool(t, badHT, name)
		if gotStatus != wantStatus || string(got.Result) != string(want.Result) || (got.Error == nil) != (want.Error == nil) {
			t.Fatalf("%s: failing store changed result: got %d %s, want %d %s", name, gotStatus, got.Result, wantStatus, want.Result)
		}
	}
}
