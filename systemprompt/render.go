// Package systemprompt renders provider-neutral system prompt sections with delivery overlays.
package systemprompt

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// SectionDelivery names where a logical section is sent for a provider run.
type SectionDelivery string

const (
	DeliveryPrompt       SectionDelivery = "prompt"
	DeliveryMDC          SectionDelivery = "mdc"
	DeliveryClaudeAppend SectionDelivery = "claude_append"
	DeliveryClaudeSystem SectionDelivery = "claude_system"
	DeliveryOmit         SectionDelivery = "omit"
)

// Section describes one logical block sent by the client over the wire, or taken
// from llmd's static provider-specific catalog.
type Section struct {
	ID      string
	Content string
	Tags    []string
}

// DeliveryTarget maps a section id to a provider-specific delivery surface.
type DeliveryTarget struct {
	Delivery  SectionDelivery
	File      string
	MergeInto string
}

// Overlay maps section id → delivery target. Missing ids default to prompt delivery.
type Overlay map[string]DeliveryTarget

// WorkspaceMDC is the Cursor rule file llmd writes into the workspace's .cursor/
// directory. The Cursor CLI has no system-prompt flag, so an alwaysApply rule
// file is the only channel for delivering prompt text to a Cursor run.
const WorkspaceMDC = "llmd-workspace.mdc"

// CursorOverlay routes duplicated MCP/isolation guidance to MDC instead of ### SYSTEM.
var CursorOverlay = Overlay{
	"conversation.toolUse": {Delivery: DeliveryMDC, MergeInto: WorkspaceMDC},
	"workspace.isolation":  {Delivery: DeliveryMDC, MergeInto: WorkspaceMDC},
}

// RenderResult is the provider-specific split of a logical system prompt.
type RenderResult struct {
	PromptBody   string
	MDCFiles     map[string]string
	ClaudeAppend string
	LogicalBody  string
	Fingerprint  Fingerprint
}

// Fingerprint hashes prompt-delivered, MDC, and Claude append surfaces separately.
type Fingerprint struct {
	Prompt       string
	MDC          string
	ClaudeAppend string
	Combined     string
}

func deliveryFor(id string, overlay Overlay) SectionDelivery {
	if overlay == nil {
		return DeliveryPrompt
	}
	if t, ok := overlay[id]; ok && t.Delivery != "" {
		return t.Delivery
	}
	return DeliveryPrompt
}

func flattenSections(sections []Section, overlay Overlay, allow func(SectionDelivery) bool) string {
	var parts []string
	for _, s := range sections {
		d := deliveryFor(s.ID, overlay)
		if allow != nil && !allow(d) {
			continue
		}
		t := strings.TrimSpace(s.Content)
		if t == "" {
			continue
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, "\n\n")
}

func mdcRule(body string) string {
	return "---\nalwaysApply: true\n---\n\n" + strings.TrimSpace(body) + "\n"
}

// RenderMDCFiles merges overlay MDC sections into filenames (values include front matter).
func RenderMDCFiles(sections []Section, overlay Overlay) map[string]string {
	if overlay == nil {
		return nil
	}
	merged := map[string]string{}
	for _, s := range sections {
		t, ok := overlay[s.ID]
		if !ok || t.Delivery != DeliveryMDC {
			continue
		}
		body := strings.TrimSpace(s.Content)
		if body == "" {
			continue
		}
		file := t.MergeInto
		if file == "" {
			file = t.File
		}
		if file == "" {
			file = s.ID + ".mdc"
		}
		if prev, ok := merged[file]; ok {
			merged[file] = prev + "\n\n" + body
		} else {
			merged[file] = body
		}
	}
	out := make(map[string]string, len(merged))
	for file, body := range merged {
		out[file] = mdcRule(body)
	}
	return out
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func fingerprintMDCFiles(files map[string]string) string {
	if len(files) == 0 {
		return hashText("")
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteByte(0)
		}
		b.WriteString(name)
		b.WriteByte(0)
		b.WriteString(files[name])
	}
	return hashText(b.String())
}

// RenderSystemPrompt splits sections into prompt body and MDC files using overlay delivery targets.
func RenderSystemPrompt(sections []Section, overlay Overlay) RenderResult {
	promptBody := flattenSections(sections, overlay, func(d SectionDelivery) bool { return d == DeliveryPrompt })
	claudeAppend := flattenSections(sections, overlay, func(d SectionDelivery) bool { return d == DeliveryClaudeAppend })
	logicalBody := flattenSections(sections, overlay, func(d SectionDelivery) bool { return d != DeliveryOmit })
	mdcFiles := RenderMDCFiles(sections, overlay)
	fp := Fingerprint{
		Prompt:       hashText(promptBody),
		MDC:          fingerprintMDCFiles(mdcFiles),
		ClaudeAppend: hashText(claudeAppend),
	}
	fp.Combined = hashText(fp.Prompt + "\x00" + fp.MDC + "\x00" + fp.ClaudeAppend)
	return RenderResult{
		PromptBody:   promptBody,
		MDCFiles:     mdcFiles,
		ClaudeAppend: claudeAppend,
		LogicalBody:  logicalBody,
		Fingerprint:  fp,
	}
}

// CombinedSessionFingerprint merges prompt and optional provider overlay fingerprints for resync.
func CombinedSessionFingerprint(promptFingerprint string, providerID string, providerKind string) string {
	if overlayKindIsCursor(providerKind, providerID) {
		mdcFP := hashText(DefaultCursorWorkspaceMDCBody())
		return hashText(promptFingerprint + "\x00" + mdcFP)
	}
	if overlayKindIsClaude(providerKind, providerID) {
		claudeFP := hashText(DefaultClaudeAppendBody())
		return hashText(promptFingerprint + "\x00" + claudeFP)
	}
	return promptFingerprint
}

// WithModelFingerprint folds the serving model into a session fingerprint so a
// model switch (or provider fan-out drift) forces a stateful full resync. A
// CLI --resume session (Claude Code v2 / Cursor) is bound to the model it was
// created under and will not restore prior context across models, so reusing a
// stale resume id after a model change silently drops conversation history.
func WithModelFingerprint(fp string, model string) string {
	m := strings.TrimSpace(model)
	if m == "" {
		return fp
	}
	return hashText(fp + "\x00model=" + m)
}

// DefaultCursorWorkspaceMDCBody returns the Cursor fallback rule body used only when no
// structured sections arrive. It contains PROVIDER-SPECIFIC isolation content only; the
// shared conversation bodies are owned by the client and delivered as structured sections.
func DefaultCursorWorkspaceMDCBody() string {
	sections := []Section{
		{ID: "workspace.isolation", Content: WorkspaceIsolationSection},
	}
	files := RenderMDCFiles(sections, CursorOverlay)
	return files[WorkspaceMDC]
}

// DefaultClaudeAppendBody returns the Claude Code append fallback body used only when no
// structured sections arrive. It contains PROVIDER-SPECIFIC isolation content only; the
// shared conversation bodies are owned by the client and delivered as structured sections.
func DefaultClaudeAppendBody() string {
	sections := []Section{
		{ID: "workspace.isolation", Content: ClaudeIsolationSection},
	}
	return RenderSystemPrompt(sections, ClaudeOverlay).ClaudeAppend
}