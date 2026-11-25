package anthropicllm

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestApplyToolChoice(t *testing.T) {
	tools := []*llmv1.ToolDefinition{{Name: "a"}, {Name: "b"}}
	tc := func(m llmv1.ToolChoice_Mode, name string) *llmv1.CallOptions {
		return &llmv1.CallOptions{ToolChoice: &llmv1.ToolChoice{Mode: m, ToolName: name}}
	}
	thinking := anthropic.MessageNewParams{
		Thinking: anthropic.ThinkingConfigParamUnion{OfEnabled: &anthropic.ThinkingConfigEnabledParam{BudgetTokens: 2048}},
	}

	cases := []struct {
		name    string
		body    anthropic.MessageNewParams
		opt     *llmv1.CallOptions
		tools   []*llmv1.ToolDefinition
		check   func(t *testing.T, b anthropic.MessageNewParams)
		wantErr bool
	}{
		{name: "unset", opt: &llmv1.CallOptions{}, tools: tools, check: func(t *testing.T, b anthropic.MessageNewParams) {
			if b.ToolChoice.OfAuto != nil || b.ToolChoice.OfAny != nil || b.ToolChoice.OfNone != nil || b.ToolChoice.OfTool != nil {
				t.Fatalf("unexpected tool choice: %+v", b.ToolChoice)
			}
		}},
		{name: "auto", opt: tc(llmv1.ToolChoice_MODE_AUTO, ""), tools: tools, check: func(t *testing.T, b anthropic.MessageNewParams) {
			if b.ToolChoice.OfAuto == nil {
				t.Fatalf("want auto: %+v", b.ToolChoice)
			}
		}},
		{name: "auto without tools is a no-op", opt: tc(llmv1.ToolChoice_MODE_AUTO, ""), check: func(t *testing.T, b anthropic.MessageNewParams) {
			if b.ToolChoice.OfAuto != nil {
				t.Fatalf("must not set tool choice without tools: %+v", b.ToolChoice)
			}
		}},
		{name: "none", opt: tc(llmv1.ToolChoice_MODE_NONE, ""), tools: tools, check: func(t *testing.T, b anthropic.MessageNewParams) {
			if b.ToolChoice.OfNone == nil {
				t.Fatalf("want none: %+v", b.ToolChoice)
			}
		}},
		{name: "required", opt: tc(llmv1.ToolChoice_MODE_REQUIRED, ""), tools: tools, check: func(t *testing.T, b anthropic.MessageNewParams) {
			if b.ToolChoice.OfAny == nil {
				t.Fatalf("want any: %+v", b.ToolChoice)
			}
		}},
		{name: "named", opt: tc(llmv1.ToolChoice_MODE_NAMED, "b"), tools: tools, check: func(t *testing.T, b anthropic.MessageNewParams) {
			if n := b.ToolChoice.GetName(); n == nil || *n != "b" {
				t.Fatalf("want tool b: %+v", b.ToolChoice)
			}
		}},
		{name: "required without tools", opt: tc(llmv1.ToolChoice_MODE_REQUIRED, ""), wantErr: true},
		{name: "named unknown tool", opt: tc(llmv1.ToolChoice_MODE_NAMED, "zzz"), tools: tools, wantErr: true},
		{name: "named empty name", opt: tc(llmv1.ToolChoice_MODE_NAMED, ""), tools: tools, wantErr: true},
		{name: "required with reasoning", body: thinking, opt: tc(llmv1.ToolChoice_MODE_REQUIRED, ""), tools: tools, wantErr: true},
		{name: "named with reasoning", body: thinking, opt: tc(llmv1.ToolChoice_MODE_NAMED, "a"), tools: tools, wantErr: true},
		{name: "none with reasoning", body: thinking, opt: tc(llmv1.ToolChoice_MODE_NONE, ""), tools: tools, check: func(t *testing.T, b anthropic.MessageNewParams) {
			if b.ToolChoice.OfNone == nil {
				t.Fatalf("want none: %+v", b.ToolChoice)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := applyToolChoice(c.body, c.opt, c.tools)
			if c.wantErr {
				if status.Code(err) != codes.InvalidArgument {
					t.Fatalf("want InvalidArgument, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			c.check(t, got)
		})
	}
}
