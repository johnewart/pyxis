package store

import (
	"encoding/json"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/protobuf/encoding/protojson"
)

// MarshalSystemPromptSectionsJSON encodes a session's default system prompt
// sections for storage in llmd_sessions.system_prompt_sections_json. Mirrors
// MarshalToolsJSON's shape (a JSON array of protojson-encoded messages).
func MarshalSystemPromptSectionsJSON(sections []*llmv1.SystemPromptSection) (string, error) {
	if len(sections) == 0 {
		return "", nil
	}
	raw := make([]json.RawMessage, 0, len(sections))
	for _, s := range sections {
		if s == nil {
			continue
		}
		b, err := protojson.Marshal(s)
		if err != nil {
			return "", err
		}
		raw = append(raw, json.RawMessage(b))
	}
	if len(raw) == 0 {
		return "", nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalSystemPromptSectionsJSON decodes a session's default system prompt
// sections from storage. Empty input yields a nil slice (no default set).
func UnmarshalSystemPromptSectionsJSON(s string) ([]*llmv1.SystemPromptSection, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, err
	}
	out := make([]*llmv1.SystemPromptSection, 0, len(raw))
	for _, r := range raw {
		var sec llmv1.SystemPromptSection
		if err := protojson.Unmarshal(r, &sec); err != nil {
			continue
		}
		out = append(out, &sec)
	}
	return out, nil
}
