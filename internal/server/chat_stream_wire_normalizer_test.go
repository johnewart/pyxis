package server

import (
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"google.golang.org/protobuf/proto"
)

func TestChatStreamWireNormalizer_duplicateAssistantDeltasSuppressed(t *testing.T) {
	t.Parallel()
	n := newChatStreamWireNormalizer(false)
	turn := &llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: "t1"},
		},
	}
	if _, err := drain(n, turn); err != nil {
		t.Fatal(err)
	}

	out1, err := n.Next(textDelta("t1", "hel"))
	if err != nil {
		t.Fatal(err)
	}
	assertWireDeltas(t, out1, "hel")

	// Cumulative snapshot repeats prefix; only suffix should wire.
	out2, err := n.Next(textDelta("t1", "hello"))
	if err != nil {
		t.Fatal(err)
	}
	assertWireDeltas(t, out2, "lo")

	// Exact duplicate of cumulative snapshot => suppress.
	out3, err := n.Next(textDelta("t1", "hello"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out3) != 0 {
		t.Fatalf("expected duplicate cumulative snapshot to be suppressed; got %d frames", len(out3))
	}
}

func TestChatStreamWireNormalizer_streamCompleteStripsRedundantBodiesWhenCanonical(t *testing.T) {
	t.Parallel()
	n := newChatStreamWireNormalizer(true)
	if _, err := drain(n, turnStarted("t1")); err != nil {
		t.Fatal(err)
	}
	if _, err := drain(n, textDelta("t1", "hi")); err != nil {
		t.Fatal(err)
	}
	if _, err := drain(n, thinkingDelta("t1", "think")); err != nil {
		t.Fatal(err)
	}

	outs, err := n.Next(streamComplete("t1", "hi", "think"))
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 1 {
		t.Fatalf("expected 1 frame (stream_complete only), got %d", len(outs))
	}
	sc := outs[0].GetStreamComplete()
	if sc == nil {
		t.Fatal("expected stream_complete")
	}
	if sc.GetContent() != "" {
		t.Fatalf("expected stripped content, got %q", sc.GetContent())
	}
	if sc.GetThinkingContent() != "" {
		t.Fatalf("expected stripped thinking, got %q", sc.GetThinkingContent())
	}
}

func TestChatStreamWireNormalizer_streamCompleteEmitsMissingSuffixThenStrips(t *testing.T) {
	t.Parallel()
	n := newChatStreamWireNormalizer(true)
	if _, err := drain(n, turnStarted("t1")); err != nil {
		t.Fatal(err)
	}
	if _, err := drain(n, textDelta("t1", "hel")); err != nil {
		t.Fatal(err)
	}

	outs, err := n.Next(streamComplete("t1", "hello", ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 2 {
		t.Fatalf("expected compensation delta + stream_complete, got %d frames", len(outs))
	}
	if d := outs[0].GetAssistantTextDelta(); d == nil || d.GetDelta() != "lo" {
		t.Fatalf("expected compensation delta lo, got %#v", d)
	}
	sc := outs[1].GetStreamComplete()
	if sc == nil || sc.GetContent() != "" {
		t.Fatalf("expected stripped stream_complete, got %#v", sc)
	}
}

func TestChatStreamWireNormalizer_nonCanonicalLeavesStreamCompleteIntact(t *testing.T) {
	t.Parallel()
	n := newChatStreamWireNormalizer(false)
	if _, err := drain(n, turnStarted("t1")); err != nil {
		t.Fatal(err)
	}
	if _, err := drain(n, textDelta("t1", "hi")); err != nil {
		t.Fatal(err)
	}
	outs, err := n.Next(streamComplete("t1", "hi", ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(outs))
	}
	if got := outs[0].GetStreamComplete().GetContent(); got != "hi" {
		t.Fatalf("expected content preserved, got %q", got)
	}
}

func TestChatStreamWireNormalizer_thinkingChannelDeduped(t *testing.T) {
	t.Parallel()
	n := newChatStreamWireNormalizer(false)
	if _, err := drain(n, turnStarted("t1")); err != nil {
		t.Fatal(err)
	}
	out1, err := n.Next(thinkingDelta("t1", "a"))
	if err != nil {
		t.Fatal(err)
	}
	assertWireThinking(t, out1, "a")

	out2, err := n.Next(thinkingDelta("t1", "ab"))
	if err != nil {
		t.Fatal(err)
	}
	assertWireThinking(t, out2, "b")

	out3, err := n.Next(thinkingDelta("t1", "ab"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out3) != 0 {
		t.Fatalf("expected duplicate thinking suppressed, got %d", len(out3))
	}
}

func TestChatStreamWireNormalizer_reconstructFullAssistantFromWire(t *testing.T) {
	t.Parallel()
	n := newChatStreamWireNormalizer(true)
	if _, err := drain(n, turnStarted("t1")); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	seq := []*llmv1.ConversationStreamFrame{
		textDelta("t1", "The"),
		textDelta("t1", "The quick"),
		textDelta("t1", "The quick brown"),
		streamComplete("t1", "The quick brown fox", ""),
	}
	for _, fr := range seq {
		outs, err := n.Next(fr)
		if err != nil {
			t.Fatal(err)
		}
		for _, out := range outs {
			if td := out.GetAssistantTextDelta(); td != nil {
				b.WriteString(td.GetDelta())
			}
			if sc := out.GetStreamComplete(); sc != nil {
				if sc.GetContent() != "" {
					t.Fatalf("expected stripped stream_complete content, got %q", sc.GetContent())
				}
			}
		}
	}
	if got := b.String(); got != "The quick brown fox" {
		t.Fatalf("unexpected reconstructed assistant from wire: %q", got)
	}
}

func drain(n *chatStreamWireNormalizer, frames ...*llmv1.ConversationStreamFrame) ([]*llmv1.ConversationStreamFrame, error) {
	var all []*llmv1.ConversationStreamFrame
	for _, f := range frames {
		outs, err := n.Next(f)
		if err != nil {
			return nil, err
		}
		all = append(all, outs...)
	}
	return all, nil
}

func turnStarted(turnID string) *llmv1.ConversationStreamFrame {
	return &llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{TurnId: turnID},
		},
	}
}

func textDelta(turnID, delta string) *llmv1.ConversationStreamFrame {
	return &llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
			AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: delta},
		},
	}
}

func thinkingDelta(turnID, delta string) *llmv1.ConversationStreamFrame {
	return &llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantThinkingDelta{
			AssistantThinkingDelta: &llmv1.EvAssistantThinkingDelta{TurnId: turnID, Delta: delta},
		},
	}
}

func streamComplete(turnID, content, thinking string) *llmv1.ConversationStreamFrame {
	// turnID unused but kept for symmetry with other helpers / future extensions.
	_ = turnID
	return &llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_StreamComplete{
			StreamComplete: &llmv1.AssistantReply{
				Content:         content,
				ThinkingContent: thinking,
				Id:              "msg-1",
				FinishReason:    "stop",
			},
		},
	}
}

func assertWireDeltas(t *testing.T, outs []*llmv1.ConversationStreamFrame, want string) {
	t.Helper()
	var got strings.Builder
	for _, o := range outs {
		if td := o.GetAssistantTextDelta(); td != nil {
			got.WriteString(td.GetDelta())
		}
	}
	if got.String() != want {
		t.Fatalf("expected wire deltas %q, got %q (frames=%d)", want, got.String(), len(outs))
	}
}

func assertWireThinking(t *testing.T, outs []*llmv1.ConversationStreamFrame, want string) {
	t.Helper()
	var got strings.Builder
	for _, o := range outs {
		if td := o.GetAssistantThinkingDelta(); td != nil {
			got.WriteString(td.GetDelta())
		}
	}
	if got.String() != want {
		t.Fatalf("expected wire thinking %q, got %q", want, got.String())
	}
}

func TestChatStreamWireNormalizer_protoCloneDoesNotAliasNestedPointers(t *testing.T) {
	t.Parallel()
	n := newChatStreamWireNormalizer(true)
	if _, err := drain(n, turnStarted("t1"), textDelta("t1", "hi")); err != nil {
		t.Fatal(err)
	}
	in := streamComplete("t1", "hi", "")
	outs, err := n.Next(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 1 {
		t.Fatalf("expected 1 out, got %d", len(outs))
	}
	outs[0].GetStreamComplete().Content = "mutated"
	if in.GetStreamComplete().GetContent() != "hi" {
		t.Fatalf("expected original proto not aliased by clone output; got %q", in.GetStreamComplete().GetContent())
	}
}

func TestChatStreamWireNormalizer_streamCompleteCloneIndependent(t *testing.T) {
	t.Parallel()
	n := newChatStreamWireNormalizer(true)
	if _, err := drain(n, turnStarted("t1"), textDelta("t1", "hi"), thinkingDelta("t1", "think")); err != nil {
		t.Fatal(err)
	}
	in := streamComplete("t1", "hi", "think")
	outs, err := n.Next(proto.Clone(in).(*llmv1.ConversationStreamFrame))
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 1 {
		t.Fatalf("expected 1 out, got %d", len(outs))
	}
	outs[0].GetStreamComplete().ThinkingContent = "nope"
	if in.GetStreamComplete().GetThinkingContent() != "think" {
		t.Fatalf("clone should not alias nested reply: got %q", in.GetStreamComplete().GetThinkingContent())
	}
}
