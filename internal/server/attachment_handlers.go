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

// announceAttachmentFrame persists an attachment frame and fans it out to
// every window on the session, so an answer submitted in one client (or a
// card created by another) shows up everywhere live. Best-effort: failing to
// announce never fails the write that succeeded, but every failure is logged.
func (s *LlmServer) announceAttachmentFrame(ctx context.Context, sc serverutil.Scope, sessionID string, frame *llmv1.ConversationStreamFrame) {
	frame.SchemaVersion = conversationevents.SchemaVersion
	if s.SessionEvents != nil && sc.TenantID != "" {
		eventJSON, merr := protojson.Marshal(frame)
		if merr != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn,
				"attachment changed: marshal failed: %s session=%q: %v", sc.String(), sessionID, merr)
		} else if _, aerr := s.SessionEvents.AppendEvent(ctx, sc, sessionID, string(eventJSON)); aerr != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn,
				"attachment changed: persist failed: %s session=%q: %v", sc.String(), sessionID, aerr)
		}
	}
	if s.SessionRuntimes != nil {
		if ma, ok := s.runtimeFor(ctx, sessionID); ok && ma != nil && ma.Core != nil {
			ma.Core.Session(sessionID).Dispatch(frame)
		}
	}
}

// persistIncomingAttachments stores the attachments carried on a send's user
// messages, anchored to each user message, and announces them to every window.
// Must run AFTER those user messages are in history so the anchor resolves to
// the user's own turn. Best-effort per attachment: a failure is logged and the
// turn continues (the current send already received the content via
// injectTrackedContextForSend; only later sends would miss it).
func (s *LlmServer) persistIncomingAttachments(ctx context.Context, sc serverutil.Scope, sessionID string, incoming []*llmv1.Message) {
	if s.Attachments == nil || sc.TenantID == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	for _, m := range incoming {
		if m.GetRole() != "user" || len(m.GetAttachments()) == 0 {
			continue
		}
		msgID := strings.TrimSpace(m.GetId())
		if msgID == "" {
			llmdlog.Stdout("server", llmdlog.LevelWarn,
				"incoming attachments dropped: user message has no id: %s session=%q count=%d", sc.String(), sessionID, len(m.GetAttachments()))
			continue
		}
		for _, att := range m.GetAttachments() {
			attID := strings.TrimSpace(att.GetId())
			if attID == "" {
				llmdlog.Stdout("server", llmdlog.LevelWarn,
					"incoming attachment dropped: no id: %s session=%q message=%q kind=%q", sc.String(), sessionID, msgID, att.GetKind())
				continue
			}
			if err := s.Attachments.Upsert(ctx, sc, sessionID, msgID, store.AttachmentInput{
				ID:              attID,
				Kind:            att.GetKind(),
				PayloadJSON:     att.GetPayloadJson(),
				AfterToolCallID: att.GetAfterToolCallId(),
				Content:         att.GetContent(),
			}); err != nil {
				llmdlog.Stdout("server", llmdlog.LevelWarn,
					"incoming attachment persist failed: %s session=%q message=%q attachment=%q: %v", sc.String(), sessionID, msgID, attID, err)
				continue
			}
			s.announceAttachmentFrame(ctx, sc, sessionID, &llmv1.ConversationStreamFrame{
				Frame: &llmv1.ConversationStreamFrame_AttachmentUpserted{
					AttachmentUpserted: &llmv1.EvAttachmentUpserted{
						SessionId:       sessionID,
						AttachmentId:    attID,
						Kind:            att.GetKind(),
						MessageId:       msgID,
						AfterToolCallId: att.GetAfterToolCallId(),
						PayloadJson:     att.GetPayloadJson(),
					},
				},
			})
		}
	}
}

// attachmentStoreErr maps repo sentinel errors to gRPC status codes.
func attachmentStoreErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrAttachmentInvalidArgs):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, store.ErrAttachmentAnchorNotFound),
		errors.Is(err, store.ErrAttachmentMessageNotFound):
		return status.Error(codes.NotFound, err.Error())
	default:
		return status.Errorf(codes.Internal, "attachment store: %v", err)
	}
}

// UpsertSessionAttachment persists or replaces one UI-artifact attachment row for a session.
func (s *LlmServer) UpsertSessionAttachment(ctx context.Context, req *llmv1.UpsertSessionAttachmentRequest) (*llmv1.UpsertSessionAttachmentResponse, error) {
	sid := strings.TrimSpace(req.GetSessionId())
	if sid == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	anchor := strings.TrimSpace(req.GetAnchorMessageId())
	if anchor == "" {
		return nil, status.Error(codes.InvalidArgument, "anchor_message_id is required")
	}
	att := req.GetAttachment()
	if att == nil || strings.TrimSpace(att.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "attachment.id is required")
	}
	if s.Attachments == nil {
		return nil, status.Error(codes.FailedPrecondition, "attachment store not configured")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Attachments.Upsert(ctx, sc, sid, anchor, store.AttachmentInput{
		ID:              att.GetId(),
		Kind:            att.GetKind(),
		PayloadJSON:     att.GetPayloadJson(),
		AfterToolCallID: att.GetAfterToolCallId(),
		Content:         att.GetContent(),
	}); err != nil {
		return nil, attachmentStoreErr(err)
	}
	s.announceAttachmentFrame(ctx, sc, sid, &llmv1.ConversationStreamFrame{
		Frame: &llmv1.ConversationStreamFrame_AttachmentUpserted{
			AttachmentUpserted: &llmv1.EvAttachmentUpserted{
				SessionId:       sid,
				AttachmentId:    att.GetId(),
				Kind:            att.GetKind(),
				MessageId:       anchor,
				AfterToolCallId: att.GetAfterToolCallId(),
				PayloadJson:     att.GetPayloadJson(),
			},
		},
	})
	return &llmv1.UpsertSessionAttachmentResponse{}, nil
}

// DeleteSessionAttachment removes one attachment row.
func (s *LlmServer) DeleteSessionAttachment(ctx context.Context, req *llmv1.DeleteSessionAttachmentRequest) (*llmv1.DeleteSessionAttachmentResponse, error) {
	sid := strings.TrimSpace(req.GetSessionId())
	if sid == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	if strings.TrimSpace(req.GetAttachmentId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "attachment_id is required")
	}
	if s.Attachments == nil {
		return nil, status.Error(codes.FailedPrecondition, "attachment store not configured")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Attachments.Delete(ctx, sc, sid, req.GetAttachmentId()); err != nil {
		return nil, attachmentStoreErr(err)
	}
	s.announceAttachmentFrame(ctx, sc, sid, &llmv1.ConversationStreamFrame{
		Frame: &llmv1.ConversationStreamFrame_AttachmentDeleted{
			AttachmentDeleted: &llmv1.EvAttachmentDeleted{
				SessionId:    sid,
				AttachmentId: strings.TrimSpace(req.GetAttachmentId()),
			},
		},
	})
	return &llmv1.DeleteSessionAttachmentResponse{}, nil
}

// PatchAssistantMessageContent splices updated content into a persisted assistant message.
func (s *LlmServer) PatchAssistantMessageContent(ctx context.Context, req *llmv1.PatchAssistantMessageContentRequest) (*llmv1.PatchAssistantMessageContentResponse, error) {
	sid := strings.TrimSpace(req.GetSessionId())
	if sid == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	if strings.TrimSpace(req.GetMessageId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "message_id is required")
	}
	if s.Attachments == nil {
		return nil, status.Error(codes.FailedPrecondition, "attachment store not configured")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Attachments.PatchAssistantContent(ctx, sc, sid, req.GetMessageId(), req.GetContent(), req.GetTurnId()); err != nil {
		return nil, attachmentStoreErr(err)
	}
	return &llmv1.PatchAssistantMessageContentResponse{}, nil
}
