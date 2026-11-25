package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/tooldelegate"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// TestMCPToolCall_EmitsToolExecutionEvents is the regression test for the gap
// where MCP-dispatched tool calls (Claude Code CLI, Cursor CLI, and any other
// agentic driver reaching llmd only via tools/call) executed and persisted
// their result but never surfaced ToolExecutionStarted/ToolExecutionFinished
// on the session's conversation event stream — a client subscribed via
// ChatStream/SubscribeSessionEvents saw thinking/text deltas around the tool
// call but no sign a tool ran at all. Verifies handleToolsCall now dispatches
// both frames, in order, with the right tool call id/name/success, through
// the exact same *session.Hub ChatStream reads from
// (SessionRuntimes.Get(agentID).Core.Session(sessionID)).
func TestMCPToolCall_EmitsToolExecutionEvents(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	const workspaceID = "ws-tool-events"
	const agentID = "agent_tool_events"
	const sessionID = "sess-tool-events"

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
			Name:                 "echo_hook_events",
			ParametersSchemaJson: `{"type":"object","properties":{}}`,
			Execution: &llmv1.ToolDefinition_HttpWebhook{
				HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: hook.URL},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Subscribe to the exact same *session.Hub ChatStream would read from,
	// via the same SessionRuntimes manager the MCP server is configured with.
	ma, ok := mgr.Get(serverutil.Scope{TenantID: workspaceID}, agentID)
	if !ok {
		t.Fatal("agent runtime not loaded after EnsureSession")
	}
	hub := ma.Core.Session(sessionID)
	var mu sync.Mutex
	var frames []*llmv1.ConversationStreamFrame
	unsubscribe := hub.Subscribe(func(f *llmv1.ConversationStreamFrame) {
		mu.Lock()
		frames = append(frames, f)
		mu.Unlock()
	})
	t.Cleanup(unsubscribe)

	mcp := New(Config{DB: db, SessionRuntimes: mgr, LLM: llmSrv})
	ht := httptest.NewServer(mcp.Handler())
	t.Cleanup(ht.Close)

	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "echo_hook_events",
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
	var out struct {
		Result struct {
			Content []struct {
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

	// Dispatch happens synchronously inside handleToolsCall's respond()
	// closure, which writeToolCallContent (the HTTP response) is written
	// from too — but give the subscriber callback a beat regardless, since
	// Hub.Dispatch's own locking/notification isn't part of this test's
	// contract to assume is synchronous forever.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(frames)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) < 2 {
		t.Fatalf("expected at least 2 frames (started + finished), got %d: %+v", len(frames), frames)
	}
	started := frames[0].GetToolExecutionStarted()
	if started == nil {
		t.Fatalf("frames[0] is not ToolExecutionStarted: %+v", frames[0])
	}
	if started.GetName() != "echo_hook_events" {
		t.Errorf("started.Name = %q, want echo_hook_events", started.GetName())
	}
	if started.GetToolCallId() == "" {
		t.Error("started.ToolCallId is empty")
	}

	finished := frames[1].GetToolExecutionFinished()
	if finished == nil {
		t.Fatalf("frames[1] is not ToolExecutionFinished: %+v", frames[1])
	}
	if finished.GetName() != "echo_hook_events" {
		t.Errorf("finished.Name = %q, want echo_hook_events", finished.GetName())
	}
	if finished.GetToolCallId() != started.GetToolCallId() {
		t.Errorf("finished.ToolCallId = %q, want %q (same as started)", finished.GetToolCallId(), started.GetToolCallId())
	}
	if !finished.GetSuccess() {
		t.Errorf("finished.Success = false, want true (error=%q)", finished.GetErrorMessage())
	}
}
