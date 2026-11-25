package claudecodeclillm

import (
	_ "embed"
	"encoding/json"
	"sort"
	"sync"
)

// claudeBaseModelsJSON is the canonical cross-language model list. See the
// _comment field inside the file: it is the single source of truth for both the
// Go provider catalog and the generated TS constants.
//
//go:embed claude_base_models.json
var claudeBaseModelsJSON []byte

// ClaudeBaseModel is one tier: a moving alias plus any exact ids offered with it.
type ClaudeBaseModel struct {
	Alias     string   `json:"alias"`
	Label     string   `json:"label"`
	PinnedIDs []string `json:"pinnedIds"`
}

type claudeBaseModelsFile struct {
	BaseModels []ClaudeBaseModel `json:"baseModels"`
}

var (
	claudeBaseModelsOnce sync.Once
	claudeBaseModels     []ClaudeBaseModel
	claudeBaseAliases    []string
	claudeBasePrefixes   []string
)

func loadClaudeBaseModels() {
	claudeBaseModelsOnce.Do(func() {
		var parsed claudeBaseModelsFile
		// The file is embedded at build time, so a parse failure is a build-time
		// authoring error, not a runtime condition. Degrade to an empty list
		// rather than panicking inside a provider scan.
		_ = json.Unmarshal(claudeBaseModelsJSON, &parsed)
		claudeBaseModels = parsed.BaseModels

		for _, m := range parsed.BaseModels {
			if m.Alias != "" {
				claudeBaseAliases = append(claudeBaseAliases, m.Alias)
			}
		}

		for _, m := range parsed.BaseModels {
			claudeBasePrefixes = append(claudeBasePrefixes, m.PinnedIDs...)
			if m.Alias != "" {
				claudeBasePrefixes = append(claudeBasePrefixes, m.Alias)
			}
		}
		// Longest-first so prefix matching resolves the most specific id
		// ("claude-fable-5" must win over "fable"). Length ties are broken
		// lexicographically so the order is deterministic across builds.
		sort.SliceStable(claudeBasePrefixes, func(i, j int) bool {
			a, b := claudeBasePrefixes[i], claudeBasePrefixes[j]
			if len(a) != len(b) {
				return len(a) > len(b)
			}
			return a < b
		})
	})
}

// ClaudeBaseModels returns the canonical tiers in catalog order.
func ClaudeBaseModels() []ClaudeBaseModel {
	loadClaudeBaseModels()
	return claudeBaseModels
}

// ClaudeBaseAliases returns just the moving aliases (sonnet, opus, ...) in
// catalog order. The Claude Code CLI has no list-models API, so this IS the
// discoverable model catalog for the provider.
func ClaudeBaseAliases() []string {
	loadClaudeBaseModels()
	return claudeBaseAliases
}

// ClaudeBaseModelPrefixes returns every alias and pinned id, longest-first, for
// resolving a base model out of a profile slug.
func ClaudeBaseModelPrefixes() []string {
	loadClaudeBaseModels()
	return claudeBasePrefixes
}
