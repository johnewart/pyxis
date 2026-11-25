package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// TestBrowserWS_NoCompression is the regression test for iOS Safari dropping
// /ws/session-events (and /ws/session-states) with "WebSocket error": the
// server negotiated permessage-deflate, and Safari fails on compressed
// frames. A client that offers compression must get a handshake with no
// Sec-WebSocket-Extensions.
func TestBrowserWS_NoCompression(t *testing.T) {
	cases := []struct {
		name    string
		handler http.Handler
		path    string
	}{
		{
			name:    "session-events",
			handler: NewSessionEventsWSHandler(SessionEventsWSConfig{Token: "t", AllowedOrigins: []string{"*"}}),
			path:    SessionEventsWSPath + "?session_id=s1&" + scopeQuery("tenant-1") + "&token=t",
		},
		{
			name:    "session-states",
			handler: NewSessionStatesWSHandler(SessionStatesWSConfig{Token: "t", AllowedOrigins: []string{"*"}}),
			path:    SessionStatesWSPath + "?" + scopeQuery("tenant-1") + "&token=t",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + tc.path
			c, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
				CompressionMode: websocket.CompressionNoContextTakeover,
			})
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
			if ext := resp.Header.Get("Sec-WebSocket-Extensions"); ext != "" {
				t.Fatalf("server negotiated extensions %q; want none (compression must be off for iOS Safari)", ext)
			}
		})
	}
}
