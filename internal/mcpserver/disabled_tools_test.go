package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnewart/pyxis/coretools"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolregistry"
)

// These tests pin the MCP transport's adoption of the per-session disabled
// tool set: tools/list, resources/read (llmd://context) and tools/call all go
// through toolmerge.MergeDefinitionsExcluding with the session's
// DisabledToolSet(). The rule itself is pinned in internal/toolmerge; here we
// only prove each MCP surface honours it and that core tools never vanish.

const (
	disabledToolsWorkspace = "ws-mcp-disabled-tools"
	disabledProbeTool      = "probe_tool"
	disabledAgentID        = "agent_disabled"
	controlAgentID         = "agent_control"
)

// mcpDisabledToolsHarness wires a server-wide registry carrying the full core
// set plus a fake server-wide probe_tool, ensures one session with probe_tool
// disabled and one control session with no exclusions, and serves the MCP
// handler. The manager is returned so tests can reach the session's own
// registry for the explicit-registration override case.
func mcpDisabledToolsHarness(t *testing.T) (*httptest.Server, *sessionmanager.Manager) {
	t.Helper()
	ht, mgr, _ := mcpDisabledToolsHarnessWithLLM(t)
	return ht, mgr
}

// mcpDisabledToolsHarnessWithLLM is mcpDisabledToolsHarness that also returns
// the LlmServer backing the MCP handler.
func mcpDisabledToolsHarnessWithLLM(t *testing.T) (*httptest.Server, *sessionmanager.Manager, *server.LlmServer) {
	t.Helper()
	mgr, db := testManager(t)

	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("probe-ok"))
	}))
	t.Cleanup(hook.Close)

	llmSrv := &server.LlmServer{
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		SessionRepo:     store.NewSessionRepo(db),
	}
	if err := server.InstallCoreTools(llmSrv, toolexec.SessionToolsDeps{
		Sessions:    store.NewSessionRepo(db),
		Events:      store.NewSessionEventRepo(db),
		Turns:       store.NewTurnRegister(db),
		Attachments: store.NewChatAttachmentRepo(db),
	}); err != nil {
		t.Fatalf("InstallCoreTools: %v", err)
	}
	if err := llmSrv.Tools.Register(&llmv1.ToolDefinition{
		Name:                 disabledProbeTool,
		Description:          "server-wide probe tool",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
		Classification:       llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: hook.URL},
		},
	}); err != nil {
		t.Fatalf("register probe_tool: %v", err)
	}

	ctx := context.Background()
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId:     disabledAgentID,
		DisabledTools: []string{disabledProbeTool},
	}, serverutil.Scope{TenantID: disabledToolsWorkspace}); err != nil {
		t.Fatalf("EnsureSession(%s): %v", disabledAgentID, err)
	}
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
		SessionId: controlAgentID,
	}, serverutil.Scope{TenantID: disabledToolsWorkspace}); err != nil {
		t.Fatalf("EnsureSession(%s): %v", controlAgentID, err)
	}

	mcp := New(Config{DB: db, SessionRuntimes: mgr, LLM: llmSrv})
	ht := httptest.NewServer(mcp.Handler())
	t.Cleanup(ht.Close)
	return ht, mgr, llmSrv
}

type mcpEnvelope struct {
	Result json.RawMessage `json:"result"`
	Error  *jsonRPCError   `json:"error"`
}

// postMCP sends one JSON-RPC request for agentID and returns the HTTP status
// plus the decoded envelope. It never fails on a non-200 status: callers that
// expect an error assert on it explicitly.
func postMCP(t *testing.T, ht *httptest.Server, agentID, method string, params any) (int, mcpEnvelope) {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
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
	req.Header.Set("X-Llmd-Agent-Id", agentID)
	serverutil.Scope{TenantID: disabledToolsWorkspace}.ApplyHTTPHeader(req.Header)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out mcpEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s response: %v", method, err)
	}
	return resp.StatusCode, out
}

// listToolNames runs tools/list for agentID and returns the advertised names.
func listToolNames(t *testing.T, ht *httptest.Server, agentID string) []string {
	t.Helper()
	status, env := postMCP(t, ht, agentID, "tools/list", nil)
	if status != http.StatusOK {
		t.Fatalf("tools/list status %d", status)
	}
	if env.Error != nil {
		t.Fatalf("tools/list rpc error: %+v", env.Error)
	}
	var res struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	return names
}

// contextToolNames reads llmd://context for agentID and returns the tool names
// embedded in the context JSON (the buildContextJSON merge site).
func contextToolNames(t *testing.T, ht *httptest.Server, agentID string) []string {
	t.Helper()
	status, env := postMCP(t, ht, agentID, "resources/read", map[string]any{"uri": ContextResourceURI})
	if status != http.StatusOK {
		t.Fatalf("resources/read status %d", status)
	}
	if env.Error != nil {
		t.Fatalf("resources/read rpc error: %+v", env.Error)
	}
	var res struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatalf("decode resources/read result: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("expected 1 content entry, got %d", len(res.Contents))
	}
	var payload struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &payload); err != nil {
		t.Fatalf("decode context payload: %v", err)
	}
	names := make([]string, 0, len(payload.Tools))
	for _, tl := range payload.Tools {
		names = append(names, tl.Name)
	}
	return names
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func assertEveryCoreTool(t *testing.T, names []string, where string) {
	t.Helper()
	for _, core := range coretools.Names() {
		if !containsName(names, core) {
			t.Errorf("%s: core tool %q missing; got %v", where, core, names)
		}
	}
}

func TestMCP_DisabledTools_ToolsListOmitsDisabledServerTool(t *testing.T) {
	ht, _ := mcpDisabledToolsHarness(t)

	names := listToolNames(t, ht, disabledAgentID)
	if containsName(names, disabledProbeTool) {
		t.Fatalf("tools/list for %s should omit %q; got %v", disabledAgentID, disabledProbeTool, names)
	}
	assertEveryCoreTool(t, names, "tools/list disabled session")

	// Control: the same server-wide tool is advertised to a session without
	// exclusions, so the omission above is the disabled set, not registration.
	control := listToolNames(t, ht, controlAgentID)
	if !containsName(control, disabledProbeTool) {
		t.Fatalf("tools/list for %s should include %q; got %v", controlAgentID, disabledProbeTool, control)
	}
	assertEveryCoreTool(t, control, "tools/list control session")
}

func TestMCP_DisabledTools_ContextResourceOmitsDisabledServerTool(t *testing.T) {
	ht, _ := mcpDisabledToolsHarness(t)

	names := contextToolNames(t, ht, disabledAgentID)
	if containsName(names, disabledProbeTool) {
		t.Fatalf("llmd://context for %s should omit %q; got %v", disabledAgentID, disabledProbeTool, names)
	}
	assertEveryCoreTool(t, names, "resources/read disabled session")

	control := contextToolNames(t, ht, controlAgentID)
	if !containsName(control, disabledProbeTool) {
		t.Fatalf("llmd://context for %s should include %q; got %v", controlAgentID, disabledProbeTool, control)
	}
}

// Disabling a core tool name is a no-op at the MCP layer: the exemption in
// MergeDefinitionsExcluding keeps set_status advertised.
func TestMCP_DisabledTools_CoreToolCannotBeHidden(t *testing.T) {
	ht, mgr := mcpDisabledToolsHarness(t)
	const agent = "agent_core_disabled"
	if err := mgr.EnsureSession(context.Background(), &llmv1.EnsureSessionRequest{
		SessionId:     agent,
		DisabledTools: []string{coretools.SetStatus},
	}, serverutil.Scope{TenantID: disabledToolsWorkspace}); err != nil {
		t.Fatal(err)
	}
	names := listToolNames(t, ht, agent)
	if !containsName(names, coretools.SetStatus) {
		t.Fatalf("core tool %q must survive a disable; got %v", coretools.SetStatus, names)
	}
	assertEveryCoreTool(t, names, "tools/list core-disable session")
}

// ask_user is core but optional: disabled, the MCP surface (Claude Code,
// Cursor) neither advertises it nor runs it, and keeps every guaranteed
// core tool.
func TestMCP_DisabledTools_AskUserCanBeDisabled(t *testing.T) {
	ht, mgr := mcpDisabledToolsHarness(t)
	const agent = "agent_no_asking"
	if err := mgr.EnsureSession(context.Background(), &llmv1.EnsureSessionRequest{
		SessionId:     agent,
		DisabledTools: []string{coretools.AskUser},
	}, serverutil.Scope{TenantID: disabledToolsWorkspace}); err != nil {
		t.Fatal(err)
	}
	names := listToolNames(t, ht, agent)
	if containsName(names, coretools.AskUser) {
		t.Fatalf("tools/list still advertises disabled ask_user: %v", names)
	}
	for _, core := range coretools.Names() {
		if coretools.IsGuaranteed(core) && !containsName(names, core) {
			t.Errorf("guaranteed core tool %q missing: %v", core, names)
		}
	}
	status, env := postMCP(t, ht, agent, "tools/call", map[string]any{
		"name": coretools.AskUser, "arguments": map[string]any{"title": "Proceed?", "type": "text"},
	})
	if status == http.StatusOK && env.Error == nil {
		t.Fatalf("tools/call ran disabled ask_user: %s", string(env.Result))
	}
	// The control session still has it.
	if !containsName(listToolNames(t, ht, controlAgentID), coretools.AskUser) {
		t.Error("control session lost ask_user")
	}
}

func TestMCP_DisabledTools_ToolsCallRefusesDisabledServerTool(t *testing.T) {
	ht, _ := mcpDisabledToolsHarness(t)
	params := map[string]any{"name": disabledProbeTool, "arguments": map[string]any{}}

	status, env := postMCP(t, ht, disabledAgentID, "tools/call", params)
	if status != http.StatusBadRequest {
		t.Fatalf("tools/call on disabled tool: status %d, want 400", status)
	}
	if env.Error == nil {
		t.Fatalf("tools/call on disabled tool: expected JSON-RPC error, got result %s", string(env.Result))
	}
	if env.Error.Code != -32602 {
		t.Fatalf("tools/call on disabled tool: code %d, want -32602", env.Error.Code)
	}
	if want := "unknown tool: " + disabledProbeTool; env.Error.Message != want {
		t.Fatalf("tools/call on disabled tool: message %q, want %q", env.Error.Message, want)
	}

	// Control: the tool is found for a session with no exclusions.
	status, env = postMCP(t, ht, controlAgentID, "tools/call", params)
	if env.Error != nil && env.Error.Code == -32602 {
		t.Fatalf("tools/call for %s should find %q; got -32602 %q", controlAgentID, disabledProbeTool, env.Error.Message)
	}
	if status != http.StatusOK {
		t.Fatalf("tools/call control: status %d, error %+v", status, env.Error)
	}
}

// Deny wins on the MCP surface too: registering the disabled tool on the
// session does NOT bring it back. A control a client can lift by naming the
// tool is not a control, and session registration is reachable by any client.
func TestMCP_DisabledTools_SessionRegistrationDoesNotOverrideDisable(t *testing.T) {
	ht, mgr := mcpDisabledToolsHarness(t)

	if names := listToolNames(t, ht, disabledAgentID); containsName(names, disabledProbeTool) {
		t.Fatalf("precondition: %q should be hidden; got %v", disabledProbeTool, names)
	}

	ma, ok := mgr.Get(serverutil.Scope{TenantID: disabledToolsWorkspace}, disabledAgentID)
	if !ok {
		t.Fatalf("session %s not loaded", disabledAgentID)
	}
	if err := ma.Tools.Register(&llmv1.ToolDefinition{
		Name:                 disabledProbeTool,
		Description:          "session-scoped override",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
		Execution:            &llmv1.ToolDefinition_DelegateToClient{DelegateToClient: &llmv1.ToolExecutionDelegateToClient{}},
	}); err != nil {
		t.Fatalf("session Register: %v", err)
	}

	names := listToolNames(t, ht, disabledAgentID)
	if containsName(names, disabledProbeTool) {
		t.Fatalf("session registration unblocked disabled %q; tools/list = %v", disabledProbeTool, names)
	}
	assertEveryCoreTool(t, names, "tools/list after attempted session override")

	// And calling it anyway is refused, not merely hidden.
	status, env := postMCP(t, ht, disabledAgentID, "tools/call",
		map[string]any{"name": disabledProbeTool, "arguments": map[string]any{}})
	if status == http.StatusOK && env.Error == nil {
		t.Fatalf("tools/call on disabled %q succeeded after session registration", disabledProbeTool)
	}
}
