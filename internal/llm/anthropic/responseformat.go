package anthropicllm

import (
	"github.com/anthropics/anthropic-sdk-go"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/common"
)

// StructuredOutputToolName is the synthetic tool Anthropic is forced to call
// when the caller asks for structured output. Anthropic has no JSON mode; the
// supported equivalent is a tool whose input schema IS the desired shape.
const StructuredOutputToolName = common.StructuredOutputToolName

// applyResponseFormat forces a structured answer when one was requested.
//
// Returns true when forcing was applied, so the caller knows to lift the tool
// arguments into the reply content.
//
// Deliberately skipped when the caller supplied real tools: one turn cannot
// both force our synthetic tool and let the model choose among the caller's,
// and breaking agent tool use to tighten a response format would be the wrong
// trade. Those turns fall back to prompt-and-parse, which is why callers must
// still tolerate non-conforming output.
//
// Only wired into the non-streaming paths (Chat/Generate). A forced tool call
// arrives as tool-use frames rather than text, so applying it mid-stream would
// change the shape of the stream for no benefit — streaming turns are agent
// turns, which carry real tools and are skipped anyway.
func applyResponseFormat(
	body anthropic.MessageNewParams,
	opt *llmv1.CallOptions,
	callerTools int,
) (anthropic.MessageNewParams, bool) {
	rf, ok := common.PickResponseFormat(opt)
	if !ok || !rf.JSON || callerTools > 0 {
		return body, false
	}

	properties, required := rf.SchemaProperties()
	schema := anthropic.ToolInputSchemaParam{Required: required}
	if properties != nil {
		schema.Properties = properties
	}
	// With no usable schema the tool still forces a single JSON object —
	// shape unconstrained, matching json_object elsewhere.

	body.Tools = []anthropic.ToolUnionParam{{
		OfTool: &anthropic.ToolParam{
			Name:        StructuredOutputToolName,
			Description: anthropic.String(common.StructuredOutputToolDescription),
			InputSchema: schema,
		},
	}}
	body.ToolChoice = anthropic.ToolChoiceParamOfTool(StructuredOutputToolName)
	return body, true
}

// liftStructuredOutput moves the forced tool call's arguments into content.
func liftStructuredOutput(reply *llmv1.AssistantReply) {
	common.LiftStructuredOutput(reply)
}
