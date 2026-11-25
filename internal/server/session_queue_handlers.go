package server

import (
	"context"
	"errors"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

// Per-session composer drafts and the message queue.
//
// llmd owns this state and drains the queue itself when a session goes idle
// (see queue_drainer.go), except on sessions with client-owned tools, which are
// left to client drainers. Several drainers can be alive at once; the
// guarantees that make that safe live in store.SessionQueueRepo.

// queueSession resolves and validates the session a queue/draft call names,
// returning the workspace it belongs to.
func (s *LlmServer) queueSession(ctx context.Context, sessionID string) (sc serverutil.Scope, sid string, err error) {
	sid = strings.TrimSpace(sessionID)
	if sid == "" {
		return serverutil.Scope{}, "", status.Error(codes.InvalidArgument, "session_id is required")
	}
	if err := s.ensureKnownSession(ctx, sid); err != nil {
		return serverutil.Scope{}, "", err
	}
	sc, err = s.scope(ctx)
	if err != nil {
		return serverutil.Scope{}, "", err
	}
	return sc, sid, nil
}

func (s *LlmServer) UpsertSessionDraft(ctx context.Context, req *llmv1.UpsertSessionDraftRequest) (*llmv1.UpsertSessionDraftResponse, error) {
	if s.SessionDrafts == nil {
		return nil, status.Error(codes.FailedPrecondition, "session draft persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	if err := s.SessionDrafts.Upsert(ctx, sc, sid, req.GetContent()); err != nil {
		return nil, status.Errorf(codes.Internal, "upsert draft: %v", err)
	}
	return &llmv1.UpsertSessionDraftResponse{}, nil
}

func (s *LlmServer) GetSessionDraft(ctx context.Context, req *llmv1.GetSessionDraftRequest) (*llmv1.GetSessionDraftResponse, error) {
	if s.SessionDrafts == nil {
		return nil, status.Error(codes.FailedPrecondition, "session draft persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	content, err := s.SessionDrafts.Get(ctx, sc, sid)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get draft: %v", err)
	}
	return &llmv1.GetSessionDraftResponse{Content: content}, nil
}

func (s *LlmServer) ClearSessionDraft(ctx context.Context, req *llmv1.ClearSessionDraftRequest) (*llmv1.ClearSessionDraftResponse, error) {
	if s.SessionDrafts == nil {
		return nil, status.Error(codes.FailedPrecondition, "session draft persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	if err := s.SessionDrafts.Clear(ctx, sc, sid); err != nil {
		return nil, status.Errorf(codes.Internal, "clear draft: %v", err)
	}
	return &llmv1.ClearSessionDraftResponse{}, nil
}

func (s *LlmServer) EnqueueMessage(ctx context.Context, req *llmv1.EnqueueMessageRequest) (*llmv1.EnqueueMessageResponse, error) {
	if s.SessionQueue == nil {
		return nil, status.Error(codes.FailedPrecondition, "session queue persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetContent()) == "" && len(req.GetAttachments()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "content or attachments is required")
	}
	atts, err := queuedAttachmentsFromProto(req.GetAttachments())
	if err != nil {
		return nil, err
	}
	opts := store.QueuedTurnOptions{DisableTools: req.GetTurnOptions().GetDisableTools()}
	row, err := s.SessionQueue.EnqueueWithAttachments(ctx, sc, sid, req.GetContent(), atts, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "enqueue: %v", err)
	}
	s.announceQueueChanged(ctx, sc, sid)
	s.kickQueueDrain(sc, sid)
	return &llmv1.EnqueueMessageResponse{Entry: queuedMessageToProto(*row)}, nil
}

func (s *LlmServer) ListQueue(ctx context.Context, req *llmv1.ListQueueRequest) (*llmv1.ListQueueResponse, error) {
	if s.SessionQueue == nil {
		return nil, status.Error(codes.FailedPrecondition, "session queue persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	queue, paused, err := s.queueSnapshot(ctx, sc, sid)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list queue: %v", err)
	}
	return &llmv1.ListQueueResponse{Queue: queue, Paused: paused}, nil
}

func (s *LlmServer) RemoveQueuedMessage(ctx context.Context, req *llmv1.RemoveQueuedMessageRequest) (*llmv1.RemoveQueuedMessageResponse, error) {
	if s.SessionQueue == nil {
		return nil, status.Error(codes.FailedPrecondition, "session queue persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	removed, err := s.SessionQueue.Remove(ctx, sc, sid, req.GetEntryId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "remove queued message: %v", err)
	}
	if removed {
		s.announceQueueChanged(ctx, sc, sid)
	}
	return &llmv1.RemoveQueuedMessageResponse{Removed: removed}, nil
}

func (s *LlmServer) ReorderQueue(ctx context.Context, req *llmv1.ReorderQueueRequest) (*llmv1.ReorderQueueResponse, error) {
	if s.SessionQueue == nil {
		return nil, status.Error(codes.FailedPrecondition, "session queue persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	if err := s.SessionQueue.Reorder(ctx, sc, sid, req.GetEntryIds()); err != nil {
		return nil, status.Errorf(codes.Internal, "reorder queue: %v", err)
	}
	s.announceQueueChanged(ctx, sc, sid)
	queue, _, err := s.queueSnapshot(ctx, sc, sid)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reorder queue: %v", err)
	}
	return &llmv1.ReorderQueueResponse{Queue: queue}, nil
}

func (s *LlmServer) SetQueuePaused(ctx context.Context, req *llmv1.SetQueuePausedRequest) (*llmv1.SetQueuePausedResponse, error) {
	if s.SessionQueue == nil {
		return nil, status.Error(codes.FailedPrecondition, "session queue persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	if err := s.SessionQueue.SetPaused(ctx, sc, sid, req.GetPaused()); err != nil {
		return nil, status.Errorf(codes.Internal, "set queue paused: %v", err)
	}
	s.announceQueueChanged(ctx, sc, sid)
	if !req.GetPaused() {
		s.kickQueueDrain(sc, sid)
	}
	return &llmv1.SetQueuePausedResponse{}, nil
}

func (s *LlmServer) ClaimNextQueuedMessage(ctx context.Context, req *llmv1.ClaimNextQueuedMessageRequest) (*llmv1.ClaimNextQueuedMessageResponse, error) {
	if s.SessionQueue == nil {
		return nil, status.Error(codes.FailedPrecondition, "session queue persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetOwner()) == "" {
		return nil, status.Error(codes.InvalidArgument, "owner is required")
	}
	row, err := s.SessionQueue.ClaimNext(ctx, sc, sid, req.GetOwner())
	if errors.Is(err, store.ErrQueuePaused) {
		// Not an error condition: a paused queue is a deliberate state, and the
		// caller needs to tell it apart from "drained" so its loop keeps polling
		// instead of exiting.
		return &llmv1.ClaimNextQueuedMessageResponse{Paused: true}, nil
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "claim next: %v", err)
	}
	if row == nil {
		return &llmv1.ClaimNextQueuedMessageResponse{}, nil
	}
	s.announceQueueChanged(ctx, sc, sid)
	return &llmv1.ClaimNextQueuedMessageResponse{Entry: queuedMessageToProto(*row)}, nil
}

func (s *LlmServer) HeartbeatQueuedMessage(ctx context.Context, req *llmv1.HeartbeatQueuedMessageRequest) (*llmv1.HeartbeatQueuedMessageResponse, error) {
	if s.SessionQueue == nil {
		return nil, status.Error(codes.FailedPrecondition, "session queue persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	err = s.SessionQueue.Heartbeat(ctx, sc, sid, req.GetEntryId(), req.GetOwner())
	if err != nil {
		return nil, queueOwnershipError(err, "heartbeat")
	}
	return &llmv1.HeartbeatQueuedMessageResponse{}, nil
}

func (s *LlmServer) MarkQueuedMessageStarted(ctx context.Context, req *llmv1.MarkQueuedMessageStartedRequest) (*llmv1.MarkQueuedMessageStartedResponse, error) {
	if s.SessionQueue == nil {
		return nil, status.Error(codes.FailedPrecondition, "session queue persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetTurnId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "turn_id is required")
	}
	err = s.SessionQueue.MarkStarted(ctx, sc, sid, req.GetEntryId(), req.GetOwner(), req.GetTurnId())
	if err != nil {
		return nil, queueOwnershipError(err, "mark started")
	}
	return &llmv1.MarkQueuedMessageStartedResponse{}, nil
}

func (s *LlmServer) RetireQueuedMessage(ctx context.Context, req *llmv1.RetireQueuedMessageRequest) (*llmv1.RetireQueuedMessageResponse, error) {
	if s.SessionQueue == nil {
		return nil, status.Error(codes.FailedPrecondition, "session queue persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	err = s.SessionQueue.Retire(ctx, sc, sid, req.GetEntryId(), req.GetOwner())
	if err != nil {
		return nil, queueOwnershipError(err, "retire")
	}
	s.announceQueueChanged(ctx, sc, sid)
	return &llmv1.RetireQueuedMessageResponse{}, nil
}

func (s *LlmServer) ReleaseQueuedMessage(ctx context.Context, req *llmv1.ReleaseQueuedMessageRequest) (*llmv1.ReleaseQueuedMessageResponse, error) {
	if s.SessionQueue == nil {
		return nil, status.Error(codes.FailedPrecondition, "session queue persistence not configured")
	}
	sc, sid, err := s.queueSession(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	err = s.SessionQueue.Release(ctx, sc, sid, req.GetEntryId(), req.GetOwner())
	if err != nil {
		return nil, queueOwnershipError(err, "release")
	}
	s.announceQueueChanged(ctx, sc, sid)
	return &llmv1.ReleaseQueuedMessageResponse{}, nil
}

// queueOwnershipError maps a lost claim onto FailedPrecondition rather than
// Internal: a drainer whose lease lapsed has not hit a bug, it has been
// superseded, and it needs to stop working on that entry rather than retry.
func queueOwnershipError(err error, op string) error {
	if errors.Is(err, store.ErrQueueEntryNotClaimed) {
		return status.Errorf(codes.FailedPrecondition, "%s: entry is no longer claimed by this owner", op)
	}
	return status.Errorf(codes.Internal, "%s: %v", op, err)
}

func (s *LlmServer) queueSnapshot(ctx context.Context, sc serverutil.Scope, sessionID string) ([]*llmv1.QueuedMessage, bool, error) {
	rows, err := s.SessionQueue.List(ctx, sc, sessionID)
	if err != nil {
		return nil, false, err
	}
	paused, err := s.SessionQueue.Paused(ctx, sc, sessionID)
	if err != nil {
		return nil, false, err
	}
	out := make([]*llmv1.QueuedMessage, 0, len(rows))
	for _, row := range rows {
		out = append(out, queuedMessageToProto(row))
	}
	return out, paused, nil
}

func queuedMessageToProto(row store.SessionQueuedMessageRow) *llmv1.QueuedMessage {
	msg := &llmv1.QueuedMessage{
		Id:            row.ID,
		Content:       row.Content,
		State:         row.State,
		ClaimOwner:    row.ClaimOwner,
		CreatedAtUnix: row.CreatedAt.Unix(),
	}
	if row.ClaimExpiresAt != nil {
		expires := row.ClaimExpiresAt.Unix()
		msg.ClaimExpiresAtUnix = &expires
	}
	atts, err := row.Attachments()
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "queue: entry=%q has undecodable attachments: %v", row.ID, err)
	}
	msg.Attachments = queuedAttachmentsToProto(atts)
	if row.DisableTools {
		msg.TurnOptions = &llmv1.TurnOptions{DisableTools: true}
	}
	return msg
}

// queuedAttachmentsFromProto validates and converts attachments for storage.
func queuedAttachmentsFromProto(in []*llmv1.MessageAttachment) ([]store.QueuedAttachment, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]store.QueuedAttachment, 0, len(in))
	seen := map[string]bool{}
	for _, a := range in {
		id := strings.TrimSpace(a.GetId())
		if id == "" {
			return nil, status.Error(codes.InvalidArgument, "attachment.id is required")
		}
		if seen[id] {
			return nil, status.Errorf(codes.InvalidArgument, "duplicate attachment id %q", id)
		}
		seen[id] = true
		out = append(out, store.QueuedAttachment{
			ID:              id,
			Kind:            a.GetKind(),
			PayloadJSON:     a.GetPayloadJson(),
			AfterToolCallID: a.GetAfterToolCallId(),
			Content:         a.GetContent(),
		})
	}
	return out, nil
}

func queuedAttachmentsToProto(in []store.QueuedAttachment) []*llmv1.MessageAttachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]*llmv1.MessageAttachment, 0, len(in))
	for _, a := range in {
		pa := &llmv1.MessageAttachment{
			Id:          a.ID,
			Kind:        a.Kind,
			PayloadJson: a.PayloadJSON,
			Content:     a.Content,
		}
		if a.AfterToolCallID != "" {
			tc := a.AfterToolCallID
			pa.AfterToolCallId = &tc
		}
		out = append(out, pa)
	}
	return out
}

// announceQueueChanged publishes the queue to every window on the session.
//
// The queue is shared state that several clients edit — one window reorders,
// another pauses, a background drainer claims — so an edit that only the caller
// learned about would leave the others showing a queue that no longer exists.
// Sends the whole queue rather than a delta so a window that missed an event
// still converges. Best-effort: failing to announce must never fail the edit
// that succeeded.
func (s *LlmServer) announceQueueChanged(ctx context.Context, sc serverutil.Scope, sessionID string) {
	queue, paused, err := s.queueSnapshot(ctx, sc, sessionID)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"queue changed: snapshot failed: %s session=%q: %v", sc.String(), sessionID, err)
		return
	}
	frame := &llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_QueueChanged{
			QueueChanged: &llmv1.EvQueueChanged{
				SessionId: sessionID,
				Queue:     queue,
				Paused:    paused,
			},
		},
	}
	// Persist before fan-out, for the same reason the cancel path does: a
	// listener that reacts by re-reading must not race the write backing it.
	if s.SessionEvents != nil && sc.TenantID != "" {
		eventJSON, merr := protojson.Marshal(frame)
		if merr != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn,
				"queue changed: marshal failed: %s session=%q: %v", sc.String(), sessionID, merr)
		} else if _, aerr := s.SessionEvents.AppendEvent(ctx, sc, sessionID, string(eventJSON)); aerr != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn,
				"queue changed: persist failed: %s session=%q: %v", sc.String(), sessionID, aerr)
		}
	}
	if s.SessionRuntimes != nil {
		if ma, ok := s.runtimeFor(ctx, sessionID); ok && ma != nil && ma.Core != nil {
			ma.Core.Session(sessionID).Dispatch(frame)
		}
	}
}
