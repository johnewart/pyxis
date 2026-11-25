package geminillm

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/memctx"
)

func assistantReplyFromResponse(ctx context.Context, resp *genai.GenerateContentResponse) (*llmv1.AssistantReply, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil gemini response")
	}
	text := resp.Text()
	calls, err := toolCallsFromResponse(resp)
	if err != nil {
		return nil, err
	}
	finish := ""
	if len(resp.Candidates) > 0 {
		finish = string(resp.Candidates[0].FinishReason)
	}
	hook := memctx.AssistantMemoryHook(ctx)
	vis, think := assistanttext.ProcessUnary(text, "", hook)
	reply := &llmv1.AssistantReply{
		Content:         vis,
		ThinkingContent: think,
		Id:              resp.ResponseID,
		FinishReason:    finish,
	}
	if len(calls) > 0 {
		reply.ToolCalls = calls
	}
	if strings.TrimSpace(reply.GetContent()) == "" && len(calls) == 0 {
		return reply, nil
	}
	return reply, nil
}
