// Package mcpstdio implements the stdio MCP bridge for Cursor → llmd HTTP MCP (JSON-RPC POST /mcp).
package mcpstdio

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/johnewart/pyxis/agentpid"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/llmdlog"
)

func debugEnabled() bool {
	return llmdlog.DebugEnabled()
}

// Run parses args (after subcommand) and bridges stdio JSON-RPC lines to llmd POST /mcp.
func Run(args []string) error {
	fs := flag.NewFlagSet("mcp-stdio-bridge", flag.ContinueOnError)
	baseURL := fs.String("base-url", "", "llmd MCP HTTP base URL, e.g. http://127.0.0.1:8765")
	if err := fs.Parse(args); err != nil {
		return err
	}
	base := strings.TrimSuffix(strings.TrimSpace(*baseURL), "/")
	if base == "" {
		base = strings.TrimSuffix(strings.TrimSpace(os.Getenv("LLMD_MCP_BASE_URL")), "/")
	}
	if base == "" {
		return fmt.Errorf("mcp-stdio-bridge: --base-url or LLMD_MCP_BASE_URL is required")
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	ppid := os.Getppid()
	run, err := fetchSession(client, base, ppid)
	if err != nil {
		return fmt.Errorf("session-by-ppid: %w", err)
	}
	if debugEnabled() {
		self := os.Args[0]
		if exe, err := os.Executable(); err == nil && strings.TrimSpace(exe) != "" {
			self = exe
		}
		payload := map[string]any{
			"event":               "mcp_stdio_bridge_startup",
			"executable":          self,
			"ppid":                ppid,
			"mcp_http_base_url":   base,
			"session_id":          run.SessionID,
			"scope":               run.Scope.String(),
			"agent_id":            run.AgentID,
			"client_workspace_id": run.ClientWorkspaceID,
		}
		if b, err := json.Marshal(payload); err != nil {
			llmdlog.Stdout("mcp-stdio-bridge", llmdlog.LevelWarn, "marshal startup debug payload failed: session=%q err=%v", run.SessionID, err)
		} else {
			llmdlog.Stdout("mcp-stdio-bridge", llmdlog.LevelDebug, "%s", string(b))
		}
	}

	sc := bufio.NewScanner(os.Stdin)
	const maxScan = 16 * 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, maxScan)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		respBody, status, err := forwardRPC(client, base, run, []byte(line))
		if err != nil {
			writeRPCErrorLine(line, -32603, err.Error())
			continue
		}
		if status != http.StatusOK {
			writeRPCErrorLine(line, -32603, fmt.Sprintf("HTTP %d", status))
			continue
		}
		if t := bytes.TrimSpace(respBody); len(t) > 0 {
			fmt.Println(string(t))
		}
	}
	return sc.Err()
}

func fetchSession(client *http.Client, base string, ppid int) (agentpid.Run, error) {
	u, err := url.Parse(base)
	if err != nil {
		return agentpid.Run{}, err
	}
	u = u.JoinPath("mcp", "session-by-ppid")
	q := u.Query()
	q.Set("ppid", strconv.Itoa(ppid))
	u.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return agentpid.Run{}, err
	}
	res, err := client.Do(req)
	if err != nil {
		return agentpid.Run{}, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return agentpid.Run{}, fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	var dto struct {
		SessionID         string `json:"sessionId"`
		TenantID          string `json:"tenantId"`
		ScopeID           string `json:"scopeId"`
		AgentID           string `json:"agentId"`
		ClientWorkspaceID string `json:"clientWorkspaceId"`
	}
	if err := json.Unmarshal(body, &dto); err != nil {
		return agentpid.Run{}, err
	}
	if strings.TrimSpace(dto.AgentID) == "" {
		return agentpid.Run{}, fmt.Errorf("empty agentId in session response")
	}
	return agentpid.Run{
		Scope:             serverutil.Scope{TenantID: strings.TrimSpace(dto.TenantID), ScopeID: strings.TrimSpace(dto.ScopeID)},
		AgentID:           strings.TrimSpace(dto.AgentID),
		SessionID:         strings.TrimSpace(dto.SessionID),
		ClientWorkspaceID: strings.TrimSpace(dto.ClientWorkspaceID),
	}, nil
}

func forwardRPC(client *http.Client, base string, run agentpid.Run, payload []byte) ([]byte, int, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, 0, err
	}
	u = u.JoinPath("mcp")
	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if run.Scope.TenantID != "" {
		run.Scope.ApplyHTTPHeader(req.Header)
	}
	req.Header.Set("X-Llmd-Agent-Id", run.AgentID)
	if run.SessionID != "" {
		req.Header.Set("X-Llmd-Session-Id", run.SessionID)
	}
	if run.ClientWorkspaceID != "" {
		req.Header.Set("X-Client-Workspace-Id", run.ClientWorkspaceID)
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return b, res.StatusCode, nil
}

func writeRPCErrorLine(requestLine string, code int, msg string) {
	var out map[string]any
	var req struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal([]byte(requestLine), &req)
	out = map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    code,
			"message": msg,
		},
	}
	if len(req.ID) > 0 {
		out["id"] = json.RawMessage(req.ID)
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
}
