package common

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestPickToolChoice(t *testing.T) {
	tools := []*llmv1.ToolDefinition{{Name: "a"}, {Name: "b"}}
	opt := func(m llmv1.ToolChoice_Mode, name string) *llmv1.CallOptions {
		return &llmv1.CallOptions{ToolChoice: &llmv1.ToolChoice{Mode: m, ToolName: name}}
	}
	cases := []struct {
		name     string
		opt      *llmv1.CallOptions
		tools    []*llmv1.ToolDefinition
		wantMode ToolChoiceMode
		wantName string
		wantErr  bool
	}{
		{"nil opt", nil, tools, ToolChoiceAuto, "", false},
		{"unset", &llmv1.CallOptions{}, tools, ToolChoiceAuto, "", false},
		{"unspecified", opt(llmv1.ToolChoice_MODE_UNSPECIFIED, ""), tools, ToolChoiceAuto, "", false},
		{"auto", opt(llmv1.ToolChoice_MODE_AUTO, ""), tools, ToolChoiceAuto, "", false},
		{"auto no tools", opt(llmv1.ToolChoice_MODE_AUTO, ""), nil, ToolChoiceAuto, "", false},
		{"none", opt(llmv1.ToolChoice_MODE_NONE, ""), tools, ToolChoiceNone, "", false},
		{"required", opt(llmv1.ToolChoice_MODE_REQUIRED, ""), tools, ToolChoiceRequired, "", false},
		{"required no tools", opt(llmv1.ToolChoice_MODE_REQUIRED, ""), nil, 0, "", true},
		{"named", opt(llmv1.ToolChoice_MODE_NAMED, "b"), tools, ToolChoiceNamed, "b", false},
		{"named no tools", opt(llmv1.ToolChoice_MODE_NAMED, "a"), nil, 0, "", true},
		{"named empty name", opt(llmv1.ToolChoice_MODE_NAMED, "  "), tools, 0, "", true},
		{"named unknown", opt(llmv1.ToolChoice_MODE_NAMED, "zzz"), tools, 0, "", true},
		{"unknown mode", opt(llmv1.ToolChoice_Mode(99), ""), tools, 0, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, name, err := PickToolChoice(tc.opt, tc.tools)
			if tc.wantErr {
				if status.Code(err) != codes.InvalidArgument {
					t.Fatalf("want InvalidArgument, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mode != tc.wantMode || name != tc.wantName {
				t.Fatalf("got (%v, %q), want (%v, %q)", mode, name, tc.wantMode, tc.wantName)
			}
		})
	}
}
