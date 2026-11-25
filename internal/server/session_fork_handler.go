package server

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ForkSession clones a session into a new, independent session. See
// llm.v1.ForkSessionRequest for the contract and store.SessionRepo.ForkSession
// for the cutoff rule and exactly what is copied.
//
// Running turns: a fork may never include a turn that is still in flight (it
// can be missing tool results or its final assistant message). If the source's
// turn gate is held, the current turn_seq is passed to the store, which refuses
// the fork inside its transaction when the copied range reaches that turn.
// Forking from a completed earlier message while a turn runs is fine: new
// rows always land past the cutoff.
func (s *LlmServer) ForkSession(ctx context.Context, req *llmv1.ForkSessionRequest) (*llmv1.ForkSessionResponse, error) {
	src, err := requireSessionID(req.GetSourceSessionId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "source_session_id is required")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if s.SessionRepo == nil {
		return nil, status.Error(codes.FailedPrecondition, "session repo not configured")
	}
	dst := strings.TrimSpace(req.GetNewSessionId())
	if dst == "" {
		dst = uuid.NewString()
	}
	if dst == src {
		return nil, status.Error(codes.InvalidArgument, "new_session_id must differ from source_session_id")
	}

	opts := store.ForkOptions{Title: req.GetTitle()}
	if s.sessionTurnGate().busy(src) {
		seq := s.TurnRegister.Current(sc, src)
		if seq <= 0 {
			// A turn is running but its turn_seq is unknown to this process, so
			// the overlap can't be checked. Refuse rather than risk cloning a
			// half-finished turn.
			return nil, status.Errorf(codes.FailedPrecondition,
				"session %q has a turn in progress; retry once it finishes", src)
		}
		// Before the running turn calls Advance, Current is the previous
		// (completed) turn, so this is conservative for that brief window.
		opts.InProgressTurnSeq = seq
	}

	res, err := s.SessionRepo.ForkSession(ctx, sc, src, dst, req.GetUpToMessageId(), opts)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrForkSourceNotFound):
			return nil, status.Errorf(codes.NotFound, "unknown source_session_id %q", src)
		case errors.Is(err, store.ErrForkMessageNotFound):
			return nil, status.Errorf(codes.NotFound, "message %q not found in session %q", req.GetUpToMessageId(), src)
		case errors.Is(err, store.ErrForkTargetExists):
			return nil, status.Errorf(codes.AlreadyExists, "session %q already exists", dst)
		case errors.Is(err, store.ErrForkTurnInProgress):
			return nil, status.Errorf(codes.FailedPrecondition,
				"fork of session %q at %q reaches into a turn that is still running; fork from an earlier message or retry once it finishes",
				src, req.GetUpToMessageId())
		}
		llmdlog.Stdout("server", llmdlog.LevelWarn, "fork session: tenant=%s scope=%s src=%s dst=%s up_to=%s: %v",
			sc.TenantID, sc.ScopeID, src, dst, req.GetUpToMessageId(), err)
		return nil, status.Errorf(codes.Internal, "fork session: %v", err)
	}

	// Load the fork into the session hub now so it is immediately usable. The
	// data is committed regardless; a load failure here is logged and surfaced,
	// and the next RPC touching the session will retry the load.
	if s.SessionRuntimes != nil {
		if err := s.ensureKnownSession(ctx, dst); err != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "fork session: load fork tenant=%s scope=%s dst=%s: %v",
				sc.TenantID, sc.ScopeID, dst, err)
			return nil, status.Errorf(codes.Internal, "fork created as %q but failed to load: %v", dst, err)
		}
	}

	return &llmv1.ForkSessionResponse{
		SessionId:      res.SessionID,
		CopiedMessages: int32(res.CopiedMessages),
	}, nil
}
