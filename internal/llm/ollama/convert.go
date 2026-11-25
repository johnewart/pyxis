package ollamallm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/memctx"
)

// ollamaChatMessage is the wire shape for Ollama /api/chat messages.
type ollamaChatMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ollamaToolFn `json:"function"`
}

type ollamaToolFn struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments,omitempty"`
}

// ProtoToOllamaMessages converts gRPC messages to Ollama chat messages.
func ProtoToOllamaMessages(messages []*llmv1.Message) ([]ollamaChatMessage, error) {
	var out []ollamaChatMessage
	for _, m := range messages {
		if m == nil {
			continue
		}
		switch m.GetRole() {
		case "system":
			if strings.TrimSpace(m.GetContent()) == "" {
				continue
			}
			out = append(out, ollamaChatMessage{Role: "system", Content: m.GetContent()})
		case "user":
			if strings.TrimSpace(m.GetContent()) == "" {
				continue
			}
			out = append(out, ollamaChatMessage{Role: "user", Content: m.GetContent()})
		case "assistant":
			if len(m.GetToolCalls()) > 0 {
				var calls []ollamaToolCall
				for _, tc := range m.GetToolCalls() {
					if tc == nil {
						continue
					}
					if tc.GetId() == "" || tc.GetName() == "" {
						return nil, fmt.Errorf("assistant tool call missing id or name")
					}
					argsStr := strings.TrimSpace(tc.GetArgumentsJson())
					if argsStr == "" {
						argsStr = "{}"
					}
					var argsObj any
					if err := json.Unmarshal([]byte(argsStr), &argsObj); err != nil {
						return nil, fmt.Errorf("tool %q arguments: %w", tc.GetName(), err)
					}
					calls = append(calls, ollamaToolCall{
						ID:   tc.GetId(),
						Type: "function",
						Function: ollamaToolFn{
							Name:      tc.GetName(),
							Arguments: argsObj,
						},
					})
				}
				out = append(out, ollamaChatMessage{
					Role:      "assistant",
					Content:   m.GetContent(),
					ToolCalls: calls,
				})
				continue
			}
			if strings.TrimSpace(m.GetContent()) == "" {
				continue
			}
			out = append(out, ollamaChatMessage{Role: "assistant", Content: m.GetContent()})
		case "tool":
			if strings.TrimSpace(m.GetContent()) == "" {
				continue
			}
			out = append(out, ollamaChatMessage{
				Role:       "tool",
				Content:    m.GetContent(),
				ToolCallID: m.GetToolCallId(),
			})
		default:
			return nil, fmt.Errorf("unknown message role %q", m.GetRole())
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no messages to send after conversion")
	}
	return out, nil
}

func assistantReplyFromOllamaMessage(ctx context.Context, respID string, msg ollamaChatMessage, finish string) (*llmv1.AssistantReply, error) {
	var calls []*llmv1.ToolCall
	for _, tc := range msg.ToolCalls {
		argsJSON := "{}"
		if tc.Function.Arguments != nil {
			b, err := json.Marshal(tc.Function.Arguments)
			if err != nil {
				return nil, fmt.Errorf("marshal tool arguments: %w", err)
			}
			argsJSON = string(b)
		}
		calls = append(calls, &llmv1.ToolCall{
			Id:            tc.ID,
			Name:          tc.Function.Name,
			ArgumentsJson: argsJSON,
		})
	}
	hook := memctx.AssistantMemoryHook(ctx)
	vis, think := assistanttext.ProcessUnary(msg.Content, "", hook)
	return &llmv1.AssistantReply{
		Content:         vis,
		ThinkingContent: think,
		Id:              respID,
		ToolCalls:       calls,
		FinishReason:    finish,
	}, nil
}
