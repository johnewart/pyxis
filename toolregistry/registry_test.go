package toolregistry

import (
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestGetReturnsIndependentClone(t *testing.T) {
	r := New()
	if err := r.Register(&llmv1.ToolDefinition{
		Name:                 "echo",
		Description:          "original",
		ParametersSchemaJson: `{"type":"object"}`,
		Execution:            &llmv1.ToolDefinition_DelegateToClient{DelegateToClient: &llmv1.ToolExecutionDelegateToClient{}},
	}); err != nil {
		t.Fatal(err)
	}

	got, ok := r.Get("echo")
	if !ok {
		t.Fatal("Get(echo) = not found, want found")
	}
	if got.GetDelegateToClient() == nil {
		t.Errorf("execution variant lost: %+v", got)
	}

	// Mutating the returned definition must not reach back into the registry.
	got.Description = "mutated"
	again, ok := r.Get("echo")
	if !ok {
		t.Fatal("Get(echo) = not found after mutation")
	}
	if again.GetDescription() != "original" {
		t.Errorf("registry entry mutated through Get: got %q, want %q", again.GetDescription(), "original")
	}
}

func TestGetMissingAndBlank(t *testing.T) {
	r := New()
	if _, ok := r.Get("nope"); ok {
		t.Error("Get on empty registry reported found")
	}
	if _, ok := r.Get("   "); ok {
		t.Error("Get with blank name reported found")
	}
}
