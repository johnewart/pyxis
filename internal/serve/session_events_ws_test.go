package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/protobuf/encoding/protojson"
	"nhooyr.io/websocket"
)

// scopeQuery encodes the tenant scope as handshake query parameters.
func scopeQuery(tenant string) string {
	q := url.Values{}
	serverutil.Scope{TenantID: tenant}.ApplyQuery(q)
	return q.Encode()
}

// TestSessionEventsWS_LiveTail is the WebSocket twin of
// internal/server.TestSubscribeSessionEvents_LiveTail: a frame appended to the
// session's event log AFTER the connection's replay has already finished must
// still arrive over the open WebSocket, without reconnecting — the same
// live-delivery guarantee the gRPC SubscribeSessionEvents RPC provides, for
// hosts that would rather not carry a grpc-web streaming call open just for
// this (see session_events_ws.go's module doc). No session runtime exists:
// live delivery comes from the log itself, not from a runtime's hub.
func TestSessionEventsWS_LiveTail(t *testing.T) {
	const workspaceID = "ws-events-ws"
	const sessionID = "sess-events-ws"

	db := store.NewTestSQLite(t)
	ctx := context.Background()
	sc := serverutil.Scope{TenantID: workspaceID}

	events := store.NewSessionEventRepo(db)
	handler := NewSessionEventsWSHandler(SessionEventsWSConfig{
		SessionEvents:  events,
		Token:          "test-token",
		AllowedOrigins: []string{"*"},
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") +
		"/ws/session-events?session_id=" + sessionID +
		"&" + scopeQuery(workspaceID) +
		"&token=test-token"

	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })

	// Fresh session, no persisted events: replay sends nothing. Let the
	// handshake finish and the tail subscribe before appending. (Appending
	// earlier would still be delivered, by the replay read.)
	time.Sleep(50 * time.Millisecond)

	want := &llmv1.ConversationStreamFrame{
		Frame: &llmv1.ConversationStreamFrame_ToolExecutionStarted{
			ToolExecutionStarted: &llmv1.EvToolExecutionStarted{
				TurnId:     "turn-1",
				ToolCallId: "call-1",
				Name:       "ws_live_tail_probe",
			},
		},
	}
	// Producers only append to the durable log; the log pushes to tails.
	wantJSON, err := protojson.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.AppendEvent(ctx, sc, sessionID, string(wantJSON)); err != nil {
		t.Fatal(err)
	}

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRead()
	_, data, err := c.Read(readCtx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := &llmv1.ConversationStreamFrame{}
	if err := protojson.Unmarshal(data, got); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, data)
	}
	started := got.GetToolExecutionStarted()
	if started == nil {
		t.Fatalf("got frame is not ToolExecutionStarted: %s", data)
	}
	if started.GetName() != "ws_live_tail_probe" || started.GetToolCallId() != "call-1" {
		t.Fatalf("got %+v, want name=ws_live_tail_probe call_id=call-1", started)
	}
}

// TestSessionEventsWS_CrossOriginAllowed is the regression test for the
// bug where every cross-origin handshake was silently rejected: AllowedOrigins
// (shared with ConnectConfig, scheme-included, e.g. "http://host:port" —
// exact-string matched there) was passed straight through to
// nhooyr.io/websocket's OriginPatterns, which matches host-only (no
// scheme). A request whose Origin differs from the request Host (exactly
// what happens behind a dev-server proxy like Vite forwarding
// /host-connect to llmd with changeOrigin rewriting Host but not
// Origin) would fail Accept's origin check before any 101 response — which
// looks identical, client-side, to "connection closed before handshake".
func TestSessionEventsWS_CrossOriginAllowed(t *testing.T) {
	handler := NewSessionEventsWSHandler(SessionEventsWSConfig{
		AllowedOrigins: []string{"http://app.example.test:5174"},
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session-events?session_id=s&" + scopeQuery("w") + ""

	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Origin deliberately does NOT match srv's own host (127.0.0.1:<port>)
	// — simulating the browser's real page origin reaching llmd through a
	// proxy that rewrote the request Host but preserved Origin.
	c, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://app.example.test:5174"}},
	})
	if err != nil {
		t.Fatalf("cross-origin handshake rejected: %v", err)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
}

// TestSessionEventsWS_CrossOriginRejected confirms an origin NOT on the
// allowlist is still correctly rejected (i.e. the fix above isn't an
// accidental "allow everything").
func TestSessionEventsWS_CrossOriginRejected(t *testing.T) {
	handler := NewSessionEventsWSHandler(SessionEventsWSConfig{
		AllowedOrigins: []string{"http://app.example.test:5174"},
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session-events?session_id=s&" + scopeQuery("w") + ""

	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://evil.example.com"}},
	})
	if err == nil {
		t.Fatal("expected handshake to be rejected for a disallowed origin")
	}
}

// TestSessionEventsWS_RequiresToken confirms a mismatched/missing token is
// rejected during the HTTP handshake (before any WebSocket upgrade), same
// auth model as the Connect listener.
func TestSessionEventsWS_RequiresToken(t *testing.T) {
	handler := NewSessionEventsWSHandler(SessionEventsWSConfig{
		Token: "right-token",
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") +
		"/ws/session-events?session_id=s&" + scopeQuery("w") + "&token=wrong-token"

	dialCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(dialCtx, wsURL, nil)
	if err == nil {
		t.Fatal("expected dial to fail with a wrong token")
	}
	if resp != nil && resp.StatusCode != 401 {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
