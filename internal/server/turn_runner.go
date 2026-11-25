package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/johnewart/pyxis/llmdlog"
)

// turnRunner owns the lifetime of in-flight assistant turns.
//
// A ChatStream turn must not die with the client's request: navigation, reload
// or a network blip all end the stream, and none of them mean "stop". So each
// turn runs on a context derived from the server's base context (cancelled on
// host shutdown) that carries the request's VALUES (scope, tenant, …) but not
// its cancellation. The only things that end a turn early are:
//
//   - an explicit CancelTurn RPC for the session (user pressed Stop), and
//   - the server's base context ending (host shutdown).
//
// The cancel cause is a *turnCutError, so the turn body can tell which of the
// two happened and stamp the right finish_reason on the salvaged partial.
//
// Registration is per session id: turnGate already guarantees at most one turn
// holds a session at a time, so there is never more than one entry per sid.
type turnRunner struct {
	mu    sync.Mutex
	turns map[string]*runningTurn
	// last holds each session's most recently finished turn, so a client that
	// opens the session afterwards can still show how long it took. In memory
	// only: one small entry per session that has run a turn since llmd started.
	last map[string]finishedTurn
	seq  uint64
	wg   sync.WaitGroup
}

// finishedTurn is when a session's last turn ran, by the server clock.
type finishedTurn struct {
	startedAt time.Time
	endedAt   time.Time
}

type runningTurn struct {
	id     uint64
	cancel context.CancelCauseFunc
	// startedAt is when the turn began (server clock). The turn's
	// UserTurnCommitted carries it as committed_at_ms, and the session status
	// reports it, so every client times the turn from the same instant.
	startedAt time.Time
	// eventsSeqAtStart is the session event-log head when this turn began: a
	// client rejoining the turn subscribes with after_seq = this value and
	// receives exactly the turn's frames, none of the session's older history.
	eventsSeqAtStart int64
}

// turnCutError is the cancel cause for a turn ended on purpose.
type turnCutError struct {
	// Reason is the finish_reason for the salvaged partial ("cancelled", "shutdown", …).
	Reason string
}

func (e *turnCutError) Error() string { return "turn cut: " + e.Reason }

const (
	turnCutReasonCancelled = "cancelled"
	turnCutReasonShutdown  = "shutdown"
)

func newTurnRunner() *turnRunner {
	return &turnRunner{turns: make(map[string]*runningTurn), last: make(map[string]finishedTurn)}
}

// start creates the turn context for session sid and registers it so
// cancel(sid) can reach it. base is the server lifetime context; reqCtx only
// donates its values. The returned finish func must be called exactly once,
// after the turn has fully committed (the caller's defer).
func (r *turnRunner) start(base, reqCtx context.Context, sid string) (context.Context, func()) {
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(reqCtx))
	stopBase := context.AfterFunc(base, func() {
		cancel(&turnCutError{Reason: turnCutReasonShutdown})
	})

	r.wg.Add(1)
	sid = strings.TrimSpace(sid)
	var id uint64
	if sid != "" {
		r.mu.Lock()
		r.seq++
		id = r.seq
		if prev := r.turns[sid]; prev != nil {
			// turnGate should make this impossible; if it ever happens the newer
			// turn wins the registration, and we say so rather than hide it.
			llmdlog.Stdout("server", llmdlog.LevelWarn,
				"turnRunner: session %q already had a registered turn (id=%d); replacing with id=%d", sid, prev.id, id)
		}
		r.turns[sid] = &runningTurn{id: id, cancel: cancel, startedAt: time.Now()}
		r.mu.Unlock()
	}

	var once sync.Once
	finish := func() {
		once.Do(func() {
			stopBase()
			if sid != "" {
				r.mu.Lock()
				if cur := r.turns[sid]; cur != nil && cur.id == id {
					delete(r.turns, sid)
					r.last[sid] = finishedTurn{startedAt: cur.startedAt, endedAt: time.Now()}
				}
				r.mu.Unlock()
			}
			cancel(nil)
			r.wg.Done()
		})
	}
	return ctx, finish
}

// cancel cuts the session's running turn short. reason becomes the salvaged
// partial's finish_reason (default "cancelled"). Reports whether a turn was found.
func (r *turnRunner) cancel(sid, reason string) bool {
	sid = strings.TrimSpace(sid)
	if r == nil || sid == "" {
		return false
	}
	if strings.TrimSpace(reason) == "" {
		reason = turnCutReasonCancelled
	}
	r.mu.Lock()
	t := r.turns[sid]
	r.mu.Unlock()
	if t == nil {
		return false
	}
	t.cancel(&turnCutError{Reason: reason})
	return true
}

// running reports whether sid has a registered in-flight turn.
func (r *turnRunner) running(sid string) bool {
	_, ok := r.active(sid)
	return ok
}

// active reports whether sid has an in-flight turn and, if so, the event-log
// seq it started after.
func (r *turnRunner) active(sid string) (eventsSeqAtStart int64, ok bool) {
	if r == nil {
		return 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.turns[strings.TrimSpace(sid)]
	if t == nil {
		return 0, false
	}
	return t.eventsSeqAtStart, true
}

// startedAt reports when sid's running turn began.
func (r *turnRunner) startedAt(sid string) (time.Time, bool) {
	if r == nil {
		return time.Time{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.turns[strings.TrimSpace(sid)]
	if t == nil {
		return time.Time{}, false
	}
	return t.startedAt, true
}

// lastFinished reports when sid's most recently finished turn ran, if one has
// finished since llmd started.
func (r *turnRunner) lastFinished(sid string) (finishedTurn, bool) {
	if r == nil {
		return finishedTurn{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ft, ok := r.last[strings.TrimSpace(sid)]
	return ft, ok
}

// setEventsSeqAtStart records the event-log head for sid's running turn.
func (r *turnRunner) setEventsSeqAtStart(sid string, seq int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.turns[strings.TrimSpace(sid)]; t != nil {
		t.eventsSeqAtStart = seq
	}
}

// wait blocks until every started turn has finished (committed), or timeout.
// Reports whether all turns finished in time. Used on host shutdown so salvage
// commits land before the databases close.
func (r *turnRunner) wait(timeout time.Duration) bool {
	if r == nil {
		return true
	}
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// turnCutReason returns the finish_reason for a turn whose ctx has ended:
// the explicit reason when it was cut on purpose, otherwise the generic
// mapping of ctx.Err() (deadline → "timed_out", …). Empty while ctx is live.
func turnCutReason(ctx context.Context) string {
	if ctx.Err() == nil {
		return ""
	}
	var cut *turnCutError
	if errors.As(context.Cause(ctx), &cut) && cut.Reason != "" {
		return cut.Reason
	}
	return cutFinishReason(ctx.Err())
}
