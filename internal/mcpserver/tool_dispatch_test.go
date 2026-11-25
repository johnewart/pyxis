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
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolprovider"
	"github.com/johnewart/pyxis/internal/toolsession"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// fakeToolProvider stands in for a connected client on the hub's bidi
// stream: it records each ToolExecutionInvoke and answers it via DeliverResult.
type fakeToolProvider struct {
	grpc.ServerStream
	hub    *toolprovider.Hub
	result string
	isErr  bool
	mu     sync.Mutex
	seen   []*llmv1.ToolExecutionInvoke
}

func (f *fakeToolProvider) Send(m *llmv1.ToolServerToProvider) error {
	if inv := m.GetInvoke(); inv != nil {
		f.mu.Lock()
		f.seen = append(f.seen, inv)
		f.mu.Unlock()
		go f.hub.DeliverResult(inv.GetExecutionId(), inv.GetToolId(), f.result, f.isErr, "", nil, false)
	}
	return nil
}

func (f *fakeToolProvider) Recv() (*llmv1.ToolProviderToServer, error) { select {} }

func (f *fakeToolProvider) invocations() []*llmv1.ToolExecutionInvoke {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*llmv1.ToolExecutionInvoke, len(f.seen))
	copy(out, f.seen)
	return out
}

// spyIngestor captures the ToolInvocationEnvelope handed to the delegated-result
// chokepoint so tests can assert what dispatch stamped (e.g. turn_seq).
type spyIngestor struct {
	mu   sync.Mutex
	last *toolsession.ToolInvocationEnvelope
}

func (s *spyIngestor) IngestDelegatedResult(_ context.Context, env toolsession.ToolInvocationEnvelope, content string, _ bool) string {
	s.mu.Lock()
	e := env
	s.last = &e
	s.mu.Unlock()
	return content
}

func (s *spyIngestor) envelope() *toolsession.ToolInvocationEnvelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// mcpDispatchHarness registers an agent + delegated tool over a real in-process
// gRPC service (so the MCP server, sharing llmSrv, sees the tool) and returns a
// live MCP HTTP server plus the fake provider.
func mcpDispatchHarness(t *testing.T, workspace string, fp *fakeToolProvider, hub *toolprovider.Hub, sessions DelegatedResultIngestor, turnReg *store.TurnRegister) *httptest.Server {
	t.Helper()
	mgr, db := testManager(t)
	if err := mgr.EnsureSession(context.Background(), &llmv1.EnsureSessionRequest{SessionId: "agent_mcp_dispatch"}, serverutil.Scope{TenantID: workspace}); err != nil {
		t.Fatal(err)
	}

	unregister := hub.Register(serverutil.Scope{TenantID: workspace}, fp)
	t.Cleanup(unregister)

	llmSrv := &server.LlmServer{
		Tools:           toolregistry.New(),
		SessionRuntimes: mgr,
		ToolProviderHub: hub,
		TurnRegister:    turnReg,
	}

	lis := bufconn.Listen(grpcMcpBufSize)
	gs := grpc.NewServer()
	llmv1.RegisterLlmServiceServer(gs, llmSrv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(func() { gs.Stop() })

	ctx := serverutil.Scope{TenantID: workspace}.OutgoingContext(context.Background())
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	gclient := llmv1.NewLlmServiceClient(conn)
	if _, err := gclient.RegisterSessionTool(ctx, &llmv1.RegisterSessionToolRequest{
		SessionId: "agent_mcp_dispatch",
		Tool: &llmv1.ToolDefinition{
			Name:                 "delegated_echo",
			ParametersSchemaJson: `{"type":"object","properties":{}}`,
			Execution:            &llmv1.ToolDefinition_DelegateToClient{DelegateToClient: &llmv1.ToolExecutionDelegateToClient{}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	mcp := New(Config{DB: db, SessionRuntimes: mgr, LLM: llmSrv, Sessions: sessions})
	ht := httptest.NewServer(mcp.Handler())
	t.Cleanup(ht.Close)
	return ht
}

type mcpToolResult struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

func callMCPTool(t *testing.T, ht *httptest.Server, workspace, sessionID string) mcpToolResult {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "delegated_echo", "arguments": map[string]any{}},
	})
	req, err := http.NewRequest(http.MethodPost, ht.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Llmd-Agent-Id", "agent_mcp_dispatch")
	serverutil.Scope{TenantID: workspace}.ApplyHTTPHeader(req.Header)
	req.Header.Set("X-Llmd-Session-Id", sessionID)
	req.Header.Set("X-Client-Workspace-Id", workspace)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		Result mcpToolResult `json:"result"`
		Error  *jsonRPCError `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		t.Fatalf("rpc error: %+v", out.Error)
	}
	return out.Result
}

// TestToolDispatch_MCP_DelegatesToProvider verifies the MCP tools/call path for a
// client-delegated tool routes through the shared ToolProviderHub to the connected
// provider and returns the provider's result to the JSON-RPC caller.
func TestToolDispatch_MCP_DelegatesToProvider(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		const workspace = "ws-mcp-dispatch-ok"
		hub := toolprovider.NewHub()
		fp := &fakeToolProvider{hub: hub, result: "provider-said-hi"}
		ht := mcpDispatchHarness(t, workspace, fp, hub, nil, nil)

		res := callMCPTool(t, ht, workspace, "sess_mcp_dispatch")
		if res.IsError {
			t.Fatalf("unexpected isError: %+v", res)
		}
		if len(res.Content) != 1 || res.Content[0].Text != "provider-said-hi" {
			t.Fatalf("unexpected MCP tool result: %+v", res.Content)
		}

		invs := fp.invocations()
		if len(invs) != 1 {
			t.Fatalf("expected provider invoked once, got %d", len(invs))
		}
		if got := invs[0].GetToolId(); got != "delegated_echo" {
			t.Fatalf("provider invoked for wrong tool: %q", got)
		}
		if got := invs[0].GetSessionId(); got != "sess_mcp_dispatch" {
			t.Fatalf("provider invoke missing session id: %q", got)
		}
	})

	t.Run("ProviderError", func(t *testing.T) {
		const workspace = "ws-mcp-dispatch-err"
		hub := toolprovider.NewHub()
		fp := &fakeToolProvider{hub: hub, result: "boom: tool failed", isErr: true}
		ht := mcpDispatchHarness(t, workspace, fp, hub, nil, nil)

		res := callMCPTool(t, ht, workspace, "sess_mcp_err")
		if !res.IsError {
			t.Fatalf("expected isError=true for a provider error, got %+v", res)
		}
		if len(res.Content) != 1 || res.Content[0].Text != "boom: tool failed" {
			t.Fatalf("expected the provider error text to pass through, got %+v", res.Content)
		}
		if len(fp.invocations()) != 1 {
			t.Fatalf("expected provider invoked once, got %d", len(fp.invocations()))
		}
	})

	// StampsTurnSeqOnEnvelope proves the last-mile: MCP dispatch captures the
	// session's current turn_seq at invocation start and stamps it onto the
	// ToolInvocationEnvelope handed to the delegated-result chokepoint.
	t.Run("StampsTurnSeqOnEnvelope", func(t *testing.T) {
		const workspace = "ws-mcp-dispatch-seq"
		db := store.NewTestSQLite(t)
		turnReg := store.NewTurnRegister(db)
		// Advance the session to turn 3 before the tool call is dispatched.
		ctx := context.Background()
		for i := 0; i < 3; i++ {
			if _, err := turnReg.Advance(ctx, serverutil.Scope{TenantID: workspace}, "sess_mcp_seq", time.Now().UTC(), ""); err != nil {
				t.Fatalf("advance: %v", err)
			}
		}

		hub := toolprovider.NewHub()
		fp := &fakeToolProvider{hub: hub, result: "ok"}
		spy := &spyIngestor{}
		ht := mcpDispatchHarness(t, workspace, fp, hub, spy, turnReg)

		callMCPTool(t, ht, workspace, "sess_mcp_seq")

		env := spy.envelope()
		if env == nil {
			t.Fatal("delegated-result ingestor never received an envelope")
		}
		if env.SessionID != "sess_mcp_seq" {
			t.Fatalf("envelope session id: got %q", env.SessionID)
		}
		if env.TurnSeq != 3 {
			t.Fatalf("dispatch must stamp the active turn_seq=3 onto the envelope, got %d", env.TurnSeq)
		}
		if env.ToolName != "delegated_echo" {
			t.Fatalf("envelope tool name: got %q", env.ToolName)
		}
	})
}
