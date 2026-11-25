package anthropicllm

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func apply(t *testing.T, rf *llmv1.ResponseFormat, callerTools int) (anthropic.MessageNewParams, bool) {
	t.Helper()
	return applyResponseFormat(
		anthropic.MessageNewParams{},
		&llmv1.CallOptions{ResponseFormat: rf},
		callerTools,
	)
}

func TestApplyResponseFormat_NotRequested(t *testing.T) {
	body, forced := apply(t, nil, 0)
	if forced {
		t.Fatal("must not force without a request")
	}
	if len(body.Tools) != 0 {
		t.Fatalf("ordinary turn gained tools: %+v", body.Tools)
	}
}

func TestApplyResponseFormat_ForcesSyntheticTool(t *testing.T) {
	body, forced := apply(t, &llmv1.ResponseFormat{
		Kind: llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA,
		JsonSchema: `{
			"type": "object",
			"properties": {"title": {"type": "string"}},
			"required": ["title"]
		}`,
	}, 0)
	if !forced {
		t.Fatal("expected forcing")
	}
	if len(body.Tools) != 1 || body.Tools[0].OfTool == nil {
		t.Fatalf("expected one synthetic tool, got %+v", body.Tools)
	}
	tool := body.Tools[0].OfTool
	if tool.Name != StructuredOutputToolName {
		t.Fatalf("unexpected tool name %q", tool.Name)
	}
	if tool.InputSchema.Properties == nil {
		t.Fatal("schema properties not carried over")
	}
	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "title" {
		t.Fatalf("required not carried over: %+v", tool.InputSchema.Required)
	}
	if name := body.ToolChoice.GetName(); name == nil || *name != StructuredOutputToolName {
		t.Fatalf("tool choice not forced: %+v", body.ToolChoice)
	}
}

// Agent turns must keep their own tools. Tightening the response format is
// never worth breaking tool use.
func TestApplyResponseFormat_SkippedWhenCallerHasTools(t *testing.T) {
	body, forced := apply(t, &llmv1.ResponseFormat{
		Kind: llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_OBJECT,
	}, 3)
	if forced {
		t.Fatal("must not hijack a turn that has real tools")
	}
	if len(body.Tools) != 0 {
		t.Fatalf("must not inject a tool: %+v", body.Tools)
	}
}

func TestLiftStructuredOutput(t *testing.T) {
	reply := &llmv1.AssistantReply{
		Content: "",
		ToolCalls: []*llmv1.ToolCall{
			{Name: StructuredOutputToolName, ArgumentsJson: `{"title":"x"}`},
		},
	}
	liftStructuredOutput(reply)
	if reply.GetContent() != `{"title":"x"}` {
		t.Fatalf("arguments not lifted into content: %q", reply.GetContent())
	}
	// The synthetic call must not escape: callers would try to execute it.
	if len(reply.GetToolCalls()) != 0 {
		t.Fatalf("synthetic tool call leaked: %+v", reply.GetToolCalls())
	}
}

// A model that answers with text anyway keeps its answer.
func TestLiftStructuredOutput_NoForcedCallLeavesContent(t *testing.T) {
	reply := &llmv1.AssistantReply{Content: `{"title":"from text"}`}
	liftStructuredOutput(reply)
	if reply.GetContent() != `{"title":"from text"}` {
		t.Fatalf("content clobbered: %q", reply.GetContent())
	}
}

func TestLiftStructuredOutput_NilSafe(t *testing.T) {
	liftStructuredOutput(nil)
}
