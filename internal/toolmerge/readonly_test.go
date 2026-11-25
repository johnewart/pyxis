package toolmerge

import (
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestIsMutationTool(t *testing.T) {
	query := &llmv1.ToolDefinition{
		Name:           "grep",
		Classification: llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
	}
	mut := &llmv1.ToolDefinition{
		Name:           "write_file",
		Classification: llmv1.ToolClassification_TOOL_CLASSIFICATION_MUTATION,
	}
	unspec := &llmv1.ToolDefinition{Name: "unknown"}

	if IsMutationTool(query) {
		t.Fatal("query tool should not be mutation")
	}
	if !IsMutationTool(mut) {
		t.Fatal("mutation tool should be mutation")
	}
	if !IsMutationTool(unspec) {
		t.Fatal("unspecified tool should default to mutation")
	}
	if !IsMutationTool(nil) {
		t.Fatal("nil tool should default to mutation")
	}
}

func TestFilterForReadOnly(t *testing.T) {
	defs := []*llmv1.ToolDefinition{
		{Name: "grep", Classification: llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY},
		{Name: "write_file", Classification: llmv1.ToolClassification_TOOL_CLASSIFICATION_MUTATION},
		{Name: "legacy"},
	}
	out := FilterForReadOnly(defs)
	if len(out) != 1 || out[0].GetName() != "grep" {
		t.Fatalf("FilterForReadOnly = %v, want [grep]", toolNames(out))
	}
}

func toolNames(defs []*llmv1.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		if d != nil {
			names = append(names, d.GetName())
		}
	}
	return names
}
