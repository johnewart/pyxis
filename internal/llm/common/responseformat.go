package common

import (
	"encoding/json"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// DefaultSchemaName is used when a caller requests a JSON schema but does not
// name it (OpenAI requires a name).
const DefaultSchemaName = "response"

// StructuredOutputToolName is the synthetic tool used by backends that have no
// JSON mode (Anthropic, Bedrock): declare a tool whose input schema is the
// desired shape, force its use, then read the arguments as the answer.
const StructuredOutputToolName = "emit_structured_response"

// StructuredOutputToolDescription is sent to the provider with the tool.
const StructuredOutputToolDescription = "Return the response as structured data. Call this exactly once."

// SchemaProperties extracts the "properties" and "required" members of a JSON
// Schema for SDKs that take them separately (Anthropic, Bedrock tool schemas).
// Missing or malformed members yield zero values rather than an error: the
// forced tool still constrains the answer to a single JSON object.
func (f ResponseFormat) SchemaProperties() (properties any, required []string) {
	if len(f.Schema) == 0 {
		return nil, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(f.Schema, &doc); err != nil {
		return nil, nil
	}
	properties = doc["properties"]
	if req, ok := doc["required"].([]any); ok {
		for _, r := range req {
			if s, isString := r.(string); isString {
				required = append(required, s)
			}
		}
	}
	return properties, required
}

// LiftStructuredOutput moves a forced structured-output tool call's arguments
// into the reply content, so these turns look like ordinary text completions.
//
// No-op when the model answered with text anyway — that content is already what
// the caller wanted, and clearing it would turn a usable answer into an empty
// one. The synthetic call is always removed so callers never try to execute it.
func LiftStructuredOutput(reply *llmv1.AssistantReply) {
	if reply == nil {
		return
	}
	for _, tc := range reply.GetToolCalls() {
		if tc.GetName() != StructuredOutputToolName {
			continue
		}
		if args := tc.GetArgumentsJson(); args != "" {
			reply.Content = args
		}
		reply.ToolCalls = nil
		return
	}
}

// ResponseFormat is the normalized, backend-agnostic form of
// llmv1.ResponseFormat. Backends map what they support and ignore the rest.
type ResponseFormat struct {
	// JSON reports that the caller asked for a JSON object of some kind.
	JSON bool
	// Schema is the raw JSON Schema when one was supplied AND parsed cleanly.
	// Nil means "JSON, shape unconstrained" — either the caller asked for
	// JSON_OBJECT, or they asked for JSON_SCHEMA with a schema we could not
	// parse and we degraded rather than failing the call.
	Schema json.RawMessage
	// SchemaName is non-empty whenever Schema is set.
	SchemaName string
	// Strict asks the provider to reject non-conforming output where it can.
	Strict bool
}

// PickResponseFormat normalizes the call's response format.
//
// The second return is false when no structured output was requested, so
// backends can leave their request untouched on the common path.
//
// A JSON_SCHEMA request carrying an unparseable schema degrades to plain JSON
// rather than erroring: the schema is a hint from a caller that already has to
// tolerate non-conforming output, and failing the whole generation because a
// schema string was malformed would be a worse outcome than a slightly less
// constrained response.
func PickResponseFormat(opt *llmv1.CallOptions) (ResponseFormat, bool) {
	if opt == nil {
		return ResponseFormat{}, false
	}
	rf := opt.GetResponseFormat()
	if rf == nil {
		return ResponseFormat{}, false
	}
	switch rf.GetKind() {
	case llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_OBJECT:
		return ResponseFormat{JSON: true}, true
	case llmv1.ResponseFormat_RESPONSE_FORMAT_KIND_JSON_SCHEMA:
		out := ResponseFormat{JSON: true, Strict: rf.GetStrict()}
		raw := strings.TrimSpace(rf.GetJsonSchema())
		if raw != "" && json.Valid([]byte(raw)) {
			out.Schema = json.RawMessage(raw)
			name := strings.TrimSpace(rf.GetSchemaName())
			if name == "" {
				name = DefaultSchemaName
			}
			out.SchemaName = name
		}
		return out, true
	default:
		// UNSPECIFIED: ordinary free-text completion.
		return ResponseFormat{}, false
	}
}

// rawSchema adapts json.RawMessage to json.Marshaler for SDKs that take one.
type rawSchema struct{ raw json.RawMessage }

func (r rawSchema) MarshalJSON() ([]byte, error) { return r.raw, nil }

// SchemaMarshaler wraps the schema for SDK fields typed as json.Marshaler.
// Returns nil when no schema is set, so callers can assign it unconditionally.
func (f ResponseFormat) SchemaMarshaler() json.Marshaler {
	if len(f.Schema) == 0 {
		return nil
	}
	return rawSchema{raw: f.Schema}
}
