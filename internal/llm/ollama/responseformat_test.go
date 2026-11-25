package ollamallm

import (
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func format(t *testing.T, rf *llmv1.ResponseFormat) string {
	t.Helper()
	c := &Client{defaultModel: "llama3", defaultMaxToks: 1024}
	return string(c.buildFormat(&llmv1.CallOptions{ResponseFormat: rf}))
}

func TestBuildFormat_NotRequested(t *testing.T) {
	// Omitted entirely so ordinary turns are byte-identical on the wire.
	if got := format(t, nil); got != "" {
		t.Fatalf("expected no format, got %q", got)
	}
}

func TestBuildFormat_JSONObject(t *testing.T) {
	if got := format(t, &llmv1.ResponseFormat{
		Kind: llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_OBJECT,
	}); got != `"json"` {
		t.Fatalf(`expected "json", got %q`, got)
	}
}

func TestBuildFormat_JSONSchemaPassesThroughVerbatim(t *testing.T) {
	schema := `{"type":"object","properties":{"title":{"type":"string"}}}`
	if got := format(t, &llmv1.ResponseFormat{
		Kind:       llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA,
		JsonSchema: schema,
	}); got != schema {
		t.Fatalf("schema mutated: %s", got)
	}
}

func TestBuildFormat_MalformedSchemaFallsBackToJSON(t *testing.T) {
	if got := format(t, &llmv1.ResponseFormat{
		Kind:       llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA,
		JsonSchema: "{not json",
	}); got != `"json"` {
		t.Fatalf(`expected "json" fallback, got %q`, got)
	}
}
