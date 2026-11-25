package store

import (
	"encoding/json"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/protobuf/encoding/protojson"
)

// MarshalToolsJSON encodes a session's tool definitions for storage in
// llmd_sessions.tools_json.
func MarshalToolsJSON(tools []*llmv1.ToolDefinition) (string, error) {
	if len(tools) == 0 {
		return "[]", nil
	}
	raw := make([]json.RawMessage, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		b, err := protojson.Marshal(t)
		if err != nil {
			return "", err
		}
		raw = append(raw, json.RawMessage(b))
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalToolsJSON decodes tool definitions from storage.
func UnmarshalToolsJSON(s string) ([]*llmv1.ToolDefinition, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, err
	}
	out := make([]*llmv1.ToolDefinition, 0, len(raw))
	for _, r := range raw {
		var td llmv1.ToolDefinition
		if err := protojson.Unmarshal(r, &td); err != nil {
			continue
		}
		out = append(out, &td)
	}
	return out, nil
}
