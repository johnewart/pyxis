package session

import (
	"sort"
	"strings"
	"sync"

	"github.com/johnewart/pyxis/internal/assistant/history"
)

// Agent owns many logical chat sessions (TS: one agent, many ConversationSessionHub instances).
type Agent struct {
	id      string
	history history.Store
	mu      sync.RWMutex
	hubs    map[string]*Hub
	hubOpts HubOptions
}

func NewAgent(agentID string, store history.Store, hubOpts HubOptions) *Agent {
	if store == nil {
		store = history.NewMemory()
	}
	return &Agent{
		id:      agentID,
		history: store,
		hubs:    make(map[string]*Hub),
		hubOpts: hubOpts,
	}
}

func (a *Agent) ID() string { return a.id }

// History returns the pluggable store backing this agent.
func (a *Agent) History() history.Store { return a.history }

// Session returns the observation hub for a session key, creating it if needed.
func (a *Agent) Session(sessionID string) *Hub {
	if sessionID == "" {
		sessionID = "_default"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if h, ok := a.hubs[sessionID]; ok {
		return h
	}
	h := NewHub(a.hubOpts)
	a.hubs[sessionID] = h
	return h
}

// RemoveSession drops the hub for a session (does not clear history unless you do so separately).
func (a *Agent) RemoveSession(sessionID string) {
	if sessionID == "" {
		sessionID = "_default"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.hubs, sessionID)
}

// OpenSessionCount returns how many session hubs currently exist for this agent.
func (a *Agent) OpenSessionCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.hubs)
}

// SessionIDs returns a sorted copy of session keys that currently have hubs.
func (a *Agent) SessionIDs() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.hubs))
	for id := range a.hubs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// AllSessionIDs returns sorted unique session ids from in-memory hubs and the history store
// (e.g. persisted chat rows) so callers can list threads that have storage but no live hub.
func (a *Agent) AllSessionIDs() []string {
	hub := a.SessionIDs()
	var hist []string
	if a.history != nil {
		hist = a.history.SessionIDs()
	}
	seen := make(map[string]struct{}, len(hub)+len(hist))
	out := make([]string, 0, len(hub)+len(hist))
	add := func(ids []string) {
		for _, id := range ids {
			if strings.TrimSpace(id) == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	add(hub)
	add(hist)
	sort.Strings(out)
	return out
}
