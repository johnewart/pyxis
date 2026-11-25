package serve

// Browser-facing WebSocket for the per-scope session summary feed
// (internal/sessionstate): one socket per (tenant, scope) carrying, for every
// session in it, whether a turn is running, its title, status headline and
// active tool. This is what a nav spinner or tab strip should hold, instead of
// one /ws/session-events transcript tail per session.
//
// Wire format (server push only; text frames, JSON):
//
//	{"type":"snapshot","sessions":[State...]}   first, and again after a lag
//	{"type":"state","session":State}            one session changed
//
// A snapshot REPLACES the client's view of the scope. Same handshake, auth and
// origin model as the session-events socket: scope query params
// (serverutil.ScopeFromQuery) and token as query parameters.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/sessionstate"
	"github.com/johnewart/pyxis/llmdlog"
	"nhooyr.io/websocket"
)

// SessionStatesWSPath is the route the session-states socket answers on.
const SessionStatesWSPath = "/ws/session-states"

const sessionStatesLogModule = "session-states-ws"

// SessionStatesWSConfig configures the session-states WebSocket handler.
type SessionStatesWSConfig struct {
	Tracker        *sessionstate.Tracker
	Token          string
	AllowedOrigins []string
}

// NewSessionStatesWSHandler returns the handler for GET /ws/session-states
// ?<scope query params>&token=...
func NewSessionStatesWSHandler(cfg SessionStatesWSConfig) http.Handler {
	return &sessionStatesWSHandler{cfg: cfg}
}

type sessionStatesWSHandler struct {
	cfg SessionStatesWSConfig
}

type sessionStatesMessage struct {
	Type     string               `json:"type"`
	Sessions []sessionstate.State `json:"sessions,omitempty"`
	Session  *sessionstate.State  `json:"session,omitempty"`
}

func (h *sessionStatesWSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sc, err := serverutil.ScopeFromQuery(q)
	if err != nil {
		llmdlog.Stdout(sessionStatesLogModule, llmdlog.LevelWarn,
			"rejecting handshake: missing tenant scope origin=%q: %v", r.Header.Get("Origin"), err)
		http.Error(w, "tenant scope is required", http.StatusBadRequest)
		return
	}
	// The query param is for a browser dialing directly (it cannot set
	// headers); a same-origin relay injects x-llmd-token instead, exactly as
	// it does for every Connect RPC, so no relay needs a rule for this path.
	token := strings.TrimSpace(q.Get("token"))
	if token == "" {
		token = r.Header.Get("x-llmd-token")
	}
	if !tokenAuthorized(h.cfg.Token, token) {
		llmdlog.Stdout(sessionStatesLogModule, llmdlog.LevelWarn,
			"rejecting handshake: bad/missing token %s origin=%q", sc.String(), r.Header.Get("Origin"))
		http.Error(w, "unauthenticated: missing or invalid llmd connect token", http.StatusUnauthorized)
		return
	}
	allowedHosts := originHosts(h.cfg.AllowedOrigins)
	c, err := websocket.Accept(w, r, browserAcceptOptions(&websocket.AcceptOptions{OriginPatterns: allowedHosts}))
	if err != nil {
		// See session_events_ws.go: almost always the origin check.
		llmdlog.Stdout(sessionStatesLogModule, llmdlog.LevelWarn,
			"handshake rejected %s request_host=%q origin=%q allowed_origin_hosts=%v err=%v",
			sc.String(), r.Host, r.Header.Get("Origin"), allowedHosts, err)
		return
	}
	defer c.Close(websocket.StatusInternalError, "")
	ctx := c.CloseRead(r.Context())

	if h.cfg.Tracker == nil {
		llmdlog.Stdout(sessionStatesLogModule, llmdlog.LevelWarn,
			"closing: no session state tracker configured %s", sc.String())
		c.Close(websocket.StatusNormalClosure, "")
		return
	}

	send := func(m sessionStatesMessage) error {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		return c.Write(ctx, websocket.MessageText, b)
	}
	snapshot := func() error {
		sessions := h.cfg.Tracker.Snapshot(sc)
		if sessions == nil {
			sessions = []sessionstate.State{}
		}
		return send(sessionStatesMessage{Type: "snapshot", Sessions: sessions})
	}

	// Subscribe before the snapshot so nothing lands in between unseen.
	sub := h.cfg.Tracker.Subscribe(sc)
	defer sub.Close()
	if err := snapshot(); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			c.Close(websocket.StatusNormalClosure, "")
			return
		case <-sub.Lagged:
			// Updates were dropped; drain what is queued and resend the truth.
			for drained := false; !drained; {
				select {
				case <-sub.Updates:
				default:
					drained = true
				}
			}
			if err := snapshot(); err != nil {
				return
			}
		case st := <-sub.Updates:
			if err := send(sessionStatesMessage{Type: "state", Session: &st}); err != nil {
				return
			}
		}
	}
}
