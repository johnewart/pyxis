package bedrockllm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	brdoc "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func toolConfigurationFromProto(tools []*llmv1.ToolDefinition) (*types.ToolConfiguration, error) {
	var bedrockTools []types.Tool
	for _, t := range tools {
		if t == nil {
			continue
		}
		name := strings.TrimSpace(t.GetName())
		if name == "" {
			continue
		}
		schemaJSON := strings.TrimSpace(t.GetParametersSchemaJson())
		if schemaJSON == "" {
			schemaJSON = `{"type":"object","properties":{}}`
		}
		var schema any
		if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
			return nil, fmt.Errorf("tool %q parameters_schema_json: %w", name, err)
		}
		desc := t.GetDescription()
		spec := types.ToolSpecification{
			Name:        aws.String(name),
			Description: aws.String(desc),
			InputSchema: &types.ToolInputSchemaMemberJson{Value: brdoc.NewLazyDocument(schema)},
		}
		bedrockTools = append(bedrockTools, &types.ToolMemberToolSpec{Value: spec})
	}
	if len(bedrockTools) == 0 {
		return nil, nil
	}
	return &types.ToolConfiguration{Tools: bedrockTools}, nil
}
