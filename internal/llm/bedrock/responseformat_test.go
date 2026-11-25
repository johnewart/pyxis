package bedrockllm

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func applyTo(t *testing.T, rf *llmv1.ResponseFormat, callerTools int) (*bedrockruntime.ConverseInput, bool) {
	t.Helper()
	in := &bedrockruntime.ConverseInput{}
	forced := applyResponseFormat(in, &llmv1.CallOptions{ResponseFormat: rf}, callerTools)
	return in, forced
}

func TestApplyResponseFormat_NotRequested(t *testing.T) {
	in, forced := applyTo(t, nil, 0)
	if forced {
		t.Fatal("must not force without a request")
	}
	if in.ToolConfig != nil {
		t.Fatalf("ordinary turn gained a tool config: %+v", in.ToolConfig)
	}
}

func TestApplyResponseFormat_ForcesSyntheticTool(t *testing.T) {
	in, forced := applyTo(t, &llmv1.ResponseFormat{
		Kind:       llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA,
		JsonSchema: `{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`,
	}, 0)
	if !forced {
		t.Fatal("expected forcing")
	}
	if in.ToolConfig == nil || len(in.ToolConfig.Tools) != 1 {
		t.Fatalf("expected one tool, got %+v", in.ToolConfig)
	}
	spec, ok := in.ToolConfig.Tools[0].(*types.ToolMemberToolSpec)
	if !ok {
		t.Fatalf("unexpected tool member type %T", in.ToolConfig.Tools[0])
	}
	if spec.Value.Name == nil || *spec.Value.Name != StructuredOutputToolName {
		t.Fatalf("unexpected tool name: %+v", spec.Value.Name)
	}
	choice, ok := in.ToolConfig.ToolChoice.(*types.ToolChoiceMemberTool)
	if !ok {
		t.Fatalf("tool choice not forced: %T", in.ToolConfig.ToolChoice)
	}
	if choice.Value.Name == nil || *choice.Value.Name != StructuredOutputToolName {
		t.Fatalf("forced the wrong tool: %+v", choice.Value.Name)
	}
}

// Agent turns keep their own tools.
func TestApplyResponseFormat_SkippedWhenCallerHasTools(t *testing.T) {
	in, forced := applyTo(t, &llmv1.ResponseFormat{
		Kind: llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_OBJECT,
	}, 2)
	if forced || in.ToolConfig != nil {
		t.Fatalf("must not hijack a turn with real tools: %+v", in.ToolConfig)
	}
}

// No schema still forces a single JSON object.
func TestApplyResponseFormat_NoSchemaStillForces(t *testing.T) {
	in, forced := applyTo(t, &llmv1.ResponseFormat{
		Kind: llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_OBJECT,
	}, 0)
	if !forced || in.ToolConfig == nil {
		t.Fatalf("expected forcing without a schema, got %+v", in.ToolConfig)
	}
}

func TestApplyResponseFormat_NilInput(t *testing.T) {
	if applyResponseFormat(nil, &llmv1.CallOptions{}, 0) {
		t.Fatal("nil input must not report forcing")
	}
}
