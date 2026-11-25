// Package sessionstate keeps a live, per-session summary of every session in
// the process — whether a turn is running, its title, its status headline and
// the tool executing right now — and fans it out per scope.
//
// It exists so a client that only wants "which of this project's sessions are
// busy, and what are they called" (a nav spinner, a tab strip) can hold ONE
// subscription per scope instead of one transcript tail per session, each of
// which replays that session's whole event log on connect.
//
// The fold is fed by store.SessionEventRepo.SubscribeAll: every committed
// event row is offered to it as it lands, so it costs no queries at all.
// The state is in memory and starts empty: a process that just started has no
// running turns (turns die with the process), and a title not changed since
// start is the session list's to report, not this feed's.
package sessionstate

import (
	"bytes"
	"strings"
	"sync"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/protobuf/encoding/protojson"
)

// State is one session's summary. It is always sent whole, so applying the
// same State twice is harmless.
type State struct {
	SessionID string `json:"sessionId"`
	// Running: an assistant turn is in progress.
	Running bool `json:"running"`
	// Title: last session_title_changed seen by this process; "" = none.
	Title string `json:"title,omitempty"`
	// StatusHeadline: last session_status headline; "" = none.
	StatusHeadline string `json:"statusHeadline,omitempty"`
	// ActiveTool: tool executing right now; "" between tool calls.
	ActiveTool string `json:"activeTool,omitempty"`
}

// SubscriberBuffer is how many updates a subscriber may fall behind by before
// it is told to resync from a fresh snapshot instead.
var SubscriberBuffer = 256

// Subscription is one scope subscriber. Updates carries changed sessions;
// Lagged fires when updates were dropped, and the subscriber should send a
// fresh Snapshot. Call Close when done.
type Subscription struct {
	Updates <-chan State
	Lagged  <-chan struct{}
	Close   func()
}

type subscriber struct {
	updates chan State
	lagged  chan struct{}
}

// Tracker folds every session's events into its State.
type Tracker struct {
	mu      sync.Mutex
	states  map[string]map[string]State // scope key -> session id -> state
	subs    map[string]map[int64]*subscriber
	nextSub int64
	stop    func()
}

func scopeKey(sc serverutil.Scope) string { return sc.TenantID + "\x00" + sc.ScopeID }

// New starts tracking every row appended to repo from now on. Create it when
// the repo is created, before any turn can run, or the fold misses the start
// of a turn already in flight.
func New(repo *store.SessionEventRepo) *Tracker {
	t := &Tracker{
		states: make(map[string]map[string]State),
		subs:   make(map[string]map[int64]*subscriber),
	}
	if repo != nil {
		t.stop = repo.SubscribeAll(t.onRow)
	}
	return t
}

// Stop detaches the tracker from its repo.
func (t *Tracker) Stop() {
	if t != nil && t.stop != nil {
		t.stop()
	}
}

// interesting: the protojson field names (camelCase, and the proto names in
// case a writer used UseProtoNames) of the only events the fold reads. A row
// mentioning none of them is skipped without being parsed, which is what keeps
// a turn's stream of text deltas from costing a protojson decode each.
var interesting = [][]byte{
	[]byte(`"assistantTurnStarted"`), []byte(`"assistant_turn_started"`),
	[]byte(`"assistantTurnFinished"`), []byte(`"assistant_turn_finished"`),
	[]byte(`"assistantTurnFailed"`), []byte(`"assistant_turn_failed"`),
	[]byte(`"conversationAborted"`), []byte(`"conversation_aborted"`),
	[]byte(`"toolExecutionStarted"`), []byte(`"tool_execution_started"`),
	[]byte(`"toolExecutionFinished"`), []byte(`"tool_execution_finished"`),
	[]byte(`"sessionStatus"`), []byte(`"session_status"`),
	[]byte(`"sessionTitleChanged"`), []byte(`"session_title_changed"`),
}

func mayMatter(eventJSON string) bool {
	b := []byte(eventJSON)
	for _, k := range interesting {
		if bytes.Contains(b, k) {
			return true
		}
	}
	return false
}

var unmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}

// Fold applies one frame to prev. It returns prev unchanged (and false) when
// the frame does not affect the summary.
func Fold(prev State, frame *llmv1.ConversationStreamFrame) (State, bool) {
	next := prev
	switch {
	case frame.GetAssistantTurnStarted() != nil:
		next.Running, next.ActiveTool = true, ""
	case frame.GetAssistantTurnFinished() != nil,
		frame.GetAssistantTurnFailed() != nil,
		frame.GetConversationAborted() != nil:
		next.Running, next.ActiveTool = false, ""
	case frame.GetToolExecutionStarted() != nil:
		next.ActiveTool = frame.GetToolExecutionStarted().GetName()
	case frame.GetToolExecutionFinished() != nil:
		next.ActiveTool = ""
	case frame.GetSessionTitleChanged() != nil:
		next.Title = trim(frame.GetSessionTitleChanged().GetTitle())
	case frame.GetSessionStatus() != nil:
		next.StatusHeadline = trim(frame.GetSessionStatus().GetHeadline())
	}
	return next, next != prev
}

func trim(s string) string { return strings.TrimSpace(s) }

// onRow runs on the appender's goroutine under that session's append lock: it
// must stay cheap and must never block (subscriber sends are non-blocking).
func (t *Tracker) onRow(sc serverutil.Scope, sessionID string, row store.SessionEventRow) {
	if !mayMatter(row.EventJSON) {
		return
	}
	frame := &llmv1.ConversationStreamFrame{}
	if err := unmarshal.Unmarshal([]byte(row.EventJSON), frame); err != nil {
		return
	}
	key := scopeKey(sc)
	t.mu.Lock()
	defer t.mu.Unlock()
	byID := t.states[key]
	prev, ok := byID[sessionID]
	if !ok {
		prev = State{SessionID: sessionID}
	}
	next, changed := Fold(prev, frame)
	if !changed {
		return
	}
	if byID == nil {
		byID = make(map[string]State)
		t.states[key] = byID
	}
	byID[sessionID] = next
	for _, s := range t.subs[key] {
		select {
		case s.updates <- next:
		default:
			select {
			case s.lagged <- struct{}{}:
			default:
			}
		}
	}
}

// Snapshot returns every session in the scope this process has a summary for.
func (t *Tracker) Snapshot(sc serverutil.Scope) []State {
	t.mu.Lock()
	defer t.mu.Unlock()
	byID := t.states[scopeKey(sc)]
	out := make([]State, 0, len(byID))
	for _, s := range byID {
		out = append(out, s)
	}
	return out
}

// Subscribe registers for changes in sc. Subscribe first, then read Snapshot:
// a change landing in between arrives on Updates too, and a State is whole,
// so applying it twice is harmless.
func (t *Tracker) Subscribe(sc serverutil.Scope) Subscription {
	s := &subscriber{
		updates: make(chan State, SubscriberBuffer),
		lagged:  make(chan struct{}, 1),
	}
	key := scopeKey(sc)
	t.mu.Lock()
	t.nextSub++
	id := t.nextSub
	if t.subs[key] == nil {
		t.subs[key] = make(map[int64]*subscriber)
	}
	t.subs[key][id] = s
	t.mu.Unlock()
	var once sync.Once
	return Subscription{
		Updates: s.updates,
		Lagged:  s.lagged,
		Close: func() {
			once.Do(func() {
				t.mu.Lock()
				defer t.mu.Unlock()
				if set := t.subs[key]; set != nil {
					delete(set, id)
					if len(set) == 0 {
						delete(t.subs, key)
					}
				}
			})
		},
	}
}
