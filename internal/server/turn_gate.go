package server

import (
	"context"
	"strings"
	"sync"
)

// turnGate serializes assistant turns within a single session, in arrival order.
//
// Why a session needs this at all: ChatStream reads the session's persisted
// transcript on entry and writes this turn's rows at the end. Two turns for one
// session running concurrently therefore interleave a read against a write, and
// the loser silently produces a wrong transcript. The case that matters is not
// exotic — it is the ordinary interrupt: the UI aborts the in-flight turn and
// immediately sends the replacement, without waiting for the aborted turn's
// (detached, post-cancel) commit to land. Unserialized, the new turn can read
// history before the partial it interrupted has been written, so the salvaged
// partial ends up ordered AFTER the message that interrupted it — or missing
// from the context the model is given for the new turn.
//
// With the gate, turn N's commit strictly precedes turn N+1's history read, so
// the transcript always reads: original message, whatever the model managed to
// say, injected message, new answer.
//
// Waiting (rather than rejecting) is the intended behaviour for a second send
// that arrives mid-turn: it lands at the back of the line. This gate is the
// foundation the per-session message queue is built on — the queue makes that
// line explicit and inspectable; the gate makes it correct.
type turnGate struct {
	mu       sync.Mutex
	sessions map[string]*turnGateLine
}

// turnGateLine is one session's line: whether a turn holds the session, plus the
// waiters in arrival order. FIFO is explicit rather than inherited from Go's
// channel wake order, because arrival order is a guarantee here, not a detail.
type turnGateLine struct {
	held    bool
	waiters []chan struct{}
}

func newTurnGate() *turnGate {
	return &turnGate{sessions: make(map[string]*turnGateLine)}
}

// acquire takes the session's turn slot, blocking until every turn that arrived
// earlier has finished committing. It returns a release func that must be called
// exactly once (the caller's defer) — releasing hands the slot to the next
// waiter in line.
//
// If ctx dies while queued, acquire abandons the slot and returns ctx.Err(): a
// caller who gave up waiting must not go on to take a turn, and must not leave
// the line blocked behind it.
func (g *turnGate) acquire(ctx context.Context, sessionID string) (release func(), err error) {
	sid := strings.TrimSpace(sessionID)
	// An empty session id has no transcript to order (unary/sessionless use), so
	// there is nothing to serialize against.
	if g == nil || sid == "" {
		return func() {}, nil
	}

	g.mu.Lock()
	line := g.sessions[sid]
	if line == nil {
		line = &turnGateLine{}
		g.sessions[sid] = line
	}
	if !line.held {
		line.held = true
		g.mu.Unlock()
		return g.releaser(sid), nil
	}
	ticket := make(chan struct{})
	line.waiters = append(line.waiters, ticket)
	g.mu.Unlock()

	select {
	case <-ticket:
		// The slot was handed to us; we hold it now.
		return g.releaser(sid), nil
	case <-ctx.Done():
		// Racing the handoff: whoever released may have already picked us. Only
		// the goroutine that finds itself still queued may walk away; if we were
		// already chosen we own the slot and have to pass it on.
		g.mu.Lock()
		stillQueued := false
		if line := g.sessions[sid]; line != nil {
			for i, w := range line.waiters {
				if w == ticket {
					line.waiters = append(line.waiters[:i], line.waiters[i+1:]...)
					stillQueued = true
					break
				}
			}
		}
		g.mu.Unlock()
		if !stillQueued {
			g.releaser(sid)()
		}
		return func() {}, ctx.Err()
	}
}

// tryAcquire takes the session's turn slot only if nobody holds it and nobody
// is waiting for it; it never joins the line. ok=false means the session is
// busy. Used where the caller would rather queue the work than wait for it.
func (g *turnGate) tryAcquire(sessionID string) (release func(), ok bool) {
	sid := strings.TrimSpace(sessionID)
	if g == nil || sid == "" {
		return func() {}, true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	line := g.sessions[sid]
	if line != nil && (line.held || len(line.waiters) > 0) {
		return nil, false
	}
	if line == nil {
		line = &turnGateLine{}
		g.sessions[sid] = line
	}
	line.held = true
	return g.releaser(sid), true
}

// releaser returns the single-use release func for a held slot.
func (g *turnGate) releaser(sid string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			line := g.sessions[sid]
			if line == nil {
				g.mu.Unlock()
				return
			}
			if len(line.waiters) > 0 {
				next := line.waiters[0]
				line.waiters = line.waiters[1:]
				// held stays true: the slot is handed straight to the next turn
				// rather than released into a free-for-all, which is what keeps
				// arrival order meaningful.
				g.mu.Unlock()
				close(next)
				return
			}
			line.held = false
			// Drop the empty line so idle sessions cost nothing.
			delete(g.sessions, sid)
			g.mu.Unlock()
		})
	}
}

// busy reports whether a turn currently holds the session's slot, i.e. a
// ChatStream turn is running (or committing) right now. A point-in-time read:
// a turn may start the instant after it returns false.
func (g *turnGate) busy(sessionID string) bool {
	if g == nil {
		return false
	}
	sid := strings.TrimSpace(sessionID)
	g.mu.Lock()
	defer g.mu.Unlock()
	line := g.sessions[sid]
	return line != nil && line.held
}

// waiting reports how many turns are queued behind the session's current turn.
// Used by tests, and the natural place for the queue layer to read depth from.
func (g *turnGate) waiting(sessionID string) int {
	if g == nil {
		return 0
	}
	sid := strings.TrimSpace(sessionID)
	g.mu.Lock()
	defer g.mu.Unlock()
	line := g.sessions[sid]
	if line == nil {
		return 0
	}
	return len(line.waiters)
}
