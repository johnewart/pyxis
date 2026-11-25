package openaillm

import (
	"context"
	"fmt"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/memctx"
	"github.com/sashabaranov/go-openai"
)

// ProtoToOpenAIMessages maps gRPC messages to OpenAI chat completion messages (system/user/assistant/tool).
func ProtoToOpenAIMessages(messages []*llmv1.Message) ([]openai.ChatCompletionMessage, error) {
	var out []openai.ChatCompletionMessage
	for _, m := range messages {
		if m == nil {
			continue
		}
		switch m.GetRole() {
		case "system":
			if strings.TrimSpace(m.GetContent()) == "" {
				continue
			}
			out = append(out, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleSystem,
				Content: m.GetContent(),
			})
		case "user":
			if strings.TrimSpace(m.GetContent()) == "" {
				continue
			}
			out = append(out, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: m.GetContent(),
			})
		case "assistant":
			if len(m.GetToolCalls()) > 0 {
				var tcalls []openai.ToolCall
				for _, tc := range m.GetToolCalls() {
					if tc == nil {
						continue
					}
					if tc.GetId() == "" || tc.GetName() == "" {
						return nil, fmt.Errorf("assistant tool call missing id or name")
					}
					args := strings.TrimSpace(tc.GetArgumentsJson())
					if args == "" {
						args = "{}"
					}
					tcalls = append(tcalls, openai.ToolCall{
						ID:   tc.GetId(),
						Type: openai.ToolTypeFunction,
						Function: openai.FunctionCall{
							Name:      tc.GetName(),
							Arguments: args,
						},
					})
				}
				assist := openai.ChatCompletionMessage{
					Role:      openai.ChatMessageRoleAssistant,
					Content:   m.GetContent(),
					ToolCalls: tcalls,
				}
				if strings.TrimSpace(m.GetThinkingContent()) != "" {
					assist.ReasoningContent = m.GetThinkingContent()
				}
				out = append(out, assist)
				continue
			}
			if strings.TrimSpace(m.GetContent()) == "" && strings.TrimSpace(m.GetThinkingContent()) == "" {
				continue
			}
			assist := openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleAssistant,
				Content: m.GetContent(),
			}
			if strings.TrimSpace(m.GetThinkingContent()) != "" {
				assist.ReasoningContent = m.GetThinkingContent()
			}
			out = append(out, assist)
		case "tool":
			if strings.TrimSpace(m.GetContent()) == "" {
				continue
			}
			out = append(out, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
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

func assistantReplyFromOpenAI(ctx context.Context, resp openai.ChatCompletionResponse) (*llmv1.AssistantReply, error) {
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai: empty choices")
	}
	msg := resp.Choices[0].Message
	var calls []*llmv1.ToolCall
	for _, tc := range msg.ToolCalls {
		args := strings.TrimSpace(tc.Function.Arguments)
		if args == "" {
			args = "{}"
		}
		calls = append(calls, &llmv1.ToolCall{
			Id:            tc.ID,
			Name:          tc.Function.Name,
			ArgumentsJson: args,
		})
	}
	hook := memctx.AssistantMemoryHook(ctx)
	vis, think := assistanttext.ProcessUnary(msg.Content, msg.ReasoningContent, hook)
	return &llmv1.AssistantReply{
		Content:         vis,
		ThinkingContent: think,
		Id:              resp.ID,
		ToolCalls:       calls,
		FinishReason:    string(resp.Choices[0].FinishReason),
	}, nil
}
