package server

import (
	"context"
	"io"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	conversationevents "github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/store"
)

// markupBackend replies with fixed text on round 1 (no structured tool calls)
// and a plain "final" reply afterwards.
type markupBackend struct {
	stubBackend
	text   string
	rounds int
}

func (b *markupBackend) ChatStream(_ context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	b.rounds++
	const turnID = "turn_markup"
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID}},
	}); err != nil {
		return err
	}
	content := "final"
	if b.rounds == 1 {
		content = b.text
	}
	reply := &llmv1.AssistantReply{Id: "asst_markup", Content: content, FinishReason: "end_turn"}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{TurnId: turnID}},
	}); err != nil {
		return err
	}
	return emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_StreamComplete{StreamComplete: reply},
	})
}

func runMarkupTurn(t *testing.T, text string, mode llmv1.TextToolCallMode) (*fakeToolProvider, *markupBackend, []store.ChatMessageRow) {
	t.Helper()
	fp := &fakeToolProvider{result: "provider-result"}
	be := &markupBackend{text: text}
	client, ctx, db := grpcDispatchHarnessWith(t, fp, be)
	st, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
		Messages:  []*llmv1.Message{{Role: "user", Content: "go", Id: "u1"}},
		SessionId: strPtr("agent_dispatch"),
		Options:   &llmv1.CallOptions{TextToolCallMode: mode},
	})
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := st.Recv(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	var rows []store.ChatMessageRow
	if err := db.Where("session_id = ? AND kind = ?", "agent_dispatch", store.ChatMsgKindAssistant).Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("query assistant rows: %v", err)
	}
	return fp, be, rows
}

const echoMarkup = `<tool_call>{"name":"delegated_echo","arguments":{"x":1}}</tool_call>`

func TestChatStream_TextToolCallMarkup(t *testing.T) {
	t.Run("StripDefault", func(t *testing.T) {
		fp, be, rows := runMarkupTurn(t, "hello "+echoMarkup+" bye", llmv1.TextToolCallMode_TEXT_TOOL_CALL_MODE_UNSPECIFIED)
		if n := len(fp.invocations()); n != 0 {
			t.Fatalf("strip must not dispatch, got %d invocations", n)
		}
		if be.rounds != 1 {
			t.Fatalf("rounds = %d, want 1", be.rounds)
		}
		for _, r := range rows {
			if strings.Contains(r.Content, "tool_call") {
				t.Fatalf("persisted content still has markup: %q", r.Content)
			}
		}
		if len(rows) == 0 || rows[len(rows)-1].Content != "hello  bye" {
			t.Fatalf("persisted assistant rows = %+v", rows)
		}
	})

	t.Run("Convert", func(t *testing.T) {
		fp, be, _ := runMarkupTurn(t, "hello "+echoMarkup, llmv1.TextToolCallMode_TEXT_TOOL_CALL_MODE_CONVERT)
		invs := fp.invocations()
		if len(invs) != 1 || invs[0].GetToolId() != "delegated_echo" {
			t.Fatalf("want one delegated_echo invocation, got %+v", invs)
		}
		if be.rounds < 2 {
			t.Fatalf("expected follow-up round, rounds=%d", be.rounds)
		}
	})

	t.Run("UnknownNameUntouched", func(t *testing.T) {
		text := `<tool_call>{"name":"not_offered","arguments":{}}</tool_call>`
		fp, be, rows := runMarkupTurn(t, text, llmv1.TextToolCallMode_TEXT_TOOL_CALL_MODE_CONVERT)
		if n := len(fp.invocations()); n != 0 {
			t.Fatalf("unknown tool must not dispatch, got %d", n)
		}
		if be.rounds != 1 {
			t.Fatalf("rounds = %d, want 1", be.rounds)
		}
		if len(rows) == 0 || rows[len(rows)-1].Content != text {
			t.Fatalf("content must be unchanged, rows = %+v", rows)
		}
	})
}
