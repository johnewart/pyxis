package server

import (
	"context"
	"strings"

	"github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/sessionevents"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SubscribeSessionEvents streams a session's ConversationStreamFrames: every
// event after after_seq (0 = from the start) replayed from the durable event
// log, then followed live for as long as the caller keeps the stream open.
//
// This is how a client rejoins a turn that is still running after it left
// (navigation, reload): the turn keeps appending to the log server-side, and
// this stream delivers those frames exactly once, in seq order, with no gap
// between replay and live. It is also the side channel for session activity
// a caller's own ChatStream never carries (MCP-dispatched tool events from
// CLI-backed providers, session status/title/todos, queue changes).
//
// Live rows are pushed by the event log itself (SessionEventRepo.Subscribe);
// the log is read only to replay and to fill gaps. See internal/sessionevents.
func (s *LlmServer) SubscribeSessionEvents(
	req *llmv1.SubscribeSessionEventsRequest,
	stream llmv1.LlmService_SubscribeSessionEventsServer,
) error {
	// A tail follows the log until the CLIENT leaves — or until the server is
	// shutting down. Without the second, GracefulStop waits on every open
	// subscription forever.
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	if s.BaseContext != nil {
		stop := context.AfterFunc(s.BaseContext, cancel)
		defer stop()
	}
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	sessionID := strings.TrimSpace(req.GetSessionId())
	if sessionID == "" {
		return status.Error(codes.InvalidArgument, "session_id is required")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return err
	}
	if s.SessionEvents == nil {
		// No durable log: nothing to replay and nothing reliable to follow.
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"SubscribeSessionEvents: no session event store configured; nothing to stream session=%q", sessionID)
		return status.Error(codes.FailedPrecondition, "session event store not configured")
	}
	frames := 0
	counted := func(f *llmv1.ConversationStreamFrame) error {
		if err := stream.Send(f); err != nil {
			return err
		}
		frames++
		return nil
	}
	err = sessionevents.Tail(ctx, s.SessionEvents, sc, sessionID, req.GetAfterSeq(), counted, "server")
	reason := "client went away"
	switch {
	case err != nil:
		reason = "write to client failed: " + err.Error()
	case s.BaseContext != nil && s.BaseContext.Err() != nil:
		reason = "server shutting down"
	}
	llmdlog.Stdout("server", llmdlog.LevelInfo,
		"SubscribeSessionEvents ended: session=%q %s after_seq=%d frames_sent=%d reason=%s",
		sessionID, sc.String(), req.GetAfterSeq(), frames, reason)
	return err
}
