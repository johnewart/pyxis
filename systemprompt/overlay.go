package systemprompt

import "strings"

// ClaudeOverlay routes MCP-only guidance to Claude Code --append-system-prompt.
var ClaudeOverlay = Overlay{
	"conversation.toolUse": {Delivery: DeliveryClaudeAppend},
	"workspace.isolation":  {Delivery: DeliveryClaudeAppend},
}

func overlayKindIsCursor(providerKind, providerID string) bool {
	k := strings.ToLower(strings.TrimSpace(providerKind))
	id := strings.TrimSpace(providerID)
	return k == "cursor_cli" || strings.EqualFold(id, "cursor")
}

func overlayKindIsClaude(providerKind, providerID string) bool {
	k := strings.ToLower(strings.TrimSpace(providerKind))
	id := strings.TrimSpace(providerID)
	return k == "claude_code" || k == "claude_code_v2" ||
		strings.EqualFold(id, "claude_sdk") || strings.EqualFold(id, "claude_v2")
}

// OverlayForProvider returns the delivery overlay for a resolved llmd provider id/kind string.
func OverlayForProvider(providerID string, providerKind string) Overlay {
	if overlayKindIsCursor(providerKind, providerID) {
		return CursorOverlay
	}
	if overlayKindIsClaude(providerKind, providerID) {
		return ClaudeOverlay
	}
	return nil
}

const isolationSectionID = "workspace.isolation"

// ProviderIsolationSectionContent returns the provider-specific workspace.isolation body
// owned by llmd (not the client). ok=false means this provider uses no isolation section.
// llmd is the source of truth here so callers stay provider-agnostic.
func ProviderIsolationSectionContent(providerID string, providerKind string) (string, bool) {
	if overlayKindIsClaude(providerKind, providerID) {
		return ClaudeIsolationSection, true
	}
	if overlayKindIsCursor(providerKind, providerID) {
		return WorkspaceIsolationSection, true
	}
	return "", false
}

// EnsureProviderIsolationSection overrides (or inserts) the workspace.isolation section with
// llmd's provider-owned body, and drops any client-sent isolation section for providers that
// use none. Returns a new slice; the input is not mutated.
func EnsureProviderIsolationSection(sections []Section, providerID string, providerKind string) []Section {
	body, ok := ProviderIsolationSectionContent(providerID, providerKind)
	out := make([]Section, 0, len(sections)+1)
	replaced := false
	for _, s := range sections {
		if s.ID == isolationSectionID {
			if !ok {
				continue // provider has no isolation section; drop client-sent one
			}
			s.Content = body
			replaced = true
		}
		out = append(out, s)
	}
	if ok && !replaced {
		out = append(out, Section{ID: isolationSectionID, Content: body})
	}
	return out
}
