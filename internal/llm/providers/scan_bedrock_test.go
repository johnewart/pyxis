package providers

import (
	"testing"

	btypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
)

func TestMergeBedrockFoundationModels(t *testing.T) {
	t.Parallel()
	providerID := "bedrock"
	nameA := "Titan Text"
	nameEmbed := "Titan Embed"
	pages := [][]btypes.FoundationModelSummary{
		{
			{ModelId: strPtr("anthropic.claude-3-5-sonnet"), ModelName: strPtr("Claude 3.5 Sonnet")},
			{ModelId: strPtr("amazon.titan-text"), ModelName: &nameA},
		},
		{
			{ModelId: strPtr("amazon.titan-embed-text-v2"), ModelName: &nameEmbed},
			{ModelId: strPtr("amazon.titan-text"), ModelName: &nameA},
		},
	}
	out := mergeBedrockFoundationModels(providerID, pages)
	if len(out) != 3 {
		t.Fatalf("expected 3 unique models, got %d: %+v", len(out), out)
	}
	byID := make(map[string]string, len(out))
	for _, m := range out {
		byID[m.ModelID] = m.Display
	}
	if byID["amazon.titan-embed-text-v2"] != nameEmbed {
		t.Fatalf("missing embedding model: %+v", byID)
	}
	if byID["anthropic.claude-3-5-sonnet"] != "Claude 3.5 Sonnet" {
		t.Fatalf("missing text model: %+v", byID)
	}
}

func strPtr(s string) *string {
	return &s
}
