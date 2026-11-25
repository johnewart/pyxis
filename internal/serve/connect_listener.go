package serve

// Connect / grpc-web listener: the renderer talks to llmd directly.
//
// This listener is loopback-only and guarded by a per-spawn shared secret that
// lives in Electron main's memory and is injected into each BrowserWindow. It
// is never written to disk and never logged. An empty Token means auth is
// disabled, which is for in-process unit tests only — production wiring always
// supplies one.
//
// The listener adds no policy of its own: it frames grpc-web, checks the token
// and the Origin, then hands the call to the same grpc.Server the native
// clients use. Verdicts stay llmd-authored and pass through verbatim.

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc"
)

// ConnectTokenHeader carries the shared secret. Authorization: Bearer <token>
// is accepted as an equivalent.
const ConnectTokenHeader = "x-llmd-token"

// connectLogModule is the llmdlog `module` field for this listener's lines.
// Distinct from session-events-ws so the two surfaces on this one port stay
// separable in the log.
const connectLogModule = "connect-listener"

// connectAllowedHeaders is the set a Connect/grpc-web browser client needs to
// be allowed to send. Missing any of these turns into an opaque CORS failure in
// the renderer, so they are asserted in connect_auth_test.go.
var connectAllowedHeaders = []string{
	"content-type",
	"x-grpc-web",
	"x-user-agent",
	"grpc-timeout",
	"connect-protocol-version",
	"connect-timeout-ms",
	"authorization",
	ConnectTokenHeader,
	// llmd's own request metadata, sent by @pyxis-llm/client (packages/client/src/transport.ts).
	// Names must match internal/serverutil/scope_wire.go and the ChatStream
	// system-prompt-sections key.
	"llmd-tenant-id",
	"llmd-scope-id",
	"llmd-principal-id",
	"llmd-system-prompt-sections",
}

// connectExposedHeaders lets the browser read grpc trailers-as-headers.
var connectExposedHeaders = []string{"grpc-status", "grpc-message", "grpc-status-details-bin"}

// ConnectConfig configures the grpc-web/Connect HTTP surface.
type ConnectConfig struct {
	// Token is the per-spawn shared secret. Empty disables auth (tests only).
	Token string
	// AllowedOrigins is the exact-match allowlist of browser origins.
	AllowedOrigins []string
}

func (c ConnectConfig) originAllowed(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	for _, allowed := range c.AllowedOrigins {
		if strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

// NewConnectHandler wraps server with grpc-web framing, per-request token auth
// and an origin allowlist.
//
// This is HALF of llmd's browser surface. A handler built here serves RPCs
// and nothing else: it has no /ws/session-events route, so a renderer using
// a browser client's subscribeSessionEvents gets "closed before handshake"
// while every RPC around it works perfectly. Prefer NewBrowserSurface,
// which wires both halves together (see browser_surface.go). Calling this
// directly logs a warning naming what's missing, because the failure it
// produces is otherwise completely silent on both sides.
func NewConnectHandler(server *grpc.Server, cfg ConnectConfig) http.Handler {
	llmdlog.Stdout(connectLogModule, llmdlog.LevelWarn,
		"Connect/grpc-web handler built WITHOUT the session-events WebSocket: this handler alone serves no %s "+
			"route, so browser clients calling subscribeSessionEvents will fail the handshake (\"closed before "+
			"handshake\") with no further error on either side. If this listener is browser-facing, build it with "+
			"serve.NewBrowserSurface (or Server.BrowserSurface) instead, which wires both halves onto one mux.",
		SessionEventsWSPath)
	return newConnectHandler(server, cfg)
}

// newConnectHandler is the un-warned constructor used by NewBrowserSurface,
// where the WebSocket half is wired by construction and the warning above
// would be noise.
func newConnectHandler(server *grpc.Server, cfg ConnectConfig) http.Handler {
	wrapped := grpcweb.WrapServer(server,
		grpcweb.WithOriginFunc(cfg.originAllowed),
		grpcweb.WithCorsForRegisteredEndpointsOnly(false),
	)
	return &connectHandler{cfg: cfg, wrapped: wrapped}
}

type connectHandler struct {
	cfg     ConnectConfig
	wrapped *grpcweb.WrappedGrpcServer
}

func (h *connectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	allowedOrigin := h.cfg.originAllowed(origin)
	// A browser request carrying an Origin we don't allow is dead on arrival:
	// either rejected outright below (preflight) or answered without the CORS
	// headers that would let the renderer read the response — which the
	// browser reports as a generic network/CORS failure with nothing on the
	// server side to correlate it against. Only non-empty origins are logged;
	// native gRPC clients send none and would otherwise flood this line.
	if origin != "" && !allowedOrigin {
		llmdlog.Stdout(connectLogModule, llmdlog.LevelWarn,
			"origin not allowed: origin=%q method=%s path=%q allowed_origins=%v (exact, scheme-included match "+
				"against LLMD_CONNECT_ALLOWED_ORIGINS)",
			origin, r.Method, r.URL.Path, h.cfg.AllowedOrigins)
	}
	if allowedOrigin {
		hdr := w.Header()
		hdr.Set("Access-Control-Allow-Origin", origin)
		hdr.Add("Vary", "Origin")
		hdr.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		hdr.Set("Access-Control-Allow-Headers", strings.Join(connectAllowedHeaders, ", "))
		hdr.Set("Access-Control-Expose-Headers", strings.Join(connectExposedHeaders, ", "))
		hdr.Set("Access-Control-Max-Age", "600")
	}

	if r.Method == http.MethodOptions {
		if !allowedOrigin {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if !h.authorized(r) {
		// Logged because the response is a trailers-only HTTP 200 (see
		// writeConnectUnauthenticated): to anything reading status codes —
		// proxies, curl, dev tools' network tab — a rejected call looks like a
		// successful one. Never log the token or any part of it; whether the
		// header was present at all is the useful signal (absent => the client
		// isn't sending it; present => the two secrets disagree).
		llmdlog.Stdout(connectLogModule, llmdlog.LevelWarn,
			"unauthenticated request rejected: method=%s path=%q origin=%q token_header_present=%t "+
				"authorization_header_present=%t",
			r.Method, r.URL.Path, origin,
			strings.TrimSpace(r.Header.Get(ConnectTokenHeader)) != "",
			strings.TrimSpace(r.Header.Get("Authorization")) != "")
		writeConnectUnauthenticated(w)
		return
	}

	h.wrapped.ServeHTTP(w, r)
}

func (h *connectHandler) authorized(r *http.Request) bool {
	want := h.cfg.Token
	if want == "" {
		return true
	}
	got := strings.TrimSpace(r.Header.Get(ConnectTokenHeader))
	if got == "" {
		if bearer := strings.TrimSpace(r.Header.Get("Authorization")); bearer != "" {
			got = strings.TrimSpace(strings.TrimPrefix(bearer, "Bearer"))
		}
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// writeConnectUnauthenticated emits a grpc-web trailers-only response: HTTP 200
// with grpc-status 16 (UNAUTHENTICATED), which is what a grpc-web/Connect
// client surfaces as an unauthenticated error.
func writeConnectUnauthenticated(w http.ResponseWriter) {
	hdr := w.Header()
	hdr.Set("Content-Type", "application/grpc-web+proto")
	hdr.Set("grpc-status", "16")
	hdr.Set("grpc-message", "unauthenticated: missing or invalid llmd connect token")
	w.WriteHeader(http.StatusOK)
}
