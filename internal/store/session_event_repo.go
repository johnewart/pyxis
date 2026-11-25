package store

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/llmdlog"
	"gorm.io/gorm"
)

// Sentinel errors for session event operations.
var (
	ErrSessionEventInvalidArgs = errors.New("session_event: sc.TenantID, sc.ScopeID and sessionID are required")
)

// SessionEventRepo persists ConversationEvent rows for replay and subscription.
// Events are persisted with monotonically increasing seq per session.
// Scoped per call by (scope, sessionID).
type SessionEventRepo struct {
	db *gorm.DB

	// Appends go through one in-process queue, written by a single writer
	// goroutine (started when the queue gets work, gone when it is empty).
	// Everything waiting when the writer comes round is committed in ONE
	// transaction, across all sessions: a streaming turn emits a frame per
	// token, and a transaction per frame kept SQLite's single write lock busy
	// enough that other writers queued for seconds. Queue order is seq order,
	// so a ChatStream turn and an MCP tool-event publisher appending to the
	// same session can never reorder or collide on seq. The queue is bounded
	// (maxPendingAppends); a full queue makes appenders wait.
	qMu      sync.Mutex
	qCond    *sync.Cond
	queue    []*pendingAppend
	draining bool

	// Live subscribers per (scope, session), fed by AppendEvent after each
	// commit. This is what makes the log push-based: a subscriber gets every
	// committed row handed to it and never has to poll the table.
	subsMu  sync.RWMutex
	subs    map[string]map[int64]func(SessionEventRow)
	subNext int64
	// Process-wide subscribers: every committed row of every session (see
	// SubscribeAll). Guarded by subsMu.
	allSubs map[int64]func(serverutil.Scope, string, SessionEventRow)
}

// SubscribeAll registers fn to receive every row appended to ANY session by
// this process, immediately after it commits. Same contract as Subscribe: it
// runs on the repo's writer goroutine (so one session's rows arrive in seq
// order), and it MUST be cheap and non-blocking.
// It exists for process-wide folds such as internal/sessionstate.
func (r *SessionEventRepo) SubscribeAll(fn func(sc serverutil.Scope, sessionID string, row SessionEventRow)) (unsubscribe func()) {
	if fn == nil {
		return func() {}
	}
	r.subsMu.Lock()
	if r.allSubs == nil {
		r.allSubs = make(map[int64]func(serverutil.Scope, string, SessionEventRow))
	}
	r.subNext++
	id := r.subNext
	r.allSubs[id] = fn
	r.subsMu.Unlock()
	return func() {
		r.subsMu.Lock()
		defer r.subsMu.Unlock()
		delete(r.allSubs, id)
	}
}

func sessionEventKey(sc serverutil.Scope, sessionID string) string {
	return sc.TenantID + "\x00" + sc.ScopeID + "\x00" + sessionID
}

// Subscribe registers fn to receive every row appended to (sc, sessionID) by
// this process from now on, in seq order, immediately after the row commits.
// It is called on the repo's single writer goroutine, so it MUST NOT block or
// call back into the repo: hand the row to a
// buffered channel and return. A subscriber that cannot keep up drops rows and
// detects the seq gap itself (see internal/sessionevents.Tail); the log still
// has them. The returned func unsubscribes and is safe to call more than once.
func (r *SessionEventRepo) Subscribe(sc serverutil.Scope, sessionID string, fn func(SessionEventRow)) (unsubscribe func()) {
	if fn == nil {
		return func() {}
	}
	key := sessionEventKey(sc, sessionID)
	r.subsMu.Lock()
	if r.subs == nil {
		r.subs = make(map[string]map[int64]func(SessionEventRow))
	}
	r.subNext++
	id := r.subNext
	set := r.subs[key]
	if set == nil {
		set = make(map[int64]func(SessionEventRow))
		r.subs[key] = set
	}
	set[id] = fn
	r.subsMu.Unlock()
	return func() {
		r.subsMu.Lock()
		defer r.subsMu.Unlock()
		if set := r.subs[key]; set != nil {
			delete(set, id)
			if len(set) == 0 {
				delete(r.subs, key)
			}
		}
	}
}

// publish hands a committed row to the session's subscribers. Only the writer
// goroutine calls it, which is what keeps delivery in seq order.
func (r *SessionEventRepo) publish(sc serverutil.Scope, sessionID string, row SessionEventRow) {
	r.subsMu.RLock()
	defer r.subsMu.RUnlock()
	for _, fn := range r.subs[sessionEventKey(sc, sessionID)] {
		fn(row)
	}
	for _, fn := range r.allSubs {
		fn(sc, sessionID, row)
	}
}

// appendAttempts bounds retries of one batch when another process wins the
// seq race (possible on Postgres; on SQLite the transaction holds the write
// lock from BEGIN, see withTxLock).
const appendAttempts = 3

const (
	// maxPendingAppends bounds the queue; past it, appenders wait for the writer.
	maxPendingAppends = 8192
	// maxAppendBatch bounds the rows committed in one transaction.
	maxAppendBatch = 256
)

// pendingAppend is one queued event, or (barrier) a marker that resolves once
// everything queued before it has been written.
type pendingAppend struct {
	sc        serverutil.Scope
	sessionID string
	eventJSON string
	barrier   bool
	// done receives the outcome; nil for AppendEventAsync.
	done chan appendResult
}

type appendResult struct {
	seq int64
	err error
}

// NewSessionEventRepo returns a repo, or nil when db is nil.
func NewSessionEventRepo(db *gorm.DB) *SessionEventRepo {
	if db == nil {
		return nil
	}
	r := &SessionEventRepo{db: db}
	r.qCond = sync.NewCond(&r.qMu)
	return r
}

// AppendEvent persists one ConversationEvent with auto-assigned seq and returns
// the seq once it has committed. If ctx ends first the call returns ctx's error,
// but the event is still written: it is already in the queue, in order.
func (r *SessionEventRepo) AppendEvent(ctx context.Context, sc serverutil.Scope, sessionID string, eventJSON string) (int64, error) {
	if sc.TenantID == "" || sessionID == "" {
		return 0, ErrSessionEventInvalidArgs
	}
	p := &pendingAppend{sc: sc, sessionID: sessionID, eventJSON: eventJSON, done: make(chan appendResult, 1)}
	r.enqueue(p)
	select {
	case res := <-p.done:
		return res.seq, res.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// AppendEventAsync queues one event and returns without waiting for the write,
// for callers on a hot path (a streaming turn emits a frame per token). Order
// with every other append is kept. A write that fails is logged by the writer.
// It returns an error only for invalid arguments.
func (r *SessionEventRepo) AppendEventAsync(sc serverutil.Scope, sessionID string, eventJSON string) error {
	if sc.TenantID == "" || sessionID == "" {
		return ErrSessionEventInvalidArgs
	}
	r.enqueue(&pendingAppend{sc: sc, sessionID: sessionID, eventJSON: eventJSON})
	return nil
}

// Flush waits until every event queued before the call has been written (or
// has failed). Deletes and prunes flush first, so a queued event can never land
// after a delete that was meant to cover it.
func (r *SessionEventRepo) Flush(ctx context.Context) error {
	p := &pendingAppend{barrier: true, done: make(chan appendResult, 1)}
	r.enqueue(p)
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *SessionEventRepo) enqueue(p *pendingAppend) {
	r.qMu.Lock()
	for len(r.queue) >= maxPendingAppends {
		r.qCond.Wait()
	}
	r.queue = append(r.queue, p)
	if !r.draining {
		r.draining = true
		go r.drain()
	}
	r.qMu.Unlock()
}

// drain is the writer: it takes whatever is queued, commits it, and repeats
// until the queue is empty. Only one runs at a time (draining).
func (r *SessionEventRepo) drain() {
	for {
		r.qMu.Lock()
		n := len(r.queue)
		if n == 0 {
			r.queue = nil
			r.draining = false
			r.qMu.Unlock()
			return
		}
		if n > maxAppendBatch {
			n = maxAppendBatch
		}
		batch := make([]*pendingAppend, n)
		copy(batch, r.queue[:n])
		r.queue = r.queue[n:]
		r.qCond.Broadcast()
		r.qMu.Unlock()
		r.commitBatch(batch)
	}
}

// commitBatch writes batch in one transaction and reports each outcome. If the
// batch as a whole fails, each event is retried on its own, so one bad event
// does not take the others down with it.
func (r *SessionEventRepo) commitBatch(batch []*pendingAppend) {
	events := make([]*pendingAppend, 0, len(batch))
	for _, p := range batch {
		if !p.barrier {
			events = append(events, p)
		}
	}
	results := make(map[*pendingAppend]appendResult, len(events))
	if len(events) > 0 {
		if rows, err := r.insertWithRetry(events); err == nil {
			for i, p := range events {
				results[p] = appendResult{seq: rows[i].Seq}
				r.publish(p.sc, p.sessionID, rows[i])
			}
		} else {
			llmdlog.Stdout("store", llmdlog.LevelWarn, "session_event: batch of %d failed, writing one at a time: %v", len(events), err)
			for _, p := range events {
				rows, err := r.insertWithRetry([]*pendingAppend{p})
				if err != nil {
					results[p] = appendResult{err: err}
					continue
				}
				results[p] = appendResult{seq: rows[0].Seq}
				r.publish(p.sc, p.sessionID, rows[0])
			}
		}
	}
	for _, p := range batch {
		res := results[p]
		if p.done != nil {
			p.done <- res
		} else if res.err != nil {
			llmdlog.Stdout("store", llmdlog.LevelWarn, "session_event: append failed: scope=%s session=%q err=%v", p.sc.String(), p.sessionID, res.err)
		}
	}
}

func (r *SessionEventRepo) insertWithRetry(events []*pendingAppend) ([]SessionEventRow, error) {
	var lastErr error
	for attempt := 0; attempt < appendAttempts; attempt++ {
		rows, err := r.insert(events)
		if err == nil {
			return rows, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("session_event: append failed after %d attempts: %w", appendAttempts, lastErr)
}

// insert assigns each event the next seq of its session (MAX(seq)+1, read once
// per session in the batch) and writes them all in one transaction. It runs on
// a background context: the writer outlives any one caller's request.
func (r *SessionEventRepo) insert(events []*pendingAppend) ([]SessionEventRow, error) {
	rows := make([]SessionEventRow, 0, len(events))
	err := r.db.WithContext(context.Background()).Transaction(func(tx *gorm.DB) error {
		next := make(map[string]int64)
		for _, p := range events {
			key := sessionEventKey(p.sc, p.sessionID)
			seq, ok := next[key]
			if !ok {
				if err := tx.Model(&SessionEventRow{}).
					Where("tenant_id = ? AND scope_id = ? AND session_id = ?", p.sc.TenantID, p.sc.ScopeID, p.sessionID).
					Select("COALESCE(MAX(seq), 0)").
					Scan(&seq).Error; err != nil {
					return err
				}
			}
			seq++
			next[key] = seq
			rows = append(rows, SessionEventRow{
				TenantID: p.sc.TenantID, ScopeID: p.sc.ScopeID,
				SessionID: p.sessionID,
				Seq:       seq,
				EventJSON: p.eventJSON,
			})
		}
		return tx.Create(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// LastSeq returns the highest seq recorded for the session (0 when none): the
// cursor a subscriber passes as after_seq to receive only what comes next.
// Waits for queued appends first, so it counts everything appended before it.
func (r *SessionEventRepo) LastSeq(ctx context.Context, sc serverutil.Scope, sessionID string) (int64, error) {
	if sc.TenantID == "" || sessionID == "" {
		return 0, ErrSessionEventInvalidArgs
	}
	if err := r.Flush(ctx); err != nil {
		return 0, err
	}
	var maxSeq int64
	err := r.db.WithContext(ctx).
		Model(&SessionEventRow{}).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sessionID).
		Select("COALESCE(MAX(seq), 0)").
		Scan(&maxSeq).Error
	return maxSeq, err
}

// GetEventsSince retrieves all events since afterSeq (exclusive).
// Returns events ordered by seq ascending. It does not wait for queued appends:
// a tail subscribes before it reads, so rows still queued reach it live, and
// waiting here would slow every catch-up read during a stream.
func (r *SessionEventRepo) GetEventsSince(ctx context.Context, sc serverutil.Scope, sessionID string, afterSeq int64) ([]SessionEventRow, error) {
	if sc.TenantID == "" || sessionID == "" {
		return nil, ErrSessionEventInvalidArgs
	}

	var events []SessionEventRow
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND seq > ?", sc.TenantID, sc.ScopeID, sessionID, afterSeq).
		Order("seq ASC").
		Find(&events)

	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		return nil, result.Error
	}

	return events, nil
}

// GetAllEvents retrieves all events for a session ordered by seq, including
// any appended before the call that were still queued.
func (r *SessionEventRepo) GetAllEvents(ctx context.Context, sc serverutil.Scope, sessionID string) ([]SessionEventRow, error) {
	if sc.TenantID == "" || sessionID == "" {
		return nil, ErrSessionEventInvalidArgs
	}
	if err := r.Flush(ctx); err != nil {
		return nil, err
	}

	var events []SessionEventRow
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sessionID).
		Order("seq ASC").
		Find(&events)

	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		return nil, result.Error
	}

	return events, nil
}

// PruneEventsBefore deletes a session's events with seq < beforeSeq. Called when
// a turn commits, with the seq that turn started after: everything older belongs
// to turns already committed to history, and no client subscribes from before
// a turn's start (cursors come from events_seq / turn_start_seq). Strictly-less
// keeps the row AT beforeSeq, so MAX(seq) never moves backwards and seq stays
// monotonic even when the pruning turn appended nothing. Only ever called on
// commit, never on read.
func (r *SessionEventRepo) PruneEventsBefore(ctx context.Context, sc serverutil.Scope, sessionID string, beforeSeq int64) (int64, error) {
	if sc.TenantID == "" || sessionID == "" {
		return 0, ErrSessionEventInvalidArgs
	}
	if beforeSeq <= 1 {
		return 0, nil
	}
	if err := r.Flush(ctx); err != nil {
		return 0, err
	}
	res := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND seq < ?", sc.TenantID, sc.ScopeID, sessionID, beforeSeq).
		Delete(&SessionEventRow{})
	return res.RowsAffected, res.Error
}

// TrimToNewest deletes every event of a session except the newest one. The
// kept row holds MAX(seq), so seq stays monotonic. Called after a turn's
// history is committed: readers get finished turns from history.
func (r *SessionEventRepo) TrimToNewest(ctx context.Context, sc serverutil.Scope, sessionID string) (int64, error) {
	if sc.TenantID == "" || sessionID == "" {
		return 0, ErrSessionEventInvalidArgs
	}
	if err := r.Flush(ctx); err != nil {
		return 0, err
	}
	res := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND seq < (SELECT MAX(seq) FROM llmd_session_events WHERE tenant_id = ? AND scope_id = ? AND session_id = ?)",
			sc.TenantID, sc.ScopeID, sessionID, sc.TenantID, sc.ScopeID, sessionID).
		Delete(&SessionEventRow{})
	return res.RowsAffected, res.Error
}

// DeleteEventsForSession deletes all events for a session (used by eviction policy).
func (r *SessionEventRepo) DeleteEventsForSession(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if sc.TenantID == "" || sessionID == "" {
		return ErrSessionEventInvalidArgs
	}
	if err := r.Flush(ctx); err != nil {
		return err
	}

	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sessionID).
		Delete(&SessionEventRow{}).Error
}

// CountEventsForSession returns the number of events in a session.
func (r *SessionEventRepo) CountEventsForSession(ctx context.Context, sc serverutil.Scope, sessionID string) (int64, error) {
	if sc.TenantID == "" || sessionID == "" {
		return 0, ErrSessionEventInvalidArgs
	}
	if err := r.Flush(ctx); err != nil {
		return 0, err
	}

	var count int64
	result := r.db.WithContext(ctx).
		Model(&SessionEventRow{}).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sessionID).
		Count(&count)

	return count, result.Error
}

// EvictSessionEvents deletes all events for a session (called by eviction policy for finished sessions).
func (r *SessionEventRepo) EvictSessionEvents(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	return r.DeleteEventsForSession(ctx, sc, sessionID)
}
