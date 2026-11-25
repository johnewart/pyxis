package common

import (
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// ToolChoiceMode is the normalized, backend-agnostic tool selection mode.
// It never holds UNSPECIFIED: that is folded into ToolChoiceAuto.
type ToolChoiceMode int

const (
	// ToolChoiceAuto lets the model decide whether to call a tool.
	ToolChoiceAuto ToolChoiceMode = iota
	// ToolChoiceNone forbids tool calls.
	ToolChoiceNone
	// ToolChoiceRequired forces at least one tool call.
	ToolChoiceRequired
	// ToolChoiceNamed forces a call to one specific tool.
	ToolChoiceNamed
)

// PickToolChoice validates opt.ToolChoice against the supplied tools and
// returns the normalized mode and, for ToolChoiceNamed, the tool name.
//
// A nil opt, an unset tool_choice, UNSPECIFIED and AUTO all yield
// ToolChoiceAuto, so existing callers are unchanged. NONE is always valid.
// REQUIRED needs at least one tool; NAMED needs a non-empty name that matches
// one of the tools. Violations return an InvalidArgument status error.
func PickToolChoice(opt *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (ToolChoiceMode, string, error) {
	tc := opt.GetToolChoice()
	if tc == nil {
		return ToolChoiceAuto, "", nil
	}
	switch tc.GetMode() {
	case llmv1.ToolChoice_MODE_UNSPECIFIED, llmv1.ToolChoice_MODE_AUTO:
		return ToolChoiceAuto, "", nil
	case llmv1.ToolChoice_MODE_NONE:
		return ToolChoiceNone, "", nil
	case llmv1.ToolChoice_MODE_REQUIRED:
		if len(tools) == 0 {
			return ToolChoiceAuto, "", status.Error(codes.InvalidArgument, "tool_choice REQUIRED needs at least one tool")
		}
		return ToolChoiceRequired, "", nil
	case llmv1.ToolChoice_MODE_NAMED:
		name := strings.TrimSpace(tc.GetToolName())
		if name == "" {
			return ToolChoiceAuto, "", status.Error(codes.InvalidArgument, "tool_choice NAMED needs a tool name")
		}
		if len(tools) == 0 {
			return ToolChoiceAuto, "", status.Errorf(codes.InvalidArgument, "tool_choice NAMED %q but no tools were supplied", name)
		}
		for _, t := range tools {
			if t.GetName() == name {
				return ToolChoiceNamed, name, nil
			}
		}
		return ToolChoiceAuto, "", status.Errorf(codes.InvalidArgument, "tool_choice NAMED %q does not match any supplied tool", name)
	default:
		return ToolChoiceAuto, "", status.Errorf(codes.InvalidArgument, "unknown tool_choice mode %d", int32(tc.GetMode()))
	}
}
