package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/sessionstate"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/protobuf/encoding/protojson"
	"nhooyr.io/websocket"
)

// One socket per scope: a snapshot first, then a whole State for each change,
// for any session in the scope, and nothing from other scopes.
func TestSessionStatesWS_SnapshotThenLive(t *testing.T) {
	db := store.NewTestSQLite(t)
	ctx := context.Background()
	sc := serverutil.Scope{TenantID: "ws-states"}
	other := serverutil.Scope{TenantID: "ws-states-other"}

	events := store.NewSessionEventRepo(db)
	tracker := sessionstate.New(events)
	t.Cleanup(tracker.Stop)

	appendFrame := func(sc serverutil.Scope, sid string, f *llmv1.ConversationStreamFrame) {
		t.Helper()
		b, err := protojson.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := events.AppendEvent(ctx, sc, sid, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	started := &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{}}}

	// Before connecting: s1 is mid-turn. The snapshot must say so.
	appendFrame(sc, "s1", started)

	srv := httptest.NewServer(NewSessionStatesWSHandler(SessionStatesWSConfig{Tracker: tracker, Token: "tok"}))
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + SessionStatesWSPath + "?" + scopeQuery(sc.TenantID) + "&token=tok"
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })

	read := func() sessionStatesMessage {
		t.Helper()
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_, data, err := c.Read(rctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m sessionStatesMessage
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("decode %s: %v", data, err)
		}
		return m
	}

	snap := read()
	if snap.Type != "snapshot" || len(snap.Sessions) != 1 || snap.Sessions[0].SessionID != "s1" || !snap.Sessions[0].Running {
		t.Fatalf("snapshot = %+v, want s1 running", snap)
	}

	// Another scope's events never reach this socket; a text delta changes nothing.
	appendFrame(other, "x", started)
	appendFrame(sc, "s1", &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{AssistantTextDelta: &llmv1.EvAssistantTextDelta{}}})
	appendFrame(sc, "s2", &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_SessionTitleChanged{SessionTitleChanged: &llmv1.EvSessionTitleChanged{Title: " Fix nav "}}})
	appendFrame(sc, "s1", &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{}}})

	m := read()
	if m.Type != "state" || m.Session == nil || m.Session.SessionID != "s2" || m.Session.Title != "Fix nav" {
		t.Fatalf("first update = %+v, want s2 titled", m)
	}
	m = read()
	if m.Type != "state" || m.Session == nil || m.Session.SessionID != "s1" || m.Session.Running {
		t.Fatalf("second update = %+v, want s1 idle", m)
	}
}

// Same-origin relays inject x-llmd-token rather than a query param; an empty
// `token=` from a frame without the preload must not shadow the header.
func TestSessionStatesWS_AcceptsRelayHeaderToken(t *testing.T) {
	srv := httptest.NewServer(NewSessionStatesWSHandler(SessionStatesWSConfig{Tracker: sessionstate.New(nil), Token: "tok"}))
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + SessionStatesWSPath + "?" + scopeQuery("w") + "&token="
	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Llmd-Token": []string{"tok"}},
	})
	if err != nil {
		t.Fatalf("dial with header token: %v", err)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
}

func TestSessionStatesWS_RejectsBadToken(t *testing.T) {
	srv := httptest.NewServer(NewSessionStatesWSHandler(SessionStatesWSConfig{Tracker: sessionstate.New(nil), Token: "tok"}))
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + SessionStatesWSPath + "?" + scopeQuery("w") + "&token=wrong"
	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := websocket.Dial(dialCtx, wsURL, nil); err == nil {
		t.Fatal("expected handshake to be rejected")
	}
}
