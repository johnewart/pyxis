package store

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MarshalToolContextJSON encodes a session's tool_context map for storage in
// llmd_sessions.tool_context_json (SessionRow.ToolContextJSON). A nil or empty
// map encodes as "" so the keepIfBlank conflict clause leaves any stored value
// alone.
func MarshalToolContextJSON(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal tool_context: %w", err)
	}
	return string(b), nil
}

// UnmarshalToolContextJSON decodes SessionRow.ToolContextJSON. Blank input
// yields a nil map.
func UnmarshalToolContextJSON(s string) (map[string]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("unmarshal tool_context: %w", err)
	}
	if len(m) == 0 {
		return nil, nil
	}
	return m, nil
}
