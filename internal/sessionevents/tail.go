// Package sessionevents serves a session's conversation events to a
// subscriber: replay from the durable event log, then follow it live.
//
// The event log (llmd_session_events, seq-ordered per session) is the source
// of truth, and it is push-based: SessionEventRepo.AppendEvent hands every
// committed row to the session's live subscribers, in seq order. A tail reads
// the table exactly once to replay what it missed, then delivers pushed rows
// as they arrive. It reads the table again only when the push stream has a
// hole in it (a seq gap, or its buffer overflowed), and the log fills the
// hole. Nothing polls: an idle subscriber costs no queries at all.
//
// Because every row carries its seq, the subscriber can tell exactly what it
// has already sent, so every frame arrives exactly once, in seq order, with no
// gap between replay and live. That is what makes this safe for rebuilding a
// live assistant reply after a reload.
//
// Shared by the gRPC SubscribeSessionEvents RPC (internal/server) and the
// browser WebSocket endpoint (internal/serve), which cannot import each other.
package sessionevents

import (
	"context"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/protobuf/encoding/protojson"
)

// Log is the durable, seq-ordered, push-capable event source
// (store.SessionEventRepo).
type Log interface {
	GetEventsSince(ctx context.Context, sc serverutil.Scope, sessionID string, afterSeq int64) ([]store.SessionEventRow, error)
	Subscribe(sc serverutil.Scope, sessionID string, fn func(store.SessionEventRow)) (unsubscribe func())
}

// LiveBuffer is how many pushed rows a slow subscriber may fall behind by
// before it stops buffering and catches up from the log instead.
var LiveBuffer = 512

// ReadRetryInterval is how long a tail waits before re-reading the log after
// a read failed. It is the only timer here and runs only after a failure.
var ReadRetryInterval = time.Second

var unmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}

// Tail replays every event after afterSeq (0 = from the start), then follows
// the log live until ctx ends or send fails. send's error ends the tail and is
// returned.
func Tail(
	ctx context.Context,
	log Log,
	sc serverutil.Scope,
	sessionID string,
	afterSeq int64,
	send func(*llmv1.ConversationStreamFrame) error,
	module string,
) error {
	live := make(chan store.SessionEventRow, LiveBuffer)
	lagged := make(chan struct{}, 1)
	// Subscribe BEFORE the replay read: a row committed while the read runs is
	// then either in the read's result or in live (or both; seq dedupes).
	unsubscribe := log.Subscribe(sc, sessionID, func(row store.SessionEventRow) {
		select {
		case live <- row:
		default:
			// Full: drop it and catch up from the log once we drain.
			select {
			case lagged <- struct{}{}:
			default:
			}
		}
	})
	defer unsubscribe()

	deliver := func(row store.SessionEventRow) error {
		afterSeq = row.Seq
		frame := &llmv1.ConversationStreamFrame{}
		if err := unmarshal.Unmarshal([]byte(row.EventJSON), frame); err != nil {
			llmdlog.Stdout(module, llmdlog.LevelWarn,
				"session events tail: skipping unparsable row session=%q seq=%d err=%v", sessionID, row.Seq, err)
			return nil
		}
		return send(frame)
	}

	needRead := true // the initial replay
	var retry <-chan time.Time
	for {
		if needRead && retry == nil {
			rows, err := log.GetEventsSince(ctx, sc, sessionID, afterSeq)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				llmdlog.Stdout(module, llmdlog.LevelWarn,
					"session events tail: read failed session=%q %s after_seq=%d err=%v (retrying in %s)",
					sessionID, sc.String(), afterSeq, err, ReadRetryInterval)
				retry = time.After(ReadRetryInterval)
			} else {
				needRead = false
				for _, row := range rows {
					if row.Seq <= afterSeq {
						continue
					}
					if err := deliver(row); err != nil {
						return err
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-retry:
			retry = nil
		case <-lagged:
			needRead = true
		case row := <-live:
			switch {
			case row.Seq <= afterSeq:
				// Already delivered by a log read.
			case row.Seq == afterSeq+1:
				if err := deliver(row); err != nil {
					return err
				}
			default:
				// A hole before this row (dropped while we were full). The log
				// has everything, this row included.
				needRead = true
			}
		}
	}
}
