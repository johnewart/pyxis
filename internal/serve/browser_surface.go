package serve

// The browser-facing HTTP surface, as ONE handler.
//
// llmd's browser surface is two things on a single listener: the
// Connect/grpc-web handler (connect_listener.go) and the session-events
// WebSocket endpoint (session_events_ws.go). They are not independent
// features a host may pick from — a browser renderer needs both, because `subscribeSessionEvents` is the only live feed of
// MCP-dispatched tool activity for CLI-backed providers and it has no
// grpc-web fallback.
//
// This file exists because wiring them separately is a trap that has now
// cost real debugging time. A composition root that calls only
// NewConnectHandler gets a listener that answers every RPC correctly and
// silently has no /ws/session-events route at all. The renderer's WebSocket
// then fails with "closed before handshake"/"closed early" — and llmd logs
// NOTHING, because the code that would log the rejection is the code that
// was never wired. Nothing about the working RPC path hints that half the
// surface is missing.
//
// llmd has more than one composition root (cmd/llmd is its own module and
// its own binary; an embedding host links these services into its own process),
// so "remember to also wire the WebSocket" is not a property any single
// main() can enforce. NewBrowserSurface makes the correct wiring the
// default and the only one-call option: take this handler, serve it, and
// both surfaces exist by construction.

import (
	"net/http"

	"github.com/johnewart/pyxis/internal/sessionstate"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/grpc"
)

// BrowserSurfaceConfig configures llmd's complete browser-facing surface.
// The auth/origin fields are shared by both halves deliberately: they are
// one listener with one threat model (loopback, token-guarded, origin
// allowlisted), and letting them drift apart is how you get an RPC path
// that works next to a WebSocket path that rejects every handshake.
type BrowserSurfaceConfig struct {
	// SessionEvents feeds the session-events WebSocket (replay and live
	// tail). May be nil — the endpoint then closes immediately and says so in
	// the log — but a host wiring the browser surface almost certainly wants it.
	SessionEvents *store.SessionEventRepo
	// SessionStates feeds the per-scope session-states WebSocket (running /
	// title / status / active tool for every session in a scope). Nil leaves
	// /ws/session-states unrouted.
	SessionStates *sessionstate.Tracker
	// Token is the per-spawn shared secret. Empty disables auth (tests only).
	Token string
	// AllowedOrigins is the exact-match, scheme-included allowlist of
	// browser origins (e.g. "http://127.0.0.1:5174").
	AllowedOrigins []string
}

// NewBrowserSurface returns the single http.Handler a composition root
// should serve on the browser-facing listener: grpc-web/Connect for RPCs
// plus the session-events WebSocket, routed by NewConnectMux.
//
// This is the intended entry point. Prefer it over assembling
// NewConnectHandler / NewSessionEventsWSHandler / NewConnectMux by hand.
func NewBrowserSurface(grpcServer *grpc.Server, cfg BrowserSurfaceConfig) http.Handler {
	connectHandler := newConnectHandler(grpcServer, ConnectConfig{
		Token:          cfg.Token,
		AllowedOrigins: cfg.AllowedOrigins,
	})
	// A browser's native `new WebSocket(url)` cannot set the x-llmd-token
	// header the Connect surface uses, so this half takes its credentials
	// as query parameters instead; see session_events_ws.go's module doc.
	wsHandler := NewSessionEventsWSHandler(SessionEventsWSConfig{
		SessionEvents:  cfg.SessionEvents,
		Token:          cfg.Token,
		AllowedOrigins: cfg.AllowedOrigins,
	})
	var statesHandler http.Handler
	if cfg.SessionStates != nil {
		statesHandler = NewSessionStatesWSHandler(SessionStatesWSConfig{
			Tracker:        cfg.SessionStates,
			Token:          cfg.Token,
			AllowedOrigins: cfg.AllowedOrigins,
		})
	}
	return newBrowserMux(wsHandler, statesHandler, connectHandler)
}

// BrowserSurface builds the complete browser-facing handler against this
// Server's shared grpc.Server. See NewBrowserSurface.
func (s *Server) BrowserSurface(cfg BrowserSurfaceConfig) http.Handler {
	return NewBrowserSurface(s.GRPC, cfg)
}
