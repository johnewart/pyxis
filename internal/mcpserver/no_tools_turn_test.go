package mcpserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
)

// While a session's turn runs with TurnOptions.disable_tools, the MCP surface
// (how Claude Code / Cursor reach llmd's tools) advertises nothing and refuses
// every call; once the turn ends, tools come back.
func TestMCP_noToolsTurnHidesAndRefusesTools(t *testing.T) {
	ht, _, llmSrv := mcpDisabledToolsHarnessWithLLM(t)

	if len(listToolNames(t, ht, controlAgentID)) == 0 {
		t.Fatal("precondition: control session should list tools")
	}

	end := llmSrv.BeginNoToolsTurn(serverutil.Scope{TenantID: disabledToolsWorkspace}, controlAgentID)
	if names := listToolNames(t, ht, controlAgentID); len(names) != 0 {
		t.Fatalf("tools/list during a no-tools turn = %v, want none", names)
	}
	status, env := postMCP(t, ht, controlAgentID, "tools/call", map[string]any{"name": disabledProbeTool, "arguments": map[string]any{}})
	if status != http.StatusOK || env.Error != nil {
		t.Fatalf("tools/call: status=%d err=%+v", status, env.Error)
	}
	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatal(err)
	}
	if !res.IsError || len(res.Content) == 0 || res.Content[0].Text != server.NoToolsTurnRefusal {
		t.Fatalf("tools/call during a no-tools turn = %+v, want the refusal", res)
	}

	end()
	if len(listToolNames(t, ht, controlAgentID)) == 0 {
		t.Fatal("tools should be listed again after the turn ends")
	}
}
