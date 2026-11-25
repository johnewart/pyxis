package bedrockllm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	brdoc "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/memctx"
)

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func reasoningTextFromBlock(rc types.ReasoningContentBlock) string {
	switch v := rc.(type) {
	case *types.ReasoningContentBlockMemberReasoningText:
		return deref(v.Value.Text)
	default:
		return ""
	}
}

func documentToJSONString(d brdoc.Interface) (string, error) {
	if d == nil {
		return "{}", nil
	}
	var v any
	if err := d.UnmarshalSmithyDocument(&v); err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func assistantReplyFromBedrockMessage(ctx context.Context, msg types.Message, stop types.StopReason) (*llmv1.AssistantReply, error) {
	var text, thinking string
	var calls []*llmv1.ToolCall
	for _, block := range msg.Content {
		switch b := block.(type) {
		case *types.ContentBlockMemberText:
			text += b.Value
		case *types.ContentBlockMemberReasoningContent:
			thinking += reasoningTextFromBlock(b.Value)
		case *types.ContentBlockMemberToolUse:
			args, err := documentToJSONString(b.Value.Input)
			if err != nil {
				return nil, fmt.Errorf("tool %q arguments: %w", deref(b.Value.Name), err)
			}
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			calls = append(calls, &llmv1.ToolCall{
				Id:            deref(b.Value.ToolUseId),
				Name:          deref(b.Value.Name),
				ArgumentsJson: args,
			})
		default:
			// ignore images, etc.
		}
	}
	hook := memctx.AssistantMemoryHook(ctx)
	vis, think := assistanttext.ProcessUnary(text, thinking, hook)
	reply := &llmv1.AssistantReply{
		Content:         vis,
		ThinkingContent: think,
		Id:              "bedrock",
		FinishReason:    string(stop),
	}
	if len(calls) > 0 {
		reply.ToolCalls = calls
	}
	return reply, nil
}
