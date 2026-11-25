package store

import (
	"context"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// The current turn's agent-authored status headline (set_status) lives in the
// session cache, not on the session row: it is turn-scoped, transient state
// that only means something while a turn runs. Keeping it behind the cache
// means moving it (and other cache data) to memory or Redis later is a change
// to SessionCacheRepo alone.
//
// TurnStatus/SetTurnStatus are the only readers/writers. Writers: set_status
// stores a headline; ChatStream stores "" when a user turn starts and ends.
const (
	turnStatusNamespace = "llmd.turn"
	turnStatusKey       = "status"
)

// TurnStatus returns the session's current turn status, "" when none is set.
func (r *SessionCacheRepo) TurnStatus(ctx context.Context, sc serverutil.Scope, sessionID string) (string, error) {
	v, ok, err := r.Get(ctx, sc, sessionID, turnStatusNamespace, turnStatusKey)
	if err != nil || !ok {
		return "", err
	}
	return string(v), nil
}

// SetTurnStatus stores the session's current turn status; "" clears it.
func (r *SessionCacheRepo) SetTurnStatus(ctx context.Context, sc serverutil.Scope, sessionID, headline string) error {
	if headline == "" {
		_, err := r.Delete(ctx, sc, sessionID, turnStatusNamespace, turnStatusKey)
		return err
	}
	return r.Set(ctx, sc, sessionID, turnStatusNamespace, turnStatusKey, []byte(headline))
}
