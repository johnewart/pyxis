package server

import (
	"context"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// takeSessionOrEnqueue implements ChatStream's TurnOptions.queue_if_busy: llmd,
// not the client, decides whether a message runs now or waits in the session
// queue.
//
// It runs now only when the session is genuinely free: no turn holds (or is
// waiting for) the session's turn slot, the queue is empty, and the queue is
// not paused. Then the caller gets the held slot back (release) and runs the
// turn as usual. Otherwise the message is appended to the queue and the caller
// gets the entry to report instead.
//
// The slot is taken BEFORE the queue is read and held while it is, so the
// check and the decision are one step: a queued entry a drainer has claimed but
// not yet started counts as busy (it is in the queue, state running), and a
// drainer that claims after the read waits behind this turn at the gate. That
// is the gap a client-side "am I streaming?" check could not close.
//
// A paused queue counts as busy even when empty: a caller that asks for
// queue-if-busy on a paused session is asking to queue (sending now is the
// caller's other option, a plain ChatStream).
func (s *LlmServer) takeSessionOrEnqueue(
	ctx context.Context,
	sid string,
	incoming []*llmv1.Message,
	opts *llmv1.TurnOptions,
) (release func(), queued *llmv1.QueuedMessage, err error) {
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, nil, err
	}
	if rel, ok := s.sessionTurnGate().tryAcquire(sid); ok {
		rows, lerr := s.SessionQueue.List(ctx, sc, sid)
		if lerr != nil {
			rel()
			return nil, nil, status.Errorf(codes.Internal, "queue_if_busy: list queue: %v", lerr)
		}
		paused, perr := s.SessionQueue.Paused(ctx, sc, sid)
		if perr != nil {
			rel()
			return nil, nil, status.Errorf(codes.Internal, "queue_if_busy: queue paused state: %v", perr)
		}
		if len(rows) == 0 && !paused {
			return rel, nil, nil
		}
		rel()
	}

	um := lastCommittedUserMessage(incoming)
	if um == nil {
		return nil, nil, status.Error(codes.InvalidArgument, "queue_if_busy: no user message to queue")
	}
	atts, err := queuedAttachmentsFromProto(um.GetAttachments())
	if err != nil {
		return nil, nil, err
	}
	row, err := s.SessionQueue.EnqueueWithAttachments(ctx, sc, sid, um.GetContent(), atts,
		store.QueuedTurnOptions{DisableTools: opts.GetDisableTools()})
	if err != nil {
		return nil, nil, status.Errorf(codes.Internal, "queue_if_busy: enqueue: %v", err)
	}
	s.announceQueueChanged(ctx, sc, sid)
	s.kickQueueDrain(sc, sid)
	return nil, queuedMessageToProto(*row), nil
}
