package openaillm

import (
	"encoding/json"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	openai "github.com/sashabaranov/go-openai"
)

func reqWithFormat(t *testing.T, rf *llmv1.ResponseFormat) openai.ChatCompletionRequest {
	t.Helper()
	c := New("k", "", "gpt-4o-mini", 1024)
	req, err := c.chatRequest(
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
		false,
		&llmv1.CallOptions{ResponseFormat: rf},
		nil,
	)
	if err != nil {
		t.Fatalf("chatRequest: %v", err)
	}
	return req
}

// Ordinary turns must be byte-for-byte unaffected.
func TestChatRequest_NoFormatRequested(t *testing.T) {
	if got := reqWithFormat(t, nil).ResponseFormat; got != nil {
		t.Fatalf("expected no response_format, got %+v", got)
	}
}

func TestChatRequest_JSONObject(t *testing.T) {
	got := reqWithFormat(t, &llmv1.ResponseFormat{
		Kind: llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_OBJECT,
	}).ResponseFormat
	if got == nil || got.Type != openai.ChatCompletionResponseFormatTypeJSONObject {
		t.Fatalf("expected json_object, got %+v", got)
	}
	if got.JSONSchema != nil {
		t.Fatalf("json_object must not carry a schema")
	}
}

func TestChatRequest_JSONSchema(t *testing.T) {
	schema := `{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`
	got := reqWithFormat(t, &llmv1.ResponseFormat{
		Kind:       llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA,
		JsonSchema: schema,
		SchemaName: "pr_overview",
		Strict:     true,
	}).ResponseFormat
	if got == nil || got.Type != openai.ChatCompletionResponseFormatTypeJSONSchema {
		t.Fatalf("expected json_schema, got %+v", got)
	}
	if got.JSONSchema == nil {
		t.Fatal("missing JSONSchema payload")
	}
	if got.JSONSchema.Name != "pr_overview" || !got.JSONSchema.Strict {
		t.Fatalf("unexpected name/strict: %+v", got.JSONSchema)
	}
	out, err := json.Marshal(got.JSONSchema.Schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if string(out) != schema {
		t.Fatalf("schema mutated on the way out: %s", out)
	}
}

// Schema requested but unusable: still ask for JSON, just unconstrained.
func TestChatRequest_MalformedSchemaFallsBackToJSONObject(t *testing.T) {
	got := reqWithFormat(t, &llmv1.ResponseFormat{
		Kind:       llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA,
		JsonSchema: "{oops",
	}).ResponseFormat
	if got == nil || got.Type != openai.ChatCompletionResponseFormatTypeJSONObject {
		t.Fatalf("expected json_object fallback, got %+v", got)
	}
}
