package server

import (
	"context"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/protobuf/encoding/protojson"
)

// clearTurnStatus ends the current turn's set_status headline: it clears the
// stored value (what GetSession seeds a reconnecting client from) and tells
// every listener with an empty EvSessionStatus, logged for replay and
// dispatched live through the session hub. ChatStream calls it when a user
// turn starts and when it ends, so a headline never outlives its turn.
//
// Not sent on the turn's own response stream: at turn end that stream may be
// gone, and every client already follows the session through the hub/log.
func (s *LlmServer) clearTurnStatus(ctx context.Context, sc serverutil.Scope, sid string, dispatch func(*llmv1.ConversationStreamFrame)) {
	if err := s.SessionCache.SetTurnStatus(ctx, sc, sid, ""); err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "clear turn status failed: session=%q err=%v", sid, err)
	}
	frame := &llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_SessionStatus{
			SessionStatus: &llmv1.EvSessionStatus{SessionId: sid, Headline: ""},
		},
	}
	if s.SessionEvents != nil {
		if eventJSON, err := protojson.Marshal(frame); err != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "clear turn status: marshal event failed: session=%q err=%v", sid, err)
		} else if _, err := s.SessionEvents.AppendEvent(ctx, sc, sid, string(eventJSON)); err != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "clear turn status: append event failed: session=%q err=%v", sid, err)
		}
	}
	if dispatch != nil {
		dispatch(frame)
	}
}
