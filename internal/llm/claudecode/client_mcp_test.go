package claudecodeclillm

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"net/http"

	"github.com/johnewart/pyxis/agentpid"
	"github.com/johnewart/pyxis/internal/serverutil"
)

func TestPrepareMcpConfigPath_explicitConfigPath(t *testing.T) {
	t.Setenv("LLMD_MCP_HTTP_ADDR", ":9999")
	c := &Client{cfg: Config{McpConfigPath: "/tmp/explicit-mcp.json"}}
	path, cleanup, auto, err := c.prepareMcpConfigPath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if auto {
		t.Fatalf("expected auto=false for explicit mcp_config_path")
	}
	if path != "/tmp/explicit-mcp.json" {
		t.Fatalf("expected explicit path, got %q", path)
	}
}

func TestPrepareMcpConfigPath_autoFromHTTPAddr(t *testing.T) {
	t.Setenv("LLMD_MCP_HTTP_BASE_URL", "")
	t.Setenv("LLMD_MCP_HTTP_ADDR", ":8765")
	c := &Client{cfg: Config{}}
	path, cleanup, auto, err := c.prepareMcpConfigPath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !auto {
		t.Fatalf("expected auto=true for generated llmd HTTP MCP config")
	}
	if path == "" {
		t.Fatal("expected temp mcp config path")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		MCPServers map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	ent, ok := decoded.MCPServers["llmd"]
	if !ok {
		t.Fatalf("missing llmd server in %s", strings.TrimSpace(string(raw)))
	}
	if ent.Type != "http" {
		t.Fatalf("type: got %q want http", ent.Type)
	}
	if want := "http://127.0.0.1:8765/mcp"; ent.URL != want {
		t.Fatalf("url: got %q want %q", ent.URL, want)
	}
}

func TestPrepareMcpConfigPath_autoFromBaseURL(t *testing.T) {
	t.Setenv("LLMD_MCP_HTTP_ADDR", "")
	t.Setenv("LLMD_MCP_HTTP_BASE_URL", "http://localhost:3001")
	c := &Client{cfg: Config{}}
	path, cleanup, auto, err := c.prepareMcpConfigPath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !auto {
		t.Fatalf("expected auto=true for generated llmd HTTP MCP config")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"type": "http"`) || !strings.Contains(body, `"url": "http://localhost:3001/mcp"`) {
		t.Fatalf("unexpected file contents: %s", body)
	}
}

func TestPrepareMcpConfigPath_headersFromAgentpidContext(t *testing.T) {
	t.Setenv("LLMD_MCP_HTTP_BASE_URL", "")
	t.Setenv("LLMD_MCP_HTTP_ADDR", ":8765")
	ctx := agentpid.WithRunDetails(context.Background(), agentpid.Run{
		Scope:             serverutil.Scope{TenantID: "user-1"},
		AgentID:           "agent-1",
		SessionID:         "session-1",
		ClientWorkspaceID: "ws-1",
	})
	c := &Client{cfg: Config{}}
	path, cleanup, auto, err := c.prepareMcpConfigPath(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !auto {
		t.Fatalf("expected auto=true for generated llmd HTTP MCP config")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	ent := decoded.MCPServers["llmd"]
	if ent.Headers["X-Llmd-Agent-Id"] != "agent-1" {
		t.Fatalf("X-Llmd-Agent-Id: got %q", ent.Headers["X-Llmd-Agent-Id"])
	}
	if ent.Headers["X-Llmd-Session-Id"] != "session-1" {
		t.Fatalf("X-Llmd-Session-Id: got %q", ent.Headers["X-Llmd-Session-Id"])
	}
	hh := http.Header{}
	for k, v := range ent.Headers {
		hh.Set(k, v)
	}
	if sc, err := serverutil.ScopeFromHTTPHeader(hh); err != nil || sc.TenantID != "user-1" {
		t.Fatalf("scope header: got %+v err=%v", sc, err)
	}
	if ent.Headers["X-Client-Workspace-Id"] != "ws-1" {
		t.Fatalf("X-Client-Workspace-Id: got %q", ent.Headers["X-Client-Workspace-Id"])
	}
}

func TestBuildStreamArgsV2_autoLlmdMcpAddsAllowedToolsWildcard(t *testing.T) {
	t.Setenv("LLMD_MCP_HTTP_BASE_URL", "")
	t.Setenv("LLMD_MCP_HTTP_ADDR", ":8765")
	c := NewV2(Config{})
	base := &Client{cfg: c.cfg}
	path, cleanup, auto, err := base.prepareMcpConfigPath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !auto {
		t.Fatal("expected auto MCP")
	}
	args := c.buildStreamArgsV2(context.Background(), nil, nil, path, auto, "")
	found := false
	for i, a := range args {
		if a == "--allowed-tools" && i+1 < len(args) {
			for j := i + 1; j < len(args) && !strings.HasPrefix(args[j], "--"); j++ {
				if args[j] == llmdClaudeMcpAllowedToolsWildcard {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Fatalf("expected %q in argv after --allowed-tools, got: %v", llmdClaudeMcpAllowedToolsWildcard, args)
	}
	if !strings.Contains(strings.Join(args, " "), "--permission-mode bypassPermissions") {
		t.Fatalf("expected --permission-mode bypassPermissions for auto MCP, got: %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "--tools "+claudeHeadlessMCPEagerBuiltinTools) {
		t.Fatalf("expected --tools %q for auto MCP, got: %v", claudeHeadlessMCPEagerBuiltinTools, args)
	}
}
