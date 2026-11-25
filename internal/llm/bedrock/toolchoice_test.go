package bedrockllm

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestApplyToolChoice(t *testing.T) {
	tools := []*llmv1.ToolDefinition{{Name: "a"}, {Name: "b"}}
	tc := func(m llmv1.ToolChoice_Mode, name string) *llmv1.CallOptions {
		return &llmv1.CallOptions{ToolChoice: &llmv1.ToolChoice{Mode: m, ToolName: name}}
	}

	cases := []struct {
		name      string
		reasoning bool
		opt       *llmv1.CallOptions
		tools     []*llmv1.ToolDefinition
		check     func(t *testing.T, c types.ToolChoice)
		wantErr   bool
	}{
		{name: "unset", opt: &llmv1.CallOptions{}, tools: tools, check: func(t *testing.T, c types.ToolChoice) {
			if c != nil {
				t.Fatalf("unexpected choice %T", c)
			}
		}},
		{name: "auto", opt: tc(llmv1.ToolChoice_MODE_AUTO, ""), tools: tools, check: func(t *testing.T, c types.ToolChoice) {
			if _, ok := c.(*types.ToolChoiceMemberAuto); !ok {
				t.Fatalf("want auto, got %T", c)
			}
		}},
		{name: "required", opt: tc(llmv1.ToolChoice_MODE_REQUIRED, ""), tools: tools, check: func(t *testing.T, c types.ToolChoice) {
			if _, ok := c.(*types.ToolChoiceMemberAny); !ok {
				t.Fatalf("want any, got %T", c)
			}
		}},
		{name: "named", opt: tc(llmv1.ToolChoice_MODE_NAMED, "b"), tools: tools, check: func(t *testing.T, c types.ToolChoice) {
			m, ok := c.(*types.ToolChoiceMemberTool)
			if !ok || m.Value.Name == nil || *m.Value.Name != "b" {
				t.Fatalf("want tool b, got %#v", c)
			}
		}},
		{name: "none", opt: tc(llmv1.ToolChoice_MODE_NONE, ""), tools: tools, wantErr: true},
		{name: "required without tools", opt: tc(llmv1.ToolChoice_MODE_REQUIRED, ""), wantErr: true},
		{name: "named unknown tool", opt: tc(llmv1.ToolChoice_MODE_NAMED, "zzz"), tools: tools, wantErr: true},
		{name: "required with reasoning", reasoning: true, opt: tc(llmv1.ToolChoice_MODE_REQUIRED, ""), tools: tools, wantErr: true},
		{name: "named with reasoning", reasoning: true, opt: tc(llmv1.ToolChoice_MODE_NAMED, "a"), tools: tools, wantErr: true},
		{name: "auto with reasoning", reasoning: true, opt: tc(llmv1.ToolChoice_MODE_AUTO, ""), tools: tools, check: func(t *testing.T, c types.ToolChoice) {
			if _, ok := c.(*types.ToolChoiceMemberAuto); !ok {
				t.Fatalf("want auto, got %T", c)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := &bedrockruntime.ConverseInput{ToolConfig: &types.ToolConfiguration{}}
			if c.reasoning {
				in.AdditionalModelRequestFields = document.NewLazyDocument(map[string]any{"thinking": map[string]any{"type": "enabled"}})
			}
			err := applyToolChoice(in, c.opt, c.tools)
			if c.wantErr {
				if status.Code(err) != codes.InvalidArgument {
					t.Fatalf("want InvalidArgument, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			c.check(t, in.ToolConfig.ToolChoice)
		})
	}
}
