package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/johnewart/pyxis/internal/serverutil"
	"strings"
	"time"

	"github.com/google/uuid"
	sqlite3 "github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

// retryBusy runs a write, retrying while SQLite reports the transient
// contention errors.
//
// Needed because several drainers write to this table at once by design. Two
// concurrent writers do not always queue up politely: SQLITE_BUSY is retried by
// the busy_timeout pragma, but SQLITE_LOCKED (a table-level conflict, which is
// what shared-cache connections produce) bypasses the busy handler entirely and
// surfaces immediately. Both mean "try again shortly", never "this write was
// wrong", so the queue absorbs them here rather than handing a drainer a
// spurious failure and losing its place in line.
func retryBusy(fn func() error) error {
	const attempts = 24
	backoff := 500 * time.Microsecond
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if !isBusyErr(err) {
			return err
		}
		time.Sleep(backoff)
		if backoff < 20*time.Millisecond {
			backoff *= 2
		}
	}
	return err
}

func isBusyErr(err error) bool {
	var se sqlite3.Error
	if errors.As(err, &se) {
		return se.Code == sqlite3.ErrBusy || se.Code == sqlite3.ErrLocked
	}
	return false
}

// ErrQueuePaused is returned by ClaimNext when the session's queue is paused.
// Distinct from "nothing to claim": a drainer loop should keep polling rather
// than conclude the queue is drained and stop.
var ErrQueuePaused = errors.New("session queue is paused")

// ErrQueueEntryNotClaimed is returned when a heartbeat or retire names an entry
// the caller does not currently own — because its lease expired and someone else
// took it, or it was removed from the queue while running.
var ErrQueueEntryNotClaimed = errors.New("queue entry not claimed by this owner")

// DefaultClaimLease is how long a claim is believed without a heartbeat.
//
// Deliberately far shorter than a turn can legitimately run: CLI-backed turns
// go on for many minutes, so guessing a maximum turn length would mean either
// reclaiming live work or wedging the queue for ages after a crash. Instead the
// lease is short and the drainer heartbeats while its turn is actually
// streaming, which is the only honest evidence that it is still alive.
const DefaultClaimLease = 2 * time.Minute

// SessionQueueRepo owns the durable per-session message queue.
//
// llmd does not drain the queue — drainers are ordinary clients (a host process
// looping in the background, a browser tab while open) and there can be several
// at once. What lives here is the state and the guarantees that make competing
// drainers safe rather than a double-send machine: claims are atomic, they
// expire, pause is enforced here rather than trusted to callers, and an entry
// that already started a turn is never handed out again.
type SessionQueueRepo struct {
	db *gorm.DB
	// lease is the claim duration; overridable so tests do not have to sleep for
	// minutes to exercise reclaim.
	lease time.Duration
	// now is the clock, overridable in tests for the same reason.
	now func() time.Time
}

// NewSessionQueueRepo returns a repo backed by db, or nil if db is nil.
func NewSessionQueueRepo(db *gorm.DB) *SessionQueueRepo {
	if db == nil {
		return nil
	}
	return &SessionQueueRepo{db: db, lease: DefaultClaimLease, now: time.Now}
}

// WithLeaseForTest overrides the claim lease and clock. Test-only seam.
func (r *SessionQueueRepo) WithLeaseForTest(lease time.Duration, now func() time.Time) *SessionQueueRepo {
	if r == nil {
		return nil
	}
	cp := *r
	if lease > 0 {
		cp.lease = lease
	}
	if now != nil {
		cp.now = now
	}
	return &cp
}

func (r *SessionQueueRepo) clock() time.Time {
	if r == nil || r.now == nil {
		return time.Now()
	}
	return r.now()
}

// Enqueue appends a message to the back of a session's queue and returns the
// new entry. Position is max(position)+1 within the session, so ordinary
// enqueues never need to touch existing rows.
func (r *SessionQueueRepo) Enqueue(ctx context.Context, sc serverutil.Scope, sessionID, content string) (*SessionQueuedMessageRow, error) {
	return r.EnqueueWithAttachments(ctx, sc, sessionID, content, nil, QueuedTurnOptions{})
}

// EnqueueWithAttachments is Enqueue for a message that carries attachments
// and/or per-turn options.
func (r *SessionQueueRepo) EnqueueWithAttachments(ctx context.Context, sc serverutil.Scope, sessionID, content string, attachments []QueuedAttachment, opts QueuedTurnOptions) (*SessionQueuedMessageRow, error) {
	return r.enqueue(ctx, sc, sessionID, content, attachments, opts, false)
}

// frontPositionOffset pushes front-of-queue positions below every ordinary one:
// appends use UnixNano (~1.8e18) and Reorder renumbers to 1..n, so now-4e18 is
// always negative, while still increasing with time so several front entries
// keep their own arrival order.
const frontPositionOffset = 4e18

// EnqueueFront inserts a message ahead of everything already queued (still
// subject to pause, and reorderable/removable like any other entry). Used for
// answers to agent ask cards: the agent stopped to wait for exactly this.
func (r *SessionQueueRepo) EnqueueFront(ctx context.Context, sc serverutil.Scope, sessionID, content string, attachments []QueuedAttachment, opts QueuedTurnOptions) (*SessionQueuedMessageRow, error) {
	return r.enqueue(ctx, sc, sessionID, content, attachments, opts, true)
}

// ReplacePendingAttachments swaps the attachments of a still-pending entry.
// Reports false (no error) when the entry is gone or already running.
func (r *SessionQueueRepo) ReplacePendingAttachments(ctx context.Context, sc serverutil.Scope, sessionID, entryID string, attachments []QueuedAttachment) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("session queue repo not configured")
	}
	raw := ""
	if len(attachments) > 0 {
		b, err := json.Marshal(attachments)
		if err != nil {
			return false, fmt.Errorf("replace attachments: encode: %w", err)
		}
		raw = string(b)
	}
	var affected int64
	err := retryBusy(func() error {
		res := r.db.WithContext(ctx).Model(&SessionQueuedMessageRow{}).
			Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND id = ? AND state = ?",
				sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID), strings.TrimSpace(entryID), string(QueuedPending)).
			Update("attachments_json", raw)
		affected = res.RowsAffected
		return res.Error
	})
	return affected > 0, err
}

func (r *SessionQueueRepo) enqueue(ctx context.Context, sc serverutil.Scope, sessionID, content string, attachments []QueuedAttachment, opts QueuedTurnOptions, front bool) (*SessionQueuedMessageRow, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("session queue repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return nil, errors.New("enqueue: workspace and session are required")
	}
	// Position is wall-clock nanoseconds rather than max(position)+1.
	//
	// Not an optimisation — a correctness choice. max()+1 is a read-then-write,
	// and two clients enqueueing at once would each have to upgrade a read lock
	// to a write lock inside one transaction, which SQLite refuses outright
	// rather than serialising (see ClaimNext for the same trap). A clock-derived
	// position needs no read at all, so concurrent enqueues from two windows
	// cannot conflict. Ordering is still insertion order, and Reorder's
	// renumbering to 1..n stays below any future timestamp, so a reordered queue
	// keeps accepting appends at the back.
	row := SessionQueuedMessageRow{
		ID:       uuid.New().String(),
		TenantID: sc.TenantID, ScopeID: sc.ScopeID,
		SessionID: sid,
		Content:   content,
		State:     string(QueuedPending),
		Position:  float64(r.clock().UnixNano()),

		DisableTools: opts.DisableTools,
	}
	if front {
		row.Position -= frontPositionOffset
	}
	if len(attachments) > 0 {
		raw, err := json.Marshal(attachments)
		if err != nil {
			return nil, fmt.Errorf("enqueue: encode attachments: %w", err)
		}
		row.AttachmentsJSON = string(raw)
	}
	if err := retryBusy(func() error {
		return r.db.WithContext(ctx).Create(&row).Error
	}); err != nil {
		return nil, err
	}
	return &row, nil
}

// List returns a session's queue in run order (pending and running alike), so a
// UI can show what is about to happen and what is happening now as one list.
func (r *SessionQueueRepo) List(ctx context.Context, sc serverutil.Scope, sessionID string) ([]SessionQueuedMessageRow, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	var rows []SessionQueuedMessageRow
	err := retryBusy(func() error {
		rows = nil
		return r.db.WithContext(ctx).
			Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID)).
			Order("position ASC").
			Find(&rows).Error
	})
	return rows, err
}

// QueuedSession names a session that has at least one queued entry.
type QueuedSession struct {
	Scope     serverutil.Scope
	SessionID string
}

// SessionsWithQueuedWork lists every session (across all scopes) that has
// queued entries. Used by the server drainer's startup sweep, so work queued
// before a restart is not stranded until someone touches the session.
func (r *SessionQueueRepo) SessionsWithQueuedWork(ctx context.Context) ([]QueuedSession, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	type row struct {
		TenantID  string
		ScopeID   string
		SessionID string
	}
	var rows []row
	if err := retryBusy(func() error {
		rows = nil
		return r.db.WithContext(ctx).Model(&SessionQueuedMessageRow{}).
			Distinct("tenant_id", "scope_id", "session_id").
			Scan(&rows).Error
	}); err != nil {
		return nil, err
	}
	out := make([]QueuedSession, 0, len(rows))
	for _, q := range rows {
		out = append(out, QueuedSession{Scope: serverutil.Scope{TenantID: q.TenantID, ScopeID: q.ScopeID}, SessionID: q.SessionID})
	}
	return out, nil
}

// Remove deletes one entry by id and reports whether it existed.
//
// Removing an entry that is currently running does not stop the turn — that is
// cancellation's job, and the turn is already in the transcript. It only means
// the queue stops tracking it.
func (r *SessionQueueRepo) Remove(ctx context.Context, sc serverutil.Scope, sessionID, entryID string) (bool, error) {
	if r == nil || r.db == nil {
		return false, nil
	}
	var affected int64
	err := retryBusy(func() error {
		res := r.db.WithContext(ctx).
			Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND id = ?",
				sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID), strings.TrimSpace(entryID)).
			Delete(&SessionQueuedMessageRow{})
		affected = res.RowsAffected
		return res.Error
	})
	return affected > 0, err
}

// Reorder rewrites queue order to exactly the given entry ids.
//
// Takes the full desired order rather than a move instruction: a reorder is a
// user acting on the list they were just looking at, and applying it wholesale
// makes the outcome independent of what changed underneath. Ids that no longer
// exist are ignored (an entry may have run or been removed since the UI read
// it); entries not mentioned keep their relative order AFTER the listed ones,
// so a partial reorder can never silently drop work from the queue.
func (r *SessionQueueRepo) Reorder(ctx context.Context, sc serverutil.Scope, sessionID string, entryIDs []string) error {
	if r == nil || r.db == nil {
		return nil
	}
	sid := strings.TrimSpace(sessionID)
	// Read outside the write transaction, then apply. Holding a read inside the
	// transaction would need the same read→write lock upgrade SQLite refuses (see
	// ClaimNext). The read is therefore a snapshot that may be slightly stale: an
	// entry that ran in the meantime is skipped by id, and one enqueued in the
	// meantime is simply not mentioned, which the unmentioned-entries rule below
	// already handles by keeping it in the queue.
	var rows []SessionQueuedMessageRow
	if err := retryBusy(func() error {
		rows = nil
		return r.db.WithContext(ctx).
			Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).
			Order("position ASC").Find(&rows).Error
	}); err != nil {
		return err
	}
	return retryBusy(func() error {
		return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			byID := make(map[string]*SessionQueuedMessageRow, len(rows))
			for i := range rows {
				byID[rows[i].ID] = &rows[i]
			}
			pos := 0.0
			placed := make(map[string]bool, len(entryIDs))
			for _, id := range entryIDs {
				row, ok := byID[strings.TrimSpace(id)]
				if !ok || placed[row.ID] {
					continue
				}
				pos++
				placed[row.ID] = true
				if err := tx.Model(&SessionQueuedMessageRow{}).
					Where("id = ?", row.ID).Update("position", pos).Error; err != nil {
					return err
				}
			}
			// Anything the caller did not mention keeps its existing relative order,
			// appended after the explicitly ordered entries.
			for i := range rows {
				if placed[rows[i].ID] {
					continue
				}
				pos++
				if err := tx.Model(&SessionQueuedMessageRow{}).
					Where("id = ?", rows[i].ID).Update("position", pos).Error; err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// SetPaused pauses or resumes draining for a session.
func (r *SessionQueueRepo) SetPaused(ctx context.Context, sc serverutil.Scope, sessionID string, paused bool) error {
	if r == nil || r.db == nil {
		return nil
	}
	return retryBusy(func() error {
		return r.db.WithContext(ctx).Model(&SessionRow{}).
			Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID)).
			Update("queue_paused", paused).Error
	})
}

// Paused reports whether draining is paused for a session.
func (r *SessionQueueRepo) Paused(ctx context.Context, sc serverutil.Scope, sessionID string) (bool, error) {
	if r == nil || r.db == nil {
		return false, nil
	}
	var paused *bool
	if err := retryBusy(func() error {
		paused = nil
		return r.db.WithContext(ctx).Model(&SessionRow{}).
			Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID)).
			Select("queue_paused").Scan(&paused).Error
	}); err != nil {
		return false, err
	}
	return paused != nil && *paused, nil
}

// ClaimNext atomically hands the head of the queue to one drainer.
//
// This is the whole reason the queue lives in llmd rather than in whichever
// client happens to be draining. Several drainers can be looping at once, and
// the claim is decided inside a single transaction, so exactly one of them can
// win an entry. Returns (nil, nil) when there is nothing claimable, and
// ErrQueuePaused when the session is paused — a caller must be able to tell
// "nothing to do right now" from "drained", or a pause turns into a spin.
//
// Entries whose claim has expired are reclaimable, EXCEPT when they already
// started a turn (RanTurnID set): that message has been delivered, and handing
// it out again would duplicate it in the conversation. Such an entry is retired
// here instead, on the grounds that its turn is already the record of it.
func (r *SessionQueueRepo) ClaimNext(ctx context.Context, sc serverutil.Scope, sessionID, owner string) (*SessionQueuedMessageRow, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	sid := strings.TrimSpace(sessionID)
	own := strings.TrimSpace(owner)
	if own == "" {
		return nil, errors.New("claim: owner is required")
	}
	paused, err := r.Paused(ctx, sc, sid)
	if err != nil {
		return nil, err
	}
	if paused {
		return nil, ErrQueuePaused
	}

	// Claiming is a compare-and-swap, not a transaction around a read.
	//
	// The obvious implementation — BEGIN; SELECT ... FOR UPDATE; UPDATE; COMMIT —
	// does not work here. SQLite has no FOR UPDATE, and a transaction that reads
	// and then writes has to upgrade a read lock to a write lock, which SQLite
	// fails immediately (SQLITE_LOCKED/BUSY) rather than serialising, so
	// competing drainers all lost and nobody claimed anything. Instead: read a
	// candidate with no lock at all, then attempt a single conditional UPDATE
	// whose WHERE clause re-asserts everything the read assumed. One writer wins,
	// the losers see RowsAffected == 0 and look again. A single UPDATE statement
	// is atomic on both SQLite and Postgres without any explicit locking.
	const maxAttempts = 16
	for attempt := 0; attempt < maxAttempts; attempt++ {
		now := r.clock()
		var rows []SessionQueuedMessageRow
		// Every DB access is rebuilt inside its own retry: a *gorm.DB chain is
		// single-use, so re-executing one after a failure yields "invalid
		// transaction" rather than a second attempt.
		if err := retryBusy(func() error {
			rows = nil
			return r.db.WithContext(ctx).
				Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).
				Order("position ASC").Limit(8).Find(&rows).Error
		}); err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, nil
		}

		candidate, retire := claimCandidate(rows, now)
		if retire != nil {
			// Delivered by a drainer that then died: its turn is already the
			// record of the message, so the entry is retired rather than re-run.
			// Guarded on the lapsed lease so a drainer that comes back to life
			// mid-retire cannot have its live claim deleted underneath it.
			if err := retryBusy(func() error {
				return r.db.WithContext(ctx).
					Where("id = ? AND ran_turn_id <> ? AND claim_expires_at < ?", retire.ID, "", now).
					Delete(&SessionQueuedMessageRow{}).Error
			}); err != nil {
				return nil, err
			}
			continue
		}
		if candidate == nil {
			// A live claim sits at the head: the queue is busy, not drained.
			return nil, nil
		}

		expiry := now.Add(r.lease)
		updates := map[string]any{
			"state":            string(QueuedRunning),
			"claim_owner":      own,
			"claim_expires_at": expiry,
		}
		wasPending := candidate.State == string(QueuedPending)
		var affected int64
		if err := retryBusy(func() error {
			q := r.db.WithContext(ctx).Model(&SessionQueuedMessageRow{}).Where("id = ?", candidate.ID)
			if wasPending {
				// Only one updater can move a row out of pending.
				q = q.Where("state = ?", string(QueuedPending))
			} else {
				// Reclaiming an abandoned entry: only valid while the lease is
				// still lapsed. The first winner pushes claim_expires_at into the
				// future, which is what makes every other contender's UPDATE match
				// no rows.
				q = q.Where("state = ? AND claim_expires_at < ?", string(QueuedRunning), now)
			}
			res := q.Updates(updates)
			affected = res.RowsAffected
			return res.Error
		}); err != nil {
			return nil, err
		}
		if affected == 0 {
			// Another drainer got there first; re-read and try the new head.
			continue
		}
		claimed := *candidate
		claimed.State = string(QueuedRunning)
		claimed.ClaimOwner = own
		claimed.ClaimExpiresAt = &expiry
		return &claimed, nil
	}
	// Persistent contention. Reporting "nothing claimable" (rather than an error)
	// is right for a drainer loop: it will come back around.
	return nil, nil
}

// claimCandidate picks the entry a drainer should try to take from the head of
// the queue, given the rows as they were read at `now`.
//
// Returns (candidate, nil) for something to attempt a claim on, (nil, entry) for
// an abandoned entry that must be retired rather than re-run, and (nil, nil) when
// the head is legitimately busy.
func claimCandidate(rows []SessionQueuedMessageRow, now time.Time) (candidate, retire *SessionQueuedMessageRow) {
	for i := range rows {
		row := &rows[i]
		if row.State == string(QueuedPending) {
			return row, nil
		}
		// Running. Only reachable once its lease has lapsed; a live claim at the
		// head means the queue is busy and later entries must NOT be run out of
		// order ahead of it.
		if row.ClaimExpiresAt == nil || row.ClaimExpiresAt.After(now) {
			return nil, nil
		}
		if strings.TrimSpace(row.RanTurnID) != "" {
			return nil, row
		}
		return row, nil
	}
	return nil, nil
}

// Heartbeat extends a claim the caller still owns, and is the drainer's way of
// saying "my turn is genuinely still running". Fails with
// ErrQueueEntryNotClaimed once the lease has been lost, which is a drainer's
// signal to abandon the work rather than keep streaming into a turn someone
// else may now be running.
func (r *SessionQueueRepo) Heartbeat(ctx context.Context, sc serverutil.Scope, sessionID, entryID, owner string) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.ownedWrite(func() *gorm.DB {
		return r.db.WithContext(ctx).Model(&SessionQueuedMessageRow{}).
			Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND id = ? AND claim_owner = ?",
				sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID),
				strings.TrimSpace(entryID), strings.TrimSpace(owner)).
			Update("claim_expires_at", r.clock().Add(r.lease))
	})
}

// ownedWrite runs a write that is scoped to the claiming owner, retrying
// transient lock contention and translating "matched no rows" into
// ErrQueueEntryNotClaimed — for these operations that always means the caller no
// longer owns the entry, never that the write was malformed.
func (r *SessionQueueRepo) ownedWrite(build func() *gorm.DB) error {
	var affected int64
	if err := retryBusy(func() error {
		res := build()
		affected = res.RowsAffected
		return res.Error
	}); err != nil {
		return err
	}
	if affected == 0 {
		return ErrQueueEntryNotClaimed
	}
	return nil
}

// MarkStarted records the turn a claimed entry produced. A drainer calls this as
// soon as it knows the turn id, BEFORE the turn completes: the whole point is
// that if the drainer dies mid-turn, whoever reclaims can see the message was
// already delivered and retire it instead of sending it twice.
func (r *SessionQueueRepo) MarkStarted(ctx context.Context, sc serverutil.Scope, sessionID, entryID, owner, turnID string) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.ownedWrite(func() *gorm.DB {
		return r.db.WithContext(ctx).Model(&SessionQueuedMessageRow{}).
			Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND id = ? AND claim_owner = ?",
				sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID),
				strings.TrimSpace(entryID), strings.TrimSpace(owner)).
			Update("ran_turn_id", strings.TrimSpace(turnID))
	})
}

// Retire removes a finished entry. Scoped to the claiming owner so a drainer
// that lost its lease cannot delete work another drainer has since picked up.
func (r *SessionQueueRepo) Retire(ctx context.Context, sc serverutil.Scope, sessionID, entryID, owner string) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.ownedWrite(func() *gorm.DB {
		return r.db.WithContext(ctx).
			Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND id = ? AND claim_owner = ?",
				sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID),
				strings.TrimSpace(entryID), strings.TrimSpace(owner)).
			Delete(&SessionQueuedMessageRow{})
	})
}

// Release returns a claimed entry to the queue without running it — the polite
// counterpart to letting a lease lapse, for a drainer that is shutting down
// cleanly. Keeps its position, so shutting a tab does not send its queued work
// to the back of the line.
func (r *SessionQueueRepo) Release(ctx context.Context, sc serverutil.Scope, sessionID, entryID, owner string) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.ownedWrite(func() *gorm.DB {
		return r.db.WithContext(ctx).Model(&SessionQueuedMessageRow{}).
			Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND id = ? AND claim_owner = ? AND ran_turn_id = ?",
				sc.TenantID, sc.ScopeID, strings.TrimSpace(sessionID),
				strings.TrimSpace(entryID), strings.TrimSpace(owner), "").
			Updates(map[string]any{
				"state":            string(QueuedPending),
				"claim_owner":      "",
				"claim_expires_at": nil,
			})
	})
}
