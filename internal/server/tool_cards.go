package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/toolseam"
)

var _ toolseam.CardSink = (*LlmServer)(nil)

// ShowCards puts the cards a tool returned (toolseam.Result.Cards, or an
// executor's ToolCallOutput.cards) into the calling session, anchored to the
// tool call that produced them, and announces each to every window — the same
// write UpsertSessionAttachment does, without the tool needing a client. The
// anchor is the tool call id: the store falls back to the session's latest
// assistant turn when that message is not stored yet, which is the case for
// a card created during the call itself (as for ask_user's cards).
func (s *LlmServer) ShowCards(ctx context.Context, inv toolseam.Invocation, cards []toolseam.Card) error {
	if s.Attachments == nil {
		return errors.New("attachment store not configured")
	}
	sid := strings.TrimSpace(inv.SessionID)
	callID := strings.TrimSpace(inv.ToolCallID)
	if sid == "" || callID == "" {
		return errors.New("tool call has no session or call id, so its cards have nowhere to appear")
	}
	sc := inv.Scope
	if strings.TrimSpace(sc.TenantID) == "" {
		var err error
		if sc, err = s.scope(ctx); err != nil {
			return fmt.Errorf("resolve scope for cards: %w", err)
		}
	}
	for _, c := range cards {
		if err := s.Attachments.Upsert(ctx, sc, sid, callID, store.AttachmentInput{
			ID:              c.ID,
			Kind:            c.Kind,
			PayloadJSON:     c.PayloadJSON,
			AfterToolCallID: callID,
			Content:         c.Content,
		}); err != nil {
			return fmt.Errorf("store card %q: %w", c.ID, err)
		}
		frame := &llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_AttachmentUpserted{
				AttachmentUpserted: &llmv1.EvAttachmentUpserted{
					SessionId:       sid,
					AttachmentId:    c.ID,
					Kind:            c.Kind,
					MessageId:       callID,
					AfterToolCallId: callID,
					PayloadJson:     c.PayloadJSON,
				},
			},
		}
		// A live dispatch (ChatStream tool loop, MCP) has a frame emitter that
		// streams, fans out and persists in one go; elsewhere announce it.
		// Never both: that would duplicate the event.
		if live := toolexec.FrameEmitterFrom(ctx); live != nil {
			if err := live(frame); err != nil {
				llmdlog.Stdout("server", llmdlog.LevelWarn,
					"card stored but live announce failed: %s session=%q card=%q: %v", sc.String(), sid, c.ID, err)
			}
			continue
		}
		s.announceAttachmentFrame(ctx, sc, sid, frame)
	}
	return nil
}
