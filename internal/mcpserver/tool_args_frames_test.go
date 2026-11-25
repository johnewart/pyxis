package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/toolargs"
	"github.com/johnewart/pyxis/internal/tooldelegate"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// callMCPToolCapturingStarted runs one MCP tools/call with the given arguments
// and returns the ToolExecutionStarted frame it produced.
func callMCPToolCapturingStarted(t *testing.T, args map[string]any) *llmv1.EvToolExecutionStarted {
	t.Helper()

	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(hook.Close)

	mgr, db := testManager(t)
	llmSrv := &server.LlmServer{
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		ToolWaits:       tooldelegate.NewRegistry(),
	}

	lis := bufconn.Listen(grpcMcpBufSize)
	gs := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(gs, llmSrv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	const workspaceID = "ws-tool-args"
	const agentID = "agent_tool_args"
	const sessionID = "sess-tool-args"

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
	if _, err := gclient.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: agentID}); err != nil {
		t.Fatal(err)
	}
	if _, err := gclient.RegisterSessionTool(ctx, &llmv1.RegisterSessionToolRequest{
		SessionId: agentID,
		Tool: &llmv1.ToolDefinition{
			Name:                 "read_code",
			ParametersSchemaJson: `{"type":"object","properties":{}}`,
			Execution: &llmv1.ToolDefinition_HttpWebhook{
				HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: hook.URL},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	ma, ok := mgr.Get(serverutil.Scope{TenantID: workspaceID}, agentID)
	if !ok {
		t.Fatal("agent runtime not loaded after EnsureSession")
	}
	var mu sync.Mutex
	var frames []*llmv1.ConversationStreamFrame
	unsubscribe := ma.Core.Session(sessionID).Subscribe(func(f *llmv1.ConversationStreamFrame) {
		mu.Lock()
		frames = append(frames, f)
		mu.Unlock()
	})
	t.Cleanup(unsubscribe)

	mcp := New(Config{DB: db, SessionRuntimes: mgr, LLM: llmSrv})
	ht := httptest.NewServer(mcp.Handler())
	t.Cleanup(ht.Close)

	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "read_code", "arguments": args},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ht.URL+"/mcp", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Llmd-Agent-Id", agentID)
	req.Header.Set("X-Llmd-Session-Id", sessionID)
	serverutil.Scope{TenantID: workspaceID}.ApplyHTTPHeader(req.Header)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(frames)
		mu.Unlock()
		if n >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) == 0 {
		t.Fatal("no frames dispatched")
	}
	started := frames[0].GetToolExecutionStarted()
	if started == nil {
		t.Fatalf("frames[0] is not ToolExecutionStarted: %+v", frames[0])
	}
	return started
}

// The gap this closes: an MCP-dispatched tool call is the CLI providers' only
// path to llmd, and it emits no EvToolCallStreamFinished, so before this the
// UI had a tool name and nothing else — no way to say which file was read.
func TestMCPToolCall_StartedFrameCarriesArguments(t *testing.T) {
	started := callMCPToolCapturingStarted(t, map[string]any{
		"path":       "foo.go",
		"start_line": 15,
		"end_line":   200,
	})

	if started.GetArgumentsJson() == "" {
		t.Fatal("arguments_json is empty; the UI cannot describe the call")
	}
	if started.GetArgumentsTruncated() {
		t.Error("arguments_truncated = true for a small payload")
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(started.GetArgumentsJson()), &got); err != nil {
		t.Fatalf("arguments_json is not valid JSON: %v (%q)", err, started.GetArgumentsJson())
	}
	if got["path"] != "foo.go" {
		t.Errorf("path = %v, want foo.go", got["path"])
	}
	// JSON numbers decode as float64.
	if got["start_line"] != float64(15) || got["end_line"] != float64(200) {
		t.Errorf("line range = %v..%v, want 15..200", got["start_line"], got["end_line"])
	}
}

// A call with no arguments leaves the field unset rather than shipping "{}".
func TestMCPToolCall_EmptyArgumentsAreOmitted(t *testing.T) {
	started := callMCPToolCapturingStarted(t, map[string]any{})

	if started.GetArgumentsTruncated() {
		t.Error("arguments_truncated = true for empty arguments")
	}
	if args := started.GetArgumentsJson(); args != "" && args != "{}" {
		t.Errorf("arguments_json = %q, want empty or {}", args)
	}
}

// A file-sized argument (write_code's content) is clipped and flagged, so the
// per-tool-call event log stays bounded.
func TestMCPToolCall_OversizedArgumentsAreTruncatedAndFlagged(t *testing.T) {
	started := callMCPToolCapturingStarted(t, map[string]any{
		"path":    "big.go",
		"content": strings.Repeat("x", 64*1024),
	})

	if !started.GetArgumentsTruncated() {
		t.Fatal("arguments_truncated = false for a 64KiB payload")
	}
	if n := len(started.GetArgumentsJson()); n > toolargs.MaxDisplayBytes {
		t.Errorf("arguments_json is %d bytes, want <= %d", n, toolargs.MaxDisplayBytes)
	}
	// The identifying field still has to be readable — that's the whole point.
	if !strings.Contains(started.GetArgumentsJson(), "big.go") {
		t.Error("path was lost to truncation; no label can be built")
	}
}
