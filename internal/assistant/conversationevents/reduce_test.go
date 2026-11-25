package conversationevents

import (
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/protobuf/proto"
)

func TestReduceConversation_userThenAssistantTurn(t *testing.T) {
	st := InitialConversationState()
	st = ReduceConversation(st, &llmv1.ConversationStreamFrame{
		SchemaVersion: SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_UserTurnCommitted{
			UserTurnCommitted: &llmv1.EvUserTurnCommitted{UserMessageId: "u1"},
		},
	})
	if st.LastCommittedUserMessageID != "u1" || st.ActiveAssistantTurn.Status != TurnIdle {
		t.Fatalf("after user: %+v", st)
	}
	st = ReduceConversation(st, &llmv1.ConversationStreamFrame{
		SchemaVersion: SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: "t1"},
		},
	})
	st = ReduceConversation(st, &llmv1.ConversationStreamFrame{
		SchemaVersion: SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
			AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: "t1", Delta: "hi"},
		},
	})
	if st.ActiveAssistantTurn.Assistant != "hi" || st.ActiveAssistantTurn.Status != TurnActive {
		t.Fatalf("after delta: %+v", st.ActiveAssistantTurn)
	}
	st = ReduceConversation(st, &llmv1.ConversationStreamFrame{
		SchemaVersion: SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{
			AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{
				TurnId:             "t1",
				FinishReason:       proto.String("end_turn"),
				AssistantMessageId: proto.String("a1"),
				Usage: &llmv1.TokenUsage{
					InputTokens:  proto.Int64(3),
					OutputTokens: proto.Int64(4),
				},
			},
		},
	})
	if st.ActiveAssistantTurn.Status != TurnCompleted || st.ActiveAssistantTurn.FinishReason != "end_turn" {
		t.Fatalf("after finish: %+v", st.ActiveAssistantTurn)
	}
}

func TestReduceConversation_activitySnapshot_replacesHeadline(t *testing.T) {
	st := InitialConversationState()
	st = ReduceConversation(st, &llmv1.ConversationStreamFrame{
		SchemaVersion: SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: "t1"},
		},
	})
	st = ReduceConversation(st, &llmv1.ConversationStreamFrame{
		SchemaVersion: SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantActivitySnapshot{
			AssistantActivitySnapshot: &llmv1.EvAssistantActivitySnapshot{TurnId: "t1", Headline: "first"},
		},
	})
	if st.ActiveAssistantTurn.ActivityHeadline != "first" {
		t.Fatalf("headline=%q", st.ActiveAssistantTurn.ActivityHeadline)
	}
	st = ReduceConversation(st, &llmv1.ConversationStreamFrame{
		SchemaVersion: SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantActivitySnapshot{
			AssistantActivitySnapshot: &llmv1.EvAssistantActivitySnapshot{TurnId: "t1", Headline: "second"},
		},
	})
	if st.ActiveAssistantTurn.ActivityHeadline != "second" {
		t.Fatalf("headline=%q", st.ActiveAssistantTurn.ActivityHeadline)
	}
}
