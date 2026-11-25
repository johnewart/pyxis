package server

import (
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/llm/common/suffixwire"
	"google.golang.org/protobuf/proto"
)

// chatStreamWireNormalizer makes the ChatStream wire safe for naive consumers:
//   - Re-applies suffix-only dedupe for assistant + thinking deltas at the server edge.
//   - When canonicalHistory is true (session history is persisted for this stream), it can also
//     strip redundant assistant/thinking bodies from stream_complete after emitting any missing
//     suffix deltas needed to reconcile streamed text with the final AssistantReply.
//
// Persistence must still use the original AssistantReply from the provider; this type only shapes
// what is sent on the wire + hub dispatch.
type chatStreamWireNormalizer struct {
	canonicalHistory bool
	schemaVersion    int32
	turnID           string
	visibleEmitted   string
	thinkingEmitted  string
}

func newChatStreamWireNormalizer(canonicalHistory bool) *chatStreamWireNormalizer {
	return &chatStreamWireNormalizer{
		canonicalHistory: canonicalHistory,
		schemaVersion:    conversationevents.SchemaVersion,
	}
}

// Next returns the frames that should be transmitted on the wire for this backend frame.
// It returns (nil, nil) when the frame should be suppressed entirely (duplicate no-op delta).
func (n *chatStreamWireNormalizer) Next(f *llmv1.ConversationStreamFrame) ([]*llmv1.ConversationStreamFrame, error) {
	if f == nil {
		return nil, nil
	}
	if n.schemaVersion == 0 {
		n.schemaVersion = conversationevents.SchemaVersion
	}

	switch fr := f.Frame.(type) {
	case *llmv1.ConversationStreamFrame_AssistantTurnStarted:
		if ts := fr.AssistantTurnStarted; ts != nil {
			n.turnID = strings.TrimSpace(ts.GetTurnId())
			n.visibleEmitted = ""
			n.thinkingEmitted = ""
		}
		return []*llmv1.ConversationStreamFrame{f}, nil

	case *llmv1.ConversationStreamFrame_AssistantTextDelta:
		td := fr.AssistantTextDelta
		if td == nil {
			return []*llmv1.ConversationStreamFrame{f}, nil
		}
		d := td.GetDelta()
		if d == "" {
			return nil, nil
		}
		ne, wire, div, _ := suffixwire.Compute(n.visibleEmitted, d)
		if div {
			// Provider chunk doesn't line up with what we already committed on the wire; forward
			// the original chunk without updating local suffix state (rare; avoids dropping text).
			return []*llmv1.ConversationStreamFrame{f}, nil
		}
		if wire == "" {
			return nil, nil
		}
		n.visibleEmitted = ne
		out := proto.Clone(f).(*llmv1.ConversationStreamFrame)
		out.GetAssistantTextDelta().Delta = wire
		return []*llmv1.ConversationStreamFrame{out}, nil

	case *llmv1.ConversationStreamFrame_AssistantThinkingDelta:
		td := fr.AssistantThinkingDelta
		if td == nil {
			return []*llmv1.ConversationStreamFrame{f}, nil
		}
		d := td.GetDelta()
		if d == "" {
			return nil, nil
		}
		ne, wire, div, _ := suffixwire.Compute(n.thinkingEmitted, d)
		if div {
			return []*llmv1.ConversationStreamFrame{f}, nil
		}
		if wire == "" {
			return nil, nil
		}
		n.thinkingEmitted = ne
		out := proto.Clone(f).(*llmv1.ConversationStreamFrame)
		out.GetAssistantThinkingDelta().Delta = wire
		return []*llmv1.ConversationStreamFrame{out}, nil

	case *llmv1.ConversationStreamFrame_StreamComplete:
		if !n.canonicalHistory {
			return []*llmv1.ConversationStreamFrame{f}, nil
		}
		r := fr.StreamComplete
		if r == nil {
			return []*llmv1.ConversationStreamFrame{f}, nil
		}

		outFrames := make([]*llmv1.ConversationStreamFrame, 0, 3)

		content := r.GetContent()
		if content != "" && n.turnID != "" && strings.HasPrefix(content, n.visibleEmitted) && len(content) > len(n.visibleEmitted) {
			miss := content[len(n.visibleEmitted):]
			if strings.TrimSpace(miss) != "" {
				outFrames = append(outFrames, assistantTextDeltaFrame(n.schemaVersion, n.turnID, miss))
				n.visibleEmitted = content
			}
		}

		think := r.GetThinkingContent()
		if think != "" && n.turnID != "" && strings.HasPrefix(think, n.thinkingEmitted) && len(think) > len(n.thinkingEmitted) {
			miss := think[len(n.thinkingEmitted):]
			if strings.TrimSpace(miss) != "" {
				outFrames = append(outFrames, assistantThinkingDeltaFrame(n.schemaVersion, n.turnID, miss))
				n.thinkingEmitted = think
			}
		}

		out := proto.Clone(f).(*llmv1.ConversationStreamFrame)
		rr := out.GetStreamComplete()
		if rr == nil {
			return []*llmv1.ConversationStreamFrame{f}, nil
		}
		if rr.GetContent() != "" && rr.GetContent() == n.visibleEmitted {
			rr.Content = ""
		}
		if rr.GetThinkingContent() != "" && rr.GetThinkingContent() == n.thinkingEmitted {
			rr.ThinkingContent = ""
		}

		outFrames = append(outFrames, out)
		return outFrames, nil

	default:
		return []*llmv1.ConversationStreamFrame{f}, nil
	}
}

func assistantTextDeltaFrame(schemaVersion int32, turnID, delta string) *llmv1.ConversationStreamFrame {
	return &llmv1.ConversationStreamFrame{
		SchemaVersion: schemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
			AssistantTextDelta: &llmv1.EvAssistantTextDelta{
				TurnId: turnID,
				Delta:  delta,
			},
		},
	}
}

func assistantThinkingDeltaFrame(schemaVersion int32, turnID, delta string) *llmv1.ConversationStreamFrame {
	return &llmv1.ConversationStreamFrame{
		SchemaVersion: schemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantThinkingDelta{
			AssistantThinkingDelta: &llmv1.EvAssistantThinkingDelta{
				TurnId: turnID,
				Delta:  delta,
			},
		},
	}
}
