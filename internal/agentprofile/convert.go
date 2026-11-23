package agentprofile

import (
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/protobuf/proto"
)

// FromFields builds a Profile from the wire fields of a profile. Strings are
// trimmed, sections are cloned so the caller's buffer is not aliased, and
// every tool mapping is copied as-is — an Enabled=false entry is meaningful
// (it blocks a server-wide default) and must never be dropped. The tool
// policy is copied through as given; Validate rejects a value this build does
// not know rather than defaulting it to the permissive INHERIT_SERVER_WIDE.
func FromFields(id, name, description string, sections []*llmv1.SystemPromptSection, providerID, model *string, memoryEnabled bool, mapping []*llmv1.ToolMapping, policy llmv1.ToolPolicy) *Profile {
	p := &Profile{
		ID:            strings.TrimSpace(id),
		Name:          strings.TrimSpace(name),
		Description:   strings.TrimSpace(description),
		MemoryEnabled: memoryEnabled,
		ToolPolicy:    policy,
	}
	if providerID != nil {
		p.LlmProviderID = strings.TrimSpace(*providerID)
	}
	if model != nil {
		p.DefaultModel = strings.TrimSpace(*model)
	}
	for _, sec := range sections {
		if sec == nil {
			continue
		}
		p.SystemPromptSections = append(p.SystemPromptSections, proto.Clone(sec).(*llmv1.SystemPromptSection))
	}
	for _, m := range mapping {
		if m == nil {
			continue
		}
		p.ToolMapping = append(p.ToolMapping, ToolMapping{
			ToolName: strings.TrimSpace(m.GetToolName()),
			Enabled:  m.GetEnabled(),
		})
	}
	return p
}

// FromProto builds a Profile from its wire representation. Timestamps and
// is_static are not read: a code-defined profile has neither, and the tier a
// profile belongs to is decided by where it is registered, not by the caller.
func FromProto(ap *llmv1.AgentProfile) *Profile {
	if ap == nil {
		return nil
	}
	return FromFields(ap.GetId(), ap.GetName(), ap.GetDescription(), ap.GetSystemPromptSections(),
		ap.LlmProviderId, ap.DefaultModel, ap.GetMemoryEnabled(), ap.GetToolMapping(), ap.GetToolPolicy())
}
