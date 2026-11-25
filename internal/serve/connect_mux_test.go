package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestNewConnectMux_RoutesWSPathBehindArbitraryPrefix is the regression test
// for the bug where a dev-server proxy (Vite, etc.) fronting the Connect
// listener behind its own path prefix (e.g. "/host-connect") without
// stripping it caused every WebSocket handshake to fall through to the
// Connect/grpc-web handler instead of the session-events WS handler — which
// doesn't know what to do with a GET+Upgrade request and drops the
// connection, presenting client-side as an inexplicable "socket hang up".
func TestNewConnectMux_RoutesWSPathBehindArbitraryPrefix(t *testing.T) {
	var wsHit, connectHit bool
	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsHit = true
		w.WriteHeader(http.StatusOK)
	})
	connectHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectHit = true
		w.WriteHeader(http.StatusOK)
	})
	mux := NewConnectMux(wsHandler, connectHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cases := []struct {
		name        string
		path        string
		wantWS      bool
		wantConnect bool
	}{
		{"bare ws path", "/ws/session-events?session_id=s&" + scopeQuery("w") + "", true, false},
		{"proxy-prefixed ws path", "/host-connect/ws/session-events?session_id=s&" + scopeQuery("w") + "", true, false},
		{"unrelated connect RPC path", "/llmv1.LlmService/ChatStream", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wsHit, connectHit = false, false
			resp, err := http.Get(srv.URL + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if wsHit != tc.wantWS {
				t.Fatalf("wsHit = %v, want %v", wsHit, tc.wantWS)
			}
			if connectHit != tc.wantConnect {
				t.Fatalf("connectHit = %v, want %v", connectHit, tc.wantConnect)
			}
		})
	}
}
