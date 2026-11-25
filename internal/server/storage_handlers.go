package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	claudecodeclillm "github.com/johnewart/pyxis/internal/llm/claudecode"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// compactRun is one Compact run. Callers that arrive while it runs wait on
// done and share its result.
type compactRun struct {
	id   string
	done chan struct{}
	res  *llmv1.CompactResponse
	err  error
}

// compactState serializes Compact runs.
type compactState struct {
	mu  sync.Mutex
	cur *compactRun
}

func (s *LlmServer) GetStorageStats(ctx context.Context, req *llmv1.GetStorageStatsRequest) (*llmv1.GetStorageStatsResponse, error) {
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if s.Storage == nil {
		return nil, status.Error(codes.FailedPrecondition, "storage repo not configured")
	}
	st, err := s.Storage.Stats(ctx, sc, int(req.GetLargestSessionsLimit()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "storage stats: %v", err)
	}
	out := &llmv1.GetStorageStatsResponse{
		DatabaseBytes: st.DatabaseBytes,
		FreeBytes:     st.FreeBytes,
		SessionCount:  st.SessionCount,
	}
	for _, t := range st.Tables {
		out.Tables = append(out.Tables, &llmv1.StorageTableStat{Name: t.Name, Bytes: t.Bytes})
	}
	for _, x := range st.LargestSessions {
		out.LargestSessions = append(out.LargestSessions, &llmv1.StorageSessionStat{
			SessionId:         x.SessionID,
			Title:             x.Title,
			ChatSurface:       x.ChatSurface,
			MessageCount:      x.MessageCount,
			Bytes:             x.Bytes,
			LastMessageUnixMs: x.LastMessageMs,
			ScopeId:           x.ScopeID,
		})
	}
	return out, nil
}

func (s *LlmServer) DeleteSessions(ctx context.Context, req *llmv1.DeleteSessionsRequest) (*llmv1.DeleteSessionsResponse, error) {
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if s.SessionRepo == nil {
		return nil, status.Error(codes.FailedPrecondition, "session repo not configured")
	}
	var deleted int32
	for _, raw := range req.GetSessionIds() {
		sid, err := requireSessionID(raw)
		if err != nil {
			return nil, err
		}
		err = s.SessionRepo.PurgeSession(ctx, sc, sid)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, status.Errorf(codes.Internal, "failed to delete session %q: %v", sid, err)
		}
		if err := claudecodeclillm.PurgeSessionTranscripts(sid); err != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "delete sessions: purge transcripts session=%q: %v", sid, err)
		}
		deleted++
	}
	return &llmv1.DeleteSessionsResponse{Deleted: deleted}, nil
}

// Compact trims leftover events then runs VACUUM. The run uses a context the
// server owns, so a caller that disconnects does not stop it; callers that
// arrive during a run wait for it and share its result.
func (s *LlmServer) Compact(ctx context.Context, _ *llmv1.CompactRequest) (*llmv1.CompactResponse, error) {
	if _, err := s.scope(ctx); err != nil {
		return nil, err
	}
	if s.Storage == nil {
		return nil, status.Error(codes.FailedPrecondition, "storage repo not configured")
	}
	s.compact.mu.Lock()
	run := s.compact.cur
	if run == nil {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			s.compact.mu.Unlock()
			return nil, status.Errorf(codes.Internal, "run id: %v", err)
		}
		run = &compactRun{id: hex.EncodeToString(b[:]), done: make(chan struct{})}
		s.compact.cur = run
		go s.runCompact(run)
	}
	s.compact.mu.Unlock()

	select {
	case <-run.done:
		return run.res, run.err
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	}
}

// trimLeftoverEvents trims each session to its newest event while holding the
// session's turn gate. A session whose gate is taken has a turn running (or
// starting); it is skipped, and that turn trims its own events when it ends.
func (s *LlmServer) trimLeftoverEvents(ctx context.Context) (int64, error) {
	if s.SessionEvents == nil {
		return 0, status.Error(codes.FailedPrecondition, "session event repo not configured")
	}
	refs, err := s.Storage.SessionsWithLeftoverEvents(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, ref := range refs {
		release, ok := s.sessionTurnGate().tryAcquire(ref.SessionID)
		if !ok {
			continue
		}
		n, terr := s.SessionEvents.TrimToNewest(ctx, ref.Scope, ref.SessionID)
		release()
		if terr != nil {
			return total, terr
		}
		total += n
	}
	return total, nil
}

func (s *LlmServer) runCompact(run *compactRun) {
	defer func() {
		s.compact.mu.Lock()
		s.compact.cur = nil
		s.compact.mu.Unlock()
		close(run.done)
	}()
	ctx := context.Background()
	before, err := s.Storage.DatabaseBytes(ctx)
	if err != nil {
		run.err = status.Errorf(codes.Internal, "database size: %v", err)
		return
	}
	removed, err := s.trimLeftoverEvents(ctx)
	if err != nil {
		run.err = status.Errorf(codes.Internal, "trim events: %v", err)
		return
	}
	if err := s.Storage.Vacuum(ctx); err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "disk is full") || strings.Contains(msg, "no space") {
			run.err = status.Errorf(codes.ResourceExhausted, "not enough free disk space to VACUUM (needs about the size of the database): %v", err)
		} else {
			run.err = status.Errorf(codes.Internal, "vacuum: %v", err)
		}
		llmdlog.Stdout("server", llmdlog.LevelWarn, "compact: %v", run.err)
		return
	}
	after, err := s.Storage.DatabaseBytes(ctx)
	if err != nil {
		run.err = status.Errorf(codes.Internal, "database size after: %v", err)
		return
	}
	run.res = &llmv1.CompactResponse{RunId: run.id, BytesBefore: before, BytesAfter: after, EventsRemoved: removed}
}
