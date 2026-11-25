package geminillm

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func cloneThoughtSignature(sig []byte) []byte {
	if len(sig) == 0 {
		return nil
	}
	return append([]byte(nil), sig...)
}

func toolCallFromFunctionCallPart(part *genai.Part) (*llmv1.ToolCall, error) {
	if part == nil || part.FunctionCall == nil {
		return nil, fmt.Errorf("part missing functionCall")
	}
	fc := part.FunctionCall
	if fc.ID == "" || fc.Name == "" {
		return nil, fmt.Errorf("functionCall missing id or name")
	}
	args := "{}"
	if len(fc.Args) > 0 {
		b, err := json.Marshal(fc.Args)
		if err != nil {
			return nil, fmt.Errorf("tool %q args: %w", fc.Name, err)
		}
		args = string(b)
	}
	return &llmv1.ToolCall{
		Id:               fc.ID,
		Name:             fc.Name,
		ArgumentsJson:    args,
		ThoughtSignature: cloneThoughtSignature(part.ThoughtSignature),
	}, nil
}

func toolCallsFromResponse(resp *genai.GenerateContentResponse) ([]*llmv1.ToolCall, error) {
	if resp == nil || len(resp.Candidates) == 0 {
		return nil, nil
	}
	cand := resp.Candidates[0].Content
	if cand == nil {
		return nil, nil
	}
	var calls []*llmv1.ToolCall
	for _, part := range cand.Parts {
		if part == nil || part.FunctionCall == nil {
			continue
		}
		tc, err := toolCallFromFunctionCallPart(part)
		if err != nil {
			return nil, err
		}
		calls = append(calls, tc)
	}
	return calls, nil
}

func functionCallPartFromToolCall(tc *llmv1.ToolCall) (*genai.Part, error) {
	if tc == nil {
		return nil, fmt.Errorf("nil tool call")
	}
	if tc.GetId() == "" || tc.GetName() == "" {
		return nil, fmt.Errorf("tool call missing id or name")
	}
	var args map[string]any
	raw := strings.TrimSpace(tc.GetArgumentsJson())
	if raw == "" {
		args = map[string]any{}
	} else if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("tool %q arguments_json: %w", tc.GetName(), err)
	}
	if args == nil {
		args = map[string]any{}
	}
	part := &genai.Part{
		FunctionCall: &genai.FunctionCall{
			ID:   tc.GetId(),
			Name: tc.GetName(),
			Args: args,
		},
		ThoughtSignature: cloneThoughtSignature(tc.GetThoughtSignature()),
	}
	return part, nil
}
