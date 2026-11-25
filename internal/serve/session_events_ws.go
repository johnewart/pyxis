package serve

// Browser-facing WebSocket side channel for live ConversationStreamFrame
// delivery — the WS twin of the gRPC SubscribeSessionEvents RPC
// (internal/server/subscribe_session_events.go). Exists because some hosts
// would rather not carry a grpc-web/Connect streaming call open just for a
// supplementary live feed of session activity (tool execution progress,
// session status/title/todos from MCP-dispatched session tools, etc. — see
// internal/mcpserver/server.go's handleToolsCall / publishToolEventFrame
// and SubscribeSessionEvents' own doc comment for why this side channel
// needs to exist in the first place). Same auth/origin model as the Connect
// listener (ConnectConfig): loopback-only, token-guarded, exact-match
// origin allowlist — see connect_listener.go's module doc.
//
// A browser's native `new WebSocket(url)` cannot set custom headers, so
// unlike the gRPC/Connect surfaces (which take the token as a header), this
// endpoint takes session_id / the tenant scope (serverutil.ScopeFromQuery) /
// after_seq / token as query parameters on the handshake request.

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/sessionevents"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/protobuf/encoding/protojson"
	"nhooyr.io/websocket"
)

// LogModule is the llmdlog `module` field for every line this handler emits.
const LogModule = "session-events-ws"

// SessionEventsWSPath is the route this handler answers on. Exported so the
// mux, the startup route log and any test all name the same string rather
// than three independent literals that can drift apart silently.
const SessionEventsWSPath = "/ws/session-events"

// SessionEventsWSConfig configures the WebSocket session-events handler.
// SessionEvents is the same repo internal/server.LlmServer holds (passed
// through directly rather than the whole LlmServer to avoid an
// internal/server <-> internal/serve import cycle: internal/server already
// imports internal/serve).
type SessionEventsWSConfig struct {
	SessionEvents *store.SessionEventRepo
	// Token is the per-spawn shared secret (same one as ConnectConfig.Token).
	// Empty disables auth (tests only).
	Token string
	// AllowedOrigins is the exact-match allowlist of browser origins.
	AllowedOrigins []string
}

// NewSessionEventsWSHandler returns an http.Handler for the WebSocket
// session-events endpoint: GET /ws/session-events
// ?session_id=...&<scope query params>&after_seq=N&token=...
func NewSessionEventsWSHandler(cfg SessionEventsWSConfig) http.Handler {
	return &sessionEventsWSHandler{cfg: cfg}
}

// NewConnectMux routes a single loopback listener between the WebSocket
// session-events endpoint and the Connect/grpc-web handler by PATH SUFFIX
// rather than plain http.ServeMux exact-match, because a dev-server proxy
// (Vite, etc.) commonly fronts this whole listener behind its own path
// prefix (e.g. "/host-connect") without stripping it before
// forwarding — the real inbound request path becomes
// "/host-connect/ws/session-events", not "/ws/session-events", which
// http.ServeMux's exact matching would never route to wsHandler. Anything
// not ending in the WS path falls through to connectHandler exactly as
// before. (A prefix mismatch here presents as an inexplicable "socket hang
// up"/"closed before handshake" on the client, since the Connect handler
// doesn't know what to do with a GET+Upgrade request on an arbitrary path
// and drops the connection rather than answering it.)
func NewConnectMux(wsHandler, connectHandler http.Handler) http.Handler {
	return newBrowserMux(wsHandler, nil, connectHandler)
}

// newBrowserMux is NewConnectMux plus the session-states socket (same
// suffix routing, same reason). A nil statesHandler leaves that path to the
// Connect handler.
func newBrowserMux(eventsHandler, statesHandler, connectHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, SessionEventsWSPath):
			eventsHandler.ServeHTTP(w, r)
		case statesHandler != nil && strings.HasSuffix(r.URL.Path, SessionStatesWSPath):
			statesHandler.ServeHTTP(w, r)
		default:
			connectHandler.ServeHTTP(w, r)
		}
	})
}

type sessionEventsWSHandler struct {
	cfg SessionEventsWSConfig
}

func (h *sessionEventsWSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sessionID := strings.TrimSpace(q.Get("session_id"))
	if sessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	sc, err := serverutil.ScopeFromQuery(q)
	if err != nil {
		llmdlog.Stdout("session-events-ws", llmdlog.LevelWarn,
			"rejecting handshake: missing tenant scope session=%q origin=%q: %v",
			sessionID, r.Header.Get("Origin"), err)
		http.Error(w, "tenant scope is required", http.StatusBadRequest)
		return
	}
	var afterSeq int64
	if raw := strings.TrimSpace(q.Get("after_seq")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.Error(w, "after_seq must be an integer", http.StatusBadRequest)
			return
		}
		afterSeq = v
	}
	if !h.authorized(q.Get("token")) {
		llmdlog.Stdout(LogModule, llmdlog.LevelWarn,
			"rejecting handshake: bad/missing token session=%q %s origin=%q",
			sessionID, sc.String(), r.Header.Get("Origin"))
		http.Error(w, "unauthenticated: missing or invalid llmd connect token", http.StatusUnauthorized)
		return
	}

	allowedHosts := originHosts(h.cfg.AllowedOrigins)
	c, err := websocket.Accept(w, r, browserAcceptOptions(&websocket.AcceptOptions{
		// nhooyr.io/websocket's OriginPatterns matches against the origin's
		// HOST ONLY (no scheme) via filepath.Match — see its doc comment.
		// AllowedOrigins (shared with ConnectConfig, whose CORS check does an
		// exact string match on the FULL scheme-included origin) can't be
		// passed through as-is: every cross-origin handshake would fail this
		// check silently (Accept returns before any 101 response, which is
		// indistinguishable client-side from "closed before handshake").
		// originHosts strips the scheme so the same env-configured allowlist
		// works for both surfaces.
		OriginPatterns: allowedHosts,
	}))
	if err != nil {
		// This is almost always the origin check: nhooyr.io/websocket
		// rejects (no 101 sent) before returning here if the request Origin
		// header's host isn't the request Host and isn't in OriginPatterns.
		// From a browser this presents as "closed before handshake" with no
		// other client-visible signal — log request Host/Origin and the
		// resolved allowlist so a mismatch (e.g. a dev-server proxy rewriting
		// Host but preserving the original page Origin) is visible here
		// instead of only guessable from the client side.
		llmdlog.Stdout(LogModule, llmdlog.LevelWarn,
			"handshake rejected session=%q %s request_host=%q origin=%q allowed_origin_hosts=%v err=%v",
			sessionID, sc.String(), r.Host, r.Header.Get("Origin"), allowedHosts, err)
		return
	}
	llmdlog.Stdout(LogModule, llmdlog.LevelInfo,
		"handshake accepted session=%q %s after_seq=%d origin=%q",
		sessionID, sc.String(), afterSeq, r.Header.Get("Origin"))
	defer c.Close(websocket.StatusInternalError, "")

	// CloseRead spins a goroutine that discards anything the client sends
	// (this endpoint is server-push only) and handles ping/pong + detecting
	// client-initiated close; its returned context is canceled when that
	// happens, which is also our own consumer/replay loop's cue to stop.
	ctx := c.CloseRead(r.Context())

	send := func(frame *llmv1.ConversationStreamFrame) error {
		// protojson (not encoding/json) so the wire shape matches what the
		// TS client's protobuf-es decoder expects — consistent whether the
		// frame came from a replayed persisted row (already protojson text,
		// see SubscribeSessionEvents) or a live Hub dispatch.
		b, err := protojson.Marshal(frame)
		if err != nil {
			llmdlog.Stdout(LogModule, llmdlog.LevelWarn,
				"skipping un-marshalable frame session=%q err=%v", sessionID, err)
			return nil // skip this one frame rather than killing the whole connection
		}
		return c.Write(ctx, websocket.MessageText, b)
	}

	// Replay from the durable log, then follow it live — same code path as the
	// gRPC RPC (internal/sessionevents). Live rows are pushed by the log.
	if h.cfg.SessionEvents == nil {
		llmdlog.Stdout(LogModule, llmdlog.LevelWarn,
			"closing: no session event store configured, nothing to stream session=%q", sessionID)
		c.Close(websocket.StatusNormalClosure, "")
		return
	}
	frames := 0
	counted := func(f *llmv1.ConversationStreamFrame) error {
		if err := send(f); err != nil {
			return err
		}
		frames++
		return nil
	}
	err = sessionevents.Tail(ctx, h.cfg.SessionEvents, sc, sessionID, afterSeq, counted, LogModule)
	// One line per feed end, always: whether a client got its frames is
	// otherwise invisible server-side (a proxy dropping the socket looks
	// identical to a healthy idle feed until someone reads this).
	reason := "client closed the socket"
	if err != nil {
		reason = "write to client failed: " + err.Error()
	}
	llmdlog.Stdout(LogModule, llmdlog.LevelInfo,
		"feed ended session=%q %s after_seq=%d frames_sent=%d reason=%s", sessionID, sc.String(), afterSeq, frames, reason)
}

// browserAcceptOptions applies the settings every browser-facing socket
// needs on top of opts. Compression (permessage-deflate) is turned off:
// nhooyr.io/websocket v1.8.6 negotiates it by default and compresses frames
// above a size threshold, and iOS Safari drops the connection on such
// frames, which showed up as repeated "subscribeSessionEvents: WebSocket
// error" on iPhones. These feeds are small and loopback/LAN, so compression
// buys nothing.
func browserAcceptOptions(opts *websocket.AcceptOptions) *websocket.AcceptOptions {
	opts.CompressionMode = websocket.CompressionDisabled
	return opts
}

// originHosts converts full scheme-included origins ("http://host:port",
// ConnectConfig.AllowedOrigins' shape) into the bare host:port patterns
// nhooyr.io/websocket's AcceptOptions.OriginPatterns expects. Entries that
// don't parse as a URL, or have no host, are dropped (an origin llmd could
// never legitimately see) rather than passed through malformed.
func originHosts(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		u, err := url.Parse(strings.TrimSpace(o))
		if err != nil || u.Host == "" {
			continue
		}
		out = append(out, u.Host)
	}
	return out
}

func (h *sessionEventsWSHandler) authorized(token string) bool {
	return tokenAuthorized(h.cfg.Token, token)
}

// tokenAuthorized checks a query-param token against the per-spawn secret.
// An empty want disables auth (tests only).
func tokenAuthorized(want, token string) bool {
	if want == "" {
		return true
	}
	got := strings.TrimSpace(token)
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
