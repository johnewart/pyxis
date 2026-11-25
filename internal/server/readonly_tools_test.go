package server

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestLlmServer_mergedToolsStream_filtersMutationWhenReadOnly(t *testing.T) {
	mgr := testAgentManager(t)
	ctx := context.Background()
	if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: "a1"}, testScope); err != nil {
		t.Fatal(err)
	}
	ma, ok := mgr.Get(testScope, "a1")
	if !ok {
		t.Fatal("agent missing")
	}
	if err := ma.Tools.Register(&llmv1.ToolDefinition{
		Name:                 "grep",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
		Classification:       llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
	}); err != nil {
		t.Fatal(err)
	}
	if err := ma.Tools.Register(&llmv1.ToolDefinition{
		Name:                 "write_file",
		ParametersSchemaJson: `{"type":"object","properties":{}}`,
		Classification:       llmv1.ToolClassification_TOOL_CLASSIFICATION_MUTATION,
	}); err != nil {
		t.Fatal(err)
	}

	s := &LlmServer{SessionRuntimes: mgr}
	all := s.mergedToolsStream(testScope.IncomingContext(context.Background()), "a1", false)()
	if len(all) < 2 {
		t.Fatalf("expected query+mutation tools, got %v", toolDefNames(all))
	}
	filtered := s.mergedToolsStream(testScope.IncomingContext(context.Background()), "a1", true)()
	for _, d := range filtered {
		if d != nil && d.GetName() == "write_file" {
			t.Fatalf("mutation tool should be filtered in read-only mode")
		}
	}
	if !toolDefinitionsContainName(filtered, "grep") {
		t.Fatalf("query tool should remain, got %v", toolDefNames(filtered))
	}
}
