package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnewart/pyxis/agentpid"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/history"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/tooldelegate"
	"github.com/johnewart/pyxis/toolregistry"
	"gorm.io/gorm"
)

func strPtr(s string) *string { return &s }

// testTenant is the tenant most mcpserver tests ensure their sessions under.
const testTenant = "test_workspace"

func testManager(t *testing.T) (*sessionmanager.Manager, *gorm.DB) {
	t.Helper()
	db := store.NewTestSQLite(t)
	sessionRepo := store.NewSessionRepo(db)
	mgr, err := sessionmanager.New(session.HubOptions{}, sessionmanager.Options{
		SessionPersist: sessionRepo,
		NewSessionHistory: func(sc serverutil.Scope, agentID string) history.Store {
			return store.NewHistory(db, sc, agentID)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return mgr, db
}

func TestMCP_toolsCall_webhook(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("mcp-ok"))
	}))
	t.Cleanup(hook.Close)

	mgr, db := testManager(t)
	ctx := context.Background()
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: "a1"}, serverutil.Scope{TenantID: testTenant}); err != nil {
		t.Fatal(err)
	}
	ma, ok := mgr.Get(serverutil.Scope{TenantID: testTenant}, "a1")
	if !ok {
		t.Fatal("agent not found")
	}
	if err := ma.Tools.Register(&llmv1.ToolDefinition{
		Name:                 "echo_hook",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: hook.URL},
		},
	}); err != nil {
		t.Fatal(err)
	}

	llm := &server.LlmServer{
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		ToolWaits:       tooldelegate.NewRegistry(),
	}
	srv := New(Config{
		DB:              db,
		SessionRuntimes: mgr,
		LLM:             llm,
	})
	ht := httptest.NewServer(srv.Handler())
	t.Cleanup(ht.Close)

	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "echo_hook",
			"arguments": map[string]any{},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ht.URL+"/mcp", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Llmd-Agent-Id", "a1")
	// resolveAgentScope requires the workspace; agent a1 is owned by "test_workspace".
	serverutil.Scope{TenantID: "test_workspace"}.ApplyHTTPHeader(req.Header)

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
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		t.Fatalf("rpc error: %s", out.Error.Message)
	}
	if out.Result.IsError {
		t.Fatal("unexpected isError")
	}
	if len(out.Result.Content) != 1 || out.Result.Content[0].Text != "mcp-ok" {
		t.Fatalf("got %+v", out.Result.Content)
	}
}

func TestMCP_sessionByPpid_tenantScope(t *testing.T) {
	reg := agentpid.NewRegistry()
	reg.Register(4242, agentpid.Run{
		Scope:             serverutil.Scope{TenantID: "u1", ScopeID: "s1"},
		AgentID:           "agent1",
		SessionID:         "sess1",
		ClientWorkspaceID: "ws-uuid-1",
	})
	srv := New(Config{AgentPIDRegistry: reg})
	ht := httptest.NewServer(srv.Handler())
	t.Cleanup(ht.Close)

	req, err := http.NewRequest(http.MethodGet, ht.URL+"/mcp/session-by-ppid?ppid=4242", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if got := out["tenantId"]; got != "u1" {
		t.Fatalf("tenantId=%q want u1 (full %+v)", got, out)
	}
	if got := out["scopeId"]; got != "s1" {
		t.Fatalf("scopeId=%q want s1 (full %+v)", got, out)
	}
	if _, ok := out["workspaceId"]; ok {
		t.Fatalf("unexpected workspaceId key (full %+v)", out)
	}
	if got := out["clientWorkspaceId"]; got != "ws-uuid-1" {
		t.Fatalf("clientWorkspaceId=%q want ws-uuid-1 (full %+v)", got, out)
	}
}
