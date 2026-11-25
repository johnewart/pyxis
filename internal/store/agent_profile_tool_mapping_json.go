package store

import (
	"encoding/json"
	"strings"

	"github.com/johnewart/pyxis/internal/agentprofile"
)

// toolMappingWire is the on-disk shape of one agentprofile.ToolMapping entry.
// `enabled` deliberately has no omitempty: enabled=false is a meaningful value
// (the tool is explicitly disabled) and must survive a round trip.
type toolMappingWire struct {
	ToolName string `json:"tool_name"`
	Enabled  bool   `json:"enabled"`
}

// MarshalToolMappingJSON encodes a profile's tool mapping for storage in
// llmd_agent_profiles.tool_mapping_json. Same contract as
// MarshalSystemPromptSectionsJSON: an empty mapping is stored as "" rather
// than "[]" or "null".
func MarshalToolMappingJSON(mapping []agentprofile.ToolMapping) (string, error) {
	if len(mapping) == 0 {
		return "", nil
	}
	wire := make([]toolMappingWire, 0, len(mapping))
	for _, m := range mapping {
		wire = append(wire, toolMappingWire{ToolName: m.ToolName, Enabled: m.Enabled})
	}
	b, err := json.Marshal(wire)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalToolMappingJSON decodes a stored tool mapping. Empty input (after
// trimming whitespace) yields a nil slice and no error.
func UnmarshalToolMappingJSON(s string) ([]agentprofile.ToolMapping, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var wire []toolMappingWire
	if err := json.Unmarshal([]byte(s), &wire); err != nil {
		return nil, err
	}
	if len(wire) == 0 {
		return nil, nil
	}
	out := make([]agentprofile.ToolMapping, 0, len(wire))
	for _, w := range wire {
		out = append(out, agentprofile.ToolMapping{ToolName: w.ToolName, Enabled: w.Enabled})
	}
	return out, nil
}
