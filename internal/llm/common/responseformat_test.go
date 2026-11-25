package common

import (
	"encoding/json"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func opts(rf *llmv1.ResponseFormat) *llmv1.CallOptions {
	return &llmv1.CallOptions{ResponseFormat: rf}
}

func TestPickResponseFormat_NotRequested(t *testing.T) {
	for name, opt := range map[string]*llmv1.CallOptions{
		"nil options":    nil,
		"no format":      {},
		"explicit unset": opts(&llmv1.ResponseFormat{Kind: llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_UNSPECIFIED}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := PickResponseFormat(opt); ok {
				t.Fatalf("expected no structured output request")
			}
		})
	}
}

func TestPickResponseFormat_JSONObject(t *testing.T) {
	got, ok := PickResponseFormat(opts(&llmv1.ResponseFormat{
		Kind: llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_OBJECT,
	}))
	if !ok || !got.JSON {
		t.Fatalf("expected JSON request, got %+v ok=%v", got, ok)
	}
	if got.Schema != nil {
		t.Fatalf("json_object must not carry a schema: %s", got.Schema)
	}
}

func TestPickResponseFormat_JSONSchema(t *testing.T) {
	schema := `{"type":"object","properties":{"title":{"type":"string"}}}`
	got, ok := PickResponseFormat(opts(&llmv1.ResponseFormat{
		Kind:       llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA,
		JsonSchema: schema,
		SchemaName: "pr_overview",
		Strict:     true,
	}))
	if !ok || !got.JSON {
		t.Fatalf("expected JSON request")
	}
	if string(got.Schema) != schema {
		t.Fatalf("schema round trip changed it: %s", got.Schema)
	}
	if got.SchemaName != "pr_overview" || !got.Strict {
		t.Fatalf("unexpected name/strict: %+v", got)
	}
}

func TestPickResponseFormat_DefaultsSchemaName(t *testing.T) {
	got, _ := PickResponseFormat(opts(&llmv1.ResponseFormat{
		Kind:       llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA,
		JsonSchema: `{"type":"object"}`,
	}))
	if got.SchemaName != DefaultSchemaName {
		t.Fatalf("expected default schema name, got %q", got.SchemaName)
	}
}

// A malformed schema must not fail the generation: degrade to plain JSON.
func TestPickResponseFormat_MalformedSchemaDegrades(t *testing.T) {
	for _, bad := range []string{`{"type":`, "not json at all", "   "} {
		got, ok := PickResponseFormat(opts(&llmv1.ResponseFormat{
			Kind:       llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA,
			JsonSchema: bad,
		}))
		if !ok || !got.JSON {
			t.Fatalf("expected degraded JSON request for %q", bad)
		}
		if got.Schema != nil {
			t.Fatalf("expected no schema for %q, got %s", bad, got.Schema)
		}
		if got.SchemaName != "" {
			t.Fatalf("schema name without a schema for %q", bad)
		}
	}
}

func TestSchemaMarshaler(t *testing.T) {
	if (ResponseFormat{JSON: true}).SchemaMarshaler() != nil {
		t.Fatalf("expected nil marshaler without a schema")
	}
	schema := `{"type":"object"}`
	m := ResponseFormat{JSON: true, Schema: json.RawMessage(schema)}.SchemaMarshaler()
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Must pass through verbatim rather than being re-encoded.
	if string(out) != schema {
		t.Fatalf("schema mutated: %s", out)
	}
}
