package agentprofile

import (
	"context"
	"errors"
	"fmt"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"google.golang.org/protobuf/proto"
)

// OverlayStore holds each workspace's overlays on static profiles.
//
// Contract: Get returns (nil, nil) when the scope has no overlay for baseID;
// any error is a real failure and the Resolver propagates it unchanged.
type OverlayStore interface {
	Get(ctx context.Context, sc serverutil.Scope, baseID string) (*llmv1.AgentProfileOverlay, error)
	List(ctx context.Context, sc serverutil.Scope) ([]*llmv1.AgentProfileOverlay, error)
}

// ErrNotStatic is returned when an overlay names a profile that is not in the
// static tier: only code-defined profiles take overlays.
var ErrNotStatic = errors.New("agent profile is not static")

// ApplyOverlay returns base with ov applied, following the merge rules on
// llm.v1.AgentProfileOverlay. base is not modified. The result keeps
// Static=true and lists the fields the overlay changed in Customized. A nil
// or empty overlay returns a copy of base with no customized fields.
//
// It fails when ov names a different profile or adds a section whose id
// collides with a base section; the merged profile is then validated with
// Validate, so an overlay cannot, say, disable a core tool.
func ApplyOverlay(base *Profile, ov *llmv1.AgentProfileOverlay) (*Profile, error) {
	if base == nil {
		return nil, errors.New("agent profile overlay: nil base")
	}
	out := base.clone()
	out.Customized = nil
	if ov == nil {
		return out, nil
	}
	if id := strings.TrimSpace(ov.GetBaseId()); id != "" && id != base.ID {
		return nil, fmt.Errorf("agent profile overlay for %q applied to %q", id, base.ID)
	}
	mark := func(f llmv1.AgentProfileOverlayField) { out.Customized = append(out.Customized, f) }

	if ov.LlmProviderId != nil {
		out.LlmProviderID = strings.TrimSpace(ov.GetLlmProviderId())
		mark(llmv1.AgentProfileOverlayField_AGENT_PROFILE_OVERLAY_FIELD_LLM_PROVIDER)
	}
	if ov.DefaultModel != nil {
		out.DefaultModel = strings.TrimSpace(ov.GetDefaultModel())
		mark(llmv1.AgentProfileOverlayField_AGENT_PROFILE_OVERLAY_FIELD_DEFAULT_MODEL)
	}
	if ov.MemoryEnabled != nil {
		out.MemoryEnabled = ov.GetMemoryEnabled()
		mark(llmv1.AgentProfileOverlayField_AGENT_PROFILE_OVERLAY_FIELD_MEMORY_ENABLED)
	}

	if len(ov.GetToolOverrides()) > 0 {
		idx := make(map[string]int, len(out.ToolMapping))
		for i, m := range out.ToolMapping {
			idx[m.ToolName] = i
		}
		for _, t := range ov.GetToolOverrides() {
			if t == nil {
				continue
			}
			name := strings.TrimSpace(t.GetToolName())
			if name == "" {
				return nil, fmt.Errorf("agent profile overlay %q: tool override with an empty tool name", base.ID)
			}
			if i, ok := idx[name]; ok {
				out.ToolMapping[i].Enabled = t.GetEnabled()
				continue
			}
			idx[name] = len(out.ToolMapping)
			out.ToolMapping = append(out.ToolMapping, ToolMapping{ToolName: name, Enabled: t.GetEnabled()})
		}
		mark(llmv1.AgentProfileOverlayField_AGENT_PROFILE_OVERLAY_FIELD_TOOLS)
	}

	prompt := false
	if len(ov.GetSectionReplacements()) > 0 {
		repl := make(map[string]*llmv1.SystemPromptSection, len(ov.GetSectionReplacements()))
		for _, s := range ov.GetSectionReplacements() {
			if s == nil {
				continue
			}
			repl[strings.TrimSpace(s.GetId())] = s
		}
		for i, s := range out.SystemPromptSections {
			if s == nil {
				continue
			}
			if r, ok := repl[s.GetId()]; ok {
				c := proto.Clone(s).(*llmv1.SystemPromptSection)
				c.Content = r.GetContent()
				out.SystemPromptSections[i] = c
				prompt = true
			}
		}
	}
	if len(ov.GetExtraSections()) > 0 {
		have := make(map[string]struct{}, len(out.SystemPromptSections))
		for _, s := range out.SystemPromptSections {
			if s != nil {
				have[s.GetId()] = struct{}{}
			}
		}
		for _, s := range ov.GetExtraSections() {
			if s == nil {
				continue
			}
			id := strings.TrimSpace(s.GetId())
			if _, dup := have[id]; dup {
				return nil, fmt.Errorf("agent profile overlay %q: extra section id %q is already a section of the base; use a section replacement", base.ID, id)
			}
			have[id] = struct{}{}
			c := proto.Clone(s).(*llmv1.SystemPromptSection)
			c.Id = id
			out.SystemPromptSections = append(out.SystemPromptSections, c)
			prompt = true
		}
	}
	if prompt {
		mark(llmv1.AgentProfileOverlayField_AGENT_PROFILE_OVERLAY_FIELD_SYSTEM_PROMPT)
	}

	if err := Validate(out); err != nil {
		return nil, fmt.Errorf("agent profile overlay %q: %w", base.ID, err)
	}
	return out, nil
}
