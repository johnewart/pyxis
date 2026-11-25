package bedrockllm

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/common"
)

// StructuredOutputToolName mirrors the Anthropic approach: Converse has no JSON
// mode, so a structured answer is obtained by forcing a single tool call whose
// input schema is the requested shape.
const StructuredOutputToolName = common.StructuredOutputToolName

// applyResponseFormat forces a structured answer on a Converse request.
//
// Returns true when forcing was applied. Skipped when the caller supplied real
// tools — see the Anthropic equivalent for why. Non-streaming only.
func applyResponseFormat(
	in *bedrockruntime.ConverseInput,
	opt *llmv1.CallOptions,
	callerTools int,
) bool {
	if in == nil {
		return false
	}
	rf, ok := common.PickResponseFormat(opt)
	if !ok || !rf.JSON || callerTools > 0 {
		return false
	}

	properties, required := rf.SchemaProperties()
	schemaDoc := map[string]any{"type": "object"}
	if properties != nil {
		schemaDoc["properties"] = properties
	}
	if len(required) > 0 {
		schemaDoc["required"] = required
	}

	in.ToolConfig = &types.ToolConfiguration{
		Tools: []types.Tool{
			&types.ToolMemberToolSpec{
				Value: types.ToolSpecification{
					Name:        aws.String(StructuredOutputToolName),
					Description: aws.String(common.StructuredOutputToolDescription),
					InputSchema: &types.ToolInputSchemaMemberJson{
						Value: document.NewLazyDocument(schemaDoc),
					},
				},
			},
		},
		ToolChoice: &types.ToolChoiceMemberTool{
			Value: types.SpecificToolChoice{Name: aws.String(StructuredOutputToolName)},
		},
	}
	return true
}
