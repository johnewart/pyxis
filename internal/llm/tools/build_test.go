package llmtools

import (
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestToGeminiTools_schemaAndEmptyName(t *testing.T) {
	tools, err := ToGeminiTools([]*llmv1.ToolDefinition{{
		Name:                  "echo",
		Description:           "echo back",
		ParametersSchemaJson:  `{"type":"object","properties":{"message":{"type":"string"}}}`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || len(tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools=%v", tools)
	}
	fd := tools[0].FunctionDeclarations[0]
	if fd.Name != "echo" {
		t.Fatalf("name=%q", fd.Name)
	}
	if fd.ParametersJsonSchema == nil {
		t.Fatal("expected default schema object")
	}

	_, err = ToGeminiTools([]*llmv1.ToolDefinition{{Name: "   "}})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err=%v", err)
	}
}

func TestToGeminiTools_defaultSchema(t *testing.T) {
	tools, err := ToGeminiTools([]*llmv1.ToolDefinition{{Name: "noop"}})
	if err != nil {
		t.Fatal(err)
	}
	if tools[0].FunctionDeclarations[0].ParametersJsonSchema == nil {
		t.Fatal("expected default schema")
	}
}
