package store

import (
	"encoding/json"
	"sort"
	"strings"
)

// normalizeDisabledToolNames trims each name, drops empties, dedups and sorts
// ascending so the stored representation is canonical regardless of the order
// or spelling a caller supplied. Returns nil when nothing survives.
func normalizeDisabledToolNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// MarshalDisabledToolsJSON encodes a session's default tool exclusions for
// storage in llmd_sessions.disabled_tools_json (SessionRow.DisabledToolsJSON).
// Names are trimmed, empties dropped, duplicates removed and the result sorted
// before encoding as a plain JSON string array. Follows the same empty ⇄ ""
// contract as MarshalSystemPromptSectionsJSON: when nothing survives
// normalisation the result is "" (never "[]" or "null"), so a row with no
// exclusions is indistinguishable from one written before the column existed.
func MarshalDisabledToolsJSON(names []string) (string, error) {
	norm := normalizeDisabledToolNames(names)
	if len(norm) == 0 {
		return "", nil
	}
	b, err := json.Marshal(norm)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalDisabledToolsJSON decodes SessionRow.DisabledToolsJSON back into a
// normalised (trimmed, deduped, sorted) list of tool names. Empty or
// whitespace-only input yields nil, nil — the "no exclusions" case shared with
// MarshalSystemPromptSectionsJSON's empty ⇄ "" contract. Unlike the sections
// codec, a malformed payload is returned as an error rather than skipped:
// this is a flat string array, so there is no per-element recovery to attempt.
func UnmarshalDisabledToolsJSON(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(s), &names); err != nil {
		return nil, err
	}
	return normalizeDisabledToolNames(names), nil
}
