package geminillm

import (
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/genai"
)

func cfgWithFormat(t *testing.T, rf *llmv1.ResponseFormat) *genai.GenerateContentConfig {
	t.Helper()
	c := &Client{defaultModel: "gemini-3.5-flash", defaultMaxToks: 1024}
	cfg, err := c.buildConfig(nil, &llmv1.CallOptions{ResponseFormat: rf}, nil)
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	return cfg
}

func TestBuildConfig_NoFormatRequested(t *testing.T) {
	cfg := cfgWithFormat(t, nil)
	if cfg.ResponseMIMEType != "" || cfg.ResponseSchema != nil {
		t.Fatalf("ordinary turn must be untouched: %+v", cfg)
	}
}

func TestBuildConfig_JSONObject(t *testing.T) {
	cfg := cfgWithFormat(t, &llmv1.ResponseFormat{
		Kind: llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_OBJECT,
	})
	if cfg.ResponseMIMEType != "application/json" {
		t.Fatalf("expected json mime type, got %q", cfg.ResponseMIMEType)
	}
	if cfg.ResponseSchema != nil {
		t.Fatal("json_object must not carry a schema")
	}
}

// The MIME type is the JSON guarantee; the schema is the shape bonus. This
// pins that a normal JSON Schema really does convert, rather than quietly
// falling through the degrade path on every call.
func TestBuildConfig_JSONSchemaConverts(t *testing.T) {
	cfg := cfgWithFormat(t, &llmv1.ResponseFormat{
		Kind: llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA,
		JsonSchema: `{
			"type": "object",
			"properties": {
				"title": {"type": "string"},
				"count": {"type": "integer"}
			},
			"required": ["title"]
		}`,
	})
	if cfg.ResponseMIMEType != "application/json" {
		t.Fatalf("expected json mime type, got %q", cfg.ResponseMIMEType)
	}
	if cfg.ResponseSchema == nil {
		t.Fatal("expected the schema to convert")
	}
	if cfg.ResponseSchema.Type != genai.TypeObject {
		t.Fatalf("expected object schema, got %v", cfg.ResponseSchema.Type)
	}
	if _, ok := cfg.ResponseSchema.Properties["title"]; !ok {
		t.Fatalf("expected title property, got %+v", cfg.ResponseSchema.Properties)
	}
	if len(cfg.ResponseSchema.Required) != 1 || cfg.ResponseSchema.Required[0] != "title" {
		t.Fatalf("required not carried over: %+v", cfg.ResponseSchema.Required)
	}
}

// Nested types must be uppercased too, not just the root.
func TestBuildConfig_NestedTypesNormalized(t *testing.T) {
	cfg := cfgWithFormat(t, &llmv1.ResponseFormat{
		Kind: llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA,
		JsonSchema: `{
			"type": "object",
			"properties": {
				"files": {
					"type": "array",
					"items": {
						"type": "object",
						"properties": {"path": {"type": "string"}}
					}
				}
			}
		}`,
	})
	if cfg.ResponseSchema == nil {
		t.Fatal("expected the schema to convert")
	}
	files := cfg.ResponseSchema.Properties["files"]
	if files == nil || files.Type != genai.TypeArray {
		t.Fatalf("array type not normalized: %+v", files)
	}
	if files.Items == nil || files.Items.Type != genai.TypeObject {
		t.Fatalf("item type not normalized: %+v", files.Items)
	}
	if p := files.Items.Properties["path"]; p == nil || p.Type != genai.TypeString {
		t.Fatalf("leaf type not normalized: %+v", p)
	}
}

// Unconvertible schemas: keep JSON, drop the shape. Never fail the call.
func TestBuildConfig_UnconvertibleSchemaKeepsJSON(t *testing.T) {
	for name, schema := range map[string]string{
		"non-string type": `{"type": 12345}`,
		// genai has no union types; silently sending one would misrepresent it.
		"union type": `{"type": ["string", "null"]}`,
		"not json":   `{oops`,
		"no type":    `{"properties": {}}`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := cfgWithFormat(t, &llmv1.ResponseFormat{
				Kind:       llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA,
				JsonSchema: schema,
			})
			if cfg.ResponseMIMEType != "application/json" {
				t.Fatalf("must still force JSON, got %q", cfg.ResponseMIMEType)
			}
			if cfg.ResponseSchema != nil {
				t.Fatalf("expected schema to be dropped, got %+v", cfg.ResponseSchema)
			}
		})
	}
}
