package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

// turnKey identifies a session within its scope so two tenants that happen to
// share a session id never share a turn counter.
type turnKey struct {
	sc        serverutil.Scope
	sessionID string
}

// TurnRegister issues gap-free, monotonically increasing turn sequence numbers
// per session, backed by llmd_session_turns. It is an in-memory cache over the
// persisted table (see project rule: llmd in-memory state is an evictable
// cache over persisted state): Bootstrap reconstructs the last-issued
// turn_seq from storage after a restart, and Advance both increments the
// in-memory counter and persists the new row so a later restart can
// Bootstrap past it without collision.
type TurnRegister struct {
	db *gorm.DB

	mu      sync.Mutex
	current map[turnKey]int64 // (scope, sessionID) -> last-issued turn_seq (0 = none issued)
	// currentTurnID mirrors `current` but tracks the caller-supplied
	// (external) turn id string of the last-advanced turn per session. Used
	// for cross-transport tool-event attribution: MCP-dispatched tool calls
	// (internal/mcpserver/server.go's handleToolsCall) run in a separate HTTP
	// request from the ChatStream/advanceTurn call that minted the turn, so
	// they need a way to look up "what turn is active right now" instead of
	// carrying it as a local variable. NOT reconstructed by Bootstrap (no
	// external_turn_id query against storage), so it's "" until the first
	// Advance in a given process — callers must tolerate an empty TurnId.
	currentTurnID map[turnKey]string
}

// NewTurnRegister constructs a TurnRegister over db. db may be nil for
// in-memory-only use (tests); persistence and Bootstrap become no-ops.
func NewTurnRegister(db *gorm.DB) *TurnRegister {
	return &TurnRegister{db: db, current: make(map[turnKey]int64), currentTurnID: make(map[turnKey]string)}
}

// Current returns the last-issued turn_seq for a session as known to this
// process (0 if none has been issued or bootstrapped yet).
func (r *TurnRegister) Current(sc serverutil.Scope, sessionID string) int64 {
	if r == nil {
		return 0
	}
	k := turnKey{sc: sc, sessionID: strings.TrimSpace(sessionID)}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current[k]
}

// CurrentTurnID returns the external (client-supplied) turn id last advanced
// for this session in this process, or "" if none has been issued yet (see
// the currentTurnID field doc for why this isn't restart-safe the way
// Current/Bootstrap are).
func (r *TurnRegister) CurrentTurnID(sc serverutil.Scope, sessionID string) string {
	if r == nil {
		return ""
	}
	k := turnKey{sc: sc, sessionID: strings.TrimSpace(sessionID)}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.currentTurnID[k]
}

// Bootstrap reconstructs the in-memory counter for a session from the
// persisted MAX(turn_seq), so a restarted process resumes numbering without
// colliding with rows written before the restart. Safe to call repeatedly;
// it never lowers an in-memory value already advanced past what's persisted.
func (r *TurnRegister) Bootstrap(ctx context.Context, sc serverutil.Scope, sessionID string) (int64, error) {
	if r == nil {
		return 0, nil
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return 0, nil
	}
	k := turnKey{sc: sc, sessionID: sid}
	var persisted int64
	if r.db != nil {
		var maxSeq *int64
		if err := scoped(r.db.WithContext(ctx), sc).
			Model(&SessionTurnRow{}).
			Where("session_id = ?", sid).
			Select("MAX(turn_seq)").
			Scan(&maxSeq).Error; err != nil {
			return 0, err
		}
		if maxSeq != nil {
			persisted = *maxSeq
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if persisted > r.current[k] {
		r.current[k] = persisted
	}
	return r.current[k], nil
}

// Advance issues the next turn_seq for a session: increments the in-memory
// counter and persists a new llmd_session_turns row. Callers spanning a
// process restart should call Bootstrap first so Advance never collides with
// rows already committed by a prior process.
func (r *TurnRegister) Advance(ctx context.Context, sc serverutil.Scope, sessionID string, startedAt time.Time, externalTurnID string) (int64, error) {
	if r == nil {
		return 0, nil
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return 0, errors.New("turn register: empty session id")
	}
	k := turnKey{sc: sc, sessionID: sid}

	// First advance for this session in THIS process: reconstruct the counter
	// from the persisted MAX(turn_seq) so we resume past turns committed before a
	// restart instead of colliding on turn_seq=1 (the llmd_session_turns unique
	// key), which would error and silently leave every subsequent row's turn_seq
	// NULL. Bootstrap is a cheap no-op for a brand-new session (MAX = 0). Without
	// this, callers would have to remember to Bootstrap; self-healing here keeps
	// turn numbering correct across restarts for every path.
	r.mu.Lock()
	known := r.current[k]
	r.mu.Unlock()
	if known == 0 {
		if _, err := r.Bootstrap(ctx, sc, sid); err != nil {
			return 0, err
		}
	}

	r.mu.Lock()
	next := r.current[k] + 1
	r.mu.Unlock()

	row := SessionTurnRow{
		TenantID:       sc.TenantID,
		ScopeID:        sc.ScopeID,
		SessionID:      sid,
		TurnSeq:        next,
		StartedAt:      startedAt,
		ExternalTurnID: strings.TrimSpace(externalTurnID),
	}
	if r.db != nil {
		if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
			return 0, err
		}
	}

	r.mu.Lock()
	if next > r.current[k] {
		r.current[k] = next
		r.currentTurnID[k] = row.ExternalTurnID
	}
	r.mu.Unlock()
	return next, nil
}
