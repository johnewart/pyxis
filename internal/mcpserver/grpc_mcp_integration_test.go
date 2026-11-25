package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/tooldelegate"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const grpcMcpBufSize = 1024 * 1024

// TestGRPCRegisterTool_MCPInvocation is an integration-ish test: a real gRPC LlmService (in-memory
// bufconn transport) registers an agent + webhook tool, then the HTTP MCP server invokes that tool
// and returns the webhook body. Verifies gRPC-registered tool state is visible to POST /mcp.
func TestGRPCRegisterTool_MCPInvocation(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("from-webhook"))
	}))
	t.Cleanup(hook.Close)

	mgr, db := testManager(t)
	tw := tooldelegate.NewRegistry()
	llmSrv := &server.LlmServer{
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		ToolWaits:       tw,
	}

	lis := bufconn.Listen(grpcMcpBufSize)
	gs := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(gs, llmSrv)
	go func() {
		_ = gs.Serve(lis)
	}()
	t.Cleanup(func() { gs.Stop() })

	// Agent RPCs now require the workspace workspace in gRPC metadata; EnsureSession
	// owns the runtime under this session id, so the HTTP tool call below must use
	// the same value in the scope header for ma.WorkspaceID to match.
	const workspaceID = "ws-grpc-mcp"
	ctx := serverutil.Scope{TenantID: workspaceID}.OutgoingContext(context.Background())
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	gclient := llmv1.NewLlmServiceClient(conn)
	if _, err := gclient.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: "agent_grpc_mcp"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gclient.RegisterSessionTool(ctx, &llmv1.RegisterSessionToolRequest{
		SessionId: "agent_grpc_mcp",
		Tool: &llmv1.ToolDefinition{
			Name:                 "echo_hook_grpc",
			ParametersSchemaJson: `{"type":"object","properties":{}}`,
			Execution: &llmv1.ToolDefinition_HttpWebhook{
				HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: hook.URL},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	mcp := New(Config{
		DB:              db,
		SessionRuntimes: mgr,
		LLM:             llmSrv,
	})
	ht := httptest.NewServer(mcp.Handler())
	t.Cleanup(ht.Close)

	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "echo_hook_grpc",
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
	req.Header.Set("X-Llmd-Agent-Id", "agent_grpc_mcp")
	serverutil.Scope{TenantID: workspaceID}.ApplyHTTPHeader(req.Header)

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
	if len(out.Result.Content) != 1 || out.Result.Content[0].Text != "from-webhook" {
		t.Fatalf("got %+v", out.Result.Content)
	}
}
