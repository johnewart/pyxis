package anthropicllm

import (
	"github.com/anthropics/anthropic-sdk-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/common"
)

// applyToolChoice maps CallOptions.tool_choice onto body.ToolChoice. It is a
// no-op when no tools are offered (unless the mode is invalid) or when the
// caller did not set tool_choice. Must run after body.Tools is set.
//
// Anthropic rejects forced tool use (any/tool) when extended thinking is
// enabled, so REQUIRED and NAMED return InvalidArgument in that case rather
// than being downgraded.
func applyToolChoice(
	body anthropic.MessageNewParams,
	opt *llmv1.CallOptions,
	tools []*llmv1.ToolDefinition,
) (anthropic.MessageNewParams, error) {
	mode, name, err := common.PickToolChoice(opt, tools)
	if err != nil {
		return body, err
	}
	if len(tools) == 0 || opt.GetToolChoice() == nil {
		return body, nil
	}
	thinking := body.Thinking.OfEnabled != nil
	switch mode {
	case common.ToolChoiceAuto:
		body.ToolChoice = anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}
	case common.ToolChoiceNone:
		body.ToolChoice = anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
	case common.ToolChoiceRequired:
		if thinking {
			return body, status.Error(codes.InvalidArgument, "tool_choice REQUIRED is not supported with reasoning enabled")
		}
		body.ToolChoice = anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}
	case common.ToolChoiceNamed:
		if thinking {
			return body, status.Error(codes.InvalidArgument, "tool_choice NAMED is not supported with reasoning enabled")
		}
		body.ToolChoice = anthropic.ToolChoiceParamOfTool(name)
	}
	return body, nil
}
