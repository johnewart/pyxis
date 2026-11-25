package llmtools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/sashabaranov/go-openai"
	"google.golang.org/genai"
)

// OllamaWireTool matches Ollama /api/chat "tools" entries (function tools).
type OllamaWireTool struct {
	Type     string       `json:"type"`
	Function OllamaWireFn `json:"function"`
}

type OllamaWireFn struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters"`
}

// ValidateParametersJSON ensures parameters_schema_json is valid JSON object-ish.
func ValidateParametersJSON(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return fmt.Errorf("parameters_schema_json: %w", err)
	}
	return nil
}

func anthropicInputSchema(raw string) (anthropic.ToolInputSchemaParam, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = `{"type":"object","properties":{}}`
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return anthropic.ToolInputSchemaParam{}, err
	}
	props, _ := m["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	var required []string
	if arr, ok := m["required"].([]any); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok {
				required = append(required, s)
			}
		}
	}
	return anthropic.ToolInputSchemaParam{
		Type:       constant.Object(""),
		Properties: props,
		Required:   required,
	}, nil
}

// ToAnthropicTools maps registered tools to Anthropic ToolUnionParam values.
func ToAnthropicTools(tools []*llmv1.ToolDefinition) ([]anthropic.ToolUnionParam, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	var out []anthropic.ToolUnionParam
	for _, td := range tools {
		if td == nil {
			continue
		}
		name := strings.TrimSpace(td.GetName())
		if name == "" {
			return nil, fmt.Errorf("tool name is empty")
		}
		schema, err := anthropicInputSchema(td.GetParametersSchemaJson())
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", name, err)
		}
		tp := &anthropic.ToolParam{
			Name:        name,
			Type:        anthropic.ToolTypeCustom,
			InputSchema: schema,
		}
		if d := strings.TrimSpace(td.GetDescription()); d != "" {
			tp.Description = param.NewOpt(d)
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: tp})
	}
	return out, nil
}

func openAIParameters(raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = `{"type":"object","properties":{}}`
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// ToOpenAITools maps registered tools to OpenAI Tool entries.
func ToOpenAITools(tools []*llmv1.ToolDefinition) ([]openai.Tool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	var out []openai.Tool
	for _, td := range tools {
		if td == nil {
			continue
		}
		name := strings.TrimSpace(td.GetName())
		if name == "" {
			return nil, fmt.Errorf("tool name is empty")
		}
		params, err := openAIParameters(td.GetParametersSchemaJson())
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", name, err)
		}
		out = append(out, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        name,
				Description: td.GetDescription(),
				Parameters:  params,
			},
		})
	}
	return out, nil
}

// ToOllamaTools maps registered tools to Ollama wire tools.
func ToOllamaTools(tools []*llmv1.ToolDefinition) ([]OllamaWireTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	var out []OllamaWireTool
	for _, td := range tools {
		if td == nil {
			continue
		}
		name := strings.TrimSpace(td.GetName())
		if name == "" {
			return nil, fmt.Errorf("tool name is empty")
		}
		params, err := openAIParameters(td.GetParametersSchemaJson())
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", name, err)
		}
		out = append(out, OllamaWireTool{
			Type: "function",
			Function: OllamaWireFn{
				Name:        name,
				Description: td.GetDescription(),
				Parameters:  params,
			},
		})
	}
	return out, nil
}

func geminiParametersSchema(raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = `{"type":"object","properties":{}}`
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// ToGeminiTools maps registered tools to Gemini Tool entries.
func ToGeminiTools(tools []*llmv1.ToolDefinition) ([]*genai.Tool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	var out []*genai.Tool
	for _, td := range tools {
		if td == nil {
			continue
		}
		name := strings.TrimSpace(td.GetName())
		if name == "" {
			return nil, fmt.Errorf("tool name is empty")
		}
		params, err := geminiParametersSchema(td.GetParametersSchemaJson())
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", name, err)
		}
		fd := &genai.FunctionDeclaration{
			Name:                 name,
			Description:          strings.TrimSpace(td.GetDescription()),
			ParametersJsonSchema: params,
		}
		out = append(out, &genai.Tool{FunctionDeclarations: []*genai.FunctionDeclaration{fd}})
	}
	return out, nil
}
