package bedrockllm

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/common"
)

// applyToolChoice sets in.ToolConfig.ToolChoice from CallOptions.tool_choice.
// Must run after toolConfigurationFromProto. Converse has no "none" choice, so
// NONE returns InvalidArgument when tools are offered; REQUIRED and NAMED
// return InvalidArgument when reasoning fields are set on the request.
func applyToolChoice(in *bedrockruntime.ConverseInput, opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) error {
	mode, name, err := common.PickToolChoice(opt, tools)
	if err != nil {
		return err
	}
	if in == nil || in.ToolConfig == nil || len(tools) == 0 || opt.GetToolChoice() == nil {
		return nil
	}
	reasoning := in.AdditionalModelRequestFields != nil
	switch mode {
	case common.ToolChoiceAuto:
		in.ToolConfig.ToolChoice = &types.ToolChoiceMemberAuto{Value: types.AutoToolChoice{}}
	case common.ToolChoiceNone:
		return status.Error(codes.InvalidArgument, "tool_choice NONE is not supported by Bedrock Converse")
	case common.ToolChoiceRequired:
		if reasoning {
			return status.Error(codes.InvalidArgument, "tool_choice REQUIRED is not supported with reasoning enabled")
		}
		in.ToolConfig.ToolChoice = &types.ToolChoiceMemberAny{Value: types.AnyToolChoice{}}
	case common.ToolChoiceNamed:
		if reasoning {
			return status.Error(codes.InvalidArgument, "tool_choice NAMED is not supported with reasoning enabled")
		}
		in.ToolConfig.ToolChoice = &types.ToolChoiceMemberTool{Value: types.SpecificToolChoice{Name: aws.String(name)}}
	}
	return nil
}
