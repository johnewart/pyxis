//go:build integration

package claudecodeintegration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const integrationMCPServerName = "integration"

// integrationMCPAllowedToolsWildcard is passed to Claude Code --allowed-tools for the test MCP server.
const integrationMCPAllowedToolsWildcard = "mcp__" + integrationMCPServerName + "__*"

// integrationMCPStub is a minimal HTTP MCP JSON-RPC server (POST /mcp only, like llmd mcpserver).
type integrationMCPStub struct {
	mu            sync.Mutex
	workDir       string
	writeCalls    int
	initCalls     int
	toolsListCalls int
	lastPath      string
	lastContent   string
	server        *httptest.Server
	configPath    string
}

func startIntegrationMCPStub(t *testing.T, workDir string) *integrationMCPStub {
	t.Helper()
	s := &integrationMCPStub{workDir: workDir}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", s.handleMCPPost)
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)

	type mcpServerEntry struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	payload := struct {
		MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	}{
		MCPServers: map[string]mcpServerEntry{
			integrationMCPServerName: {Type: "http", URL: s.server.URL + "/mcp"},
		},
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := os.CreateTemp("", "llmd-integration-mcp-*.json")
	if err != nil {
		t.Fatal(err)
	}
	s.configPath = tmp.Name()
	t.Cleanup(func() { _ = os.Remove(s.configPath) })
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s *integrationMCPStub) writeCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeCalls
}

func (s *integrationMCPStub) initCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initCalls
}

func (s *integrationMCPStub) toolsListCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolsListCalls
}

func (s *integrationMCPStub) handleMCPPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		integrationWriteRPCError(w, nil, -32700, "read body")
		return
	}
	var req struct {
		JSONRPC string           `json:"jsonrpc"`
		ID      *json.RawMessage `json:"id,omitempty"`
		Method  string           `json:"method"`
		Params  json.RawMessage  `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		integrationWriteRPCError(w, nil, -32700, "parse error")
		return
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		integrationWriteRPCError(w, req.ID, -32600, "invalid request: jsonrpc must be 2.0")
		return
	}
	if req.ID == nil {
		if strings.HasPrefix(req.Method, "notifications/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		integrationWriteRPCError(w, nil, -32600, "missing id")
		return
	}
	switch req.Method {
	case "initialize":
		s.mu.Lock()
		s.initCalls++
		s.mu.Unlock()
		integrationWriteRPCResult(w, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    integrationMCPServerName,
				"version": "0.0.1",
			},
		})
	case "tools/list":
		s.mu.Lock()
		s.toolsListCalls++
		s.mu.Unlock()
		integrationWriteRPCResult(w, req.ID, map[string]any{
			"tools": []map[string]any{
				{
					"name":        "write_file",
					"description": "Write text content to a file path relative to the workspace root.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path":    map[string]any{"type": "string", "description": "Relative file path"},
							"content": map[string]any{"type": "string", "description": "File content"},
						},
						"required": []string{"path", "content"},
					},
				},
			},
		})
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			integrationWriteRPCError(w, req.ID, -32602, "invalid params")
			return
		}
		if params.Name != "write_file" {
			integrationWriteRPCError(w, req.ID, -32602, "unknown tool: "+params.Name)
			return
		}
		path, _ := params.Arguments["path"].(string)
		content, _ := params.Arguments["content"].(string)
		path = strings.TrimSpace(path)
		if path == "" {
			integrationWriteRPCResult(w, req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "path is required"}},
				"isError": true,
			})
			return
		}
		abs := filepath.Join(s.workDir, filepath.Clean(path))
		if !strings.HasPrefix(abs, filepath.Clean(s.workDir)+string(os.PathSeparator)) && abs != filepath.Clean(s.workDir) {
			integrationWriteRPCResult(w, req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "path escapes workspace"}},
				"isError": true,
			})
			return
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			integrationWriteRPCResult(w, req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			})
			return
		}
		s.mu.Lock()
		s.writeCalls++
		s.lastPath = path
		s.lastContent = content
		s.mu.Unlock()
		integrationWriteRPCResult(w, req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("wrote %d bytes to %s", len(content), path)}},
			"isError": false,
		})
	default:
		integrationWriteRPCError(w, req.ID, -32601, "method not found: "+req.Method)
	}
}

func integrationWriteRPCResult(w http.ResponseWriter, id *json.RawMessage, result any) {
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func integrationWriteRPCError(w http.ResponseWriter, id *json.RawMessage, code int, message string) {
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
