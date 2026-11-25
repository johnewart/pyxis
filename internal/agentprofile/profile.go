// Package agentprofile defines agent profiles: reusable session templates
// (system prompt sections, provider/model defaults, tool mapping) that a
// session can be created from.
//
// This package is deliberately a leaf. It may import the standard library,
// google.golang.org/protobuf, gen/llmv1, internal/coretools and
// internal/toolmerge (itself a leaf over those two) — nothing else.
// internal/server composes it into RPC handlers and internal/store will hold
// the dynamic (per-workspace) tier, so importing either from here would create
// a cycle. It performs no I/O and does no logging: every error carries the
// profile id / tool name in its text so the caller can log it with context.
package agentprofile

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnewart/pyxis/coretools"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/toolmerge"
	"google.golang.org/protobuf/proto"
)

// Tool policies, re-exported so callers can say agentprofile.ProfileOnly
// without naming the generated enum. See llm.v1.ToolPolicy.
const (
	// PolicyUnspecified behaves as InheritServerWide.
	PolicyUnspecified = llmv1.ToolPolicy_TOOL_POLICY_UNSPECIFIED
	// InheritServerWide: unlisted server-wide tools remain visible.
	InheritServerWide = llmv1.ToolPolicy_TOOL_POLICY_INHERIT_SERVER_WIDE
	// ProfileOnly: silence means no — the session sees only core tools and the
	// profile's enabled tools, so a tool registered server-wide later cannot
	// leak into sessions already created from this profile. A ProfileOnly
	// profile needs no Enabled=false mappings at all.
	ProfileOnly = llmv1.ToolPolicy_TOOL_POLICY_PROFILE_ONLY
)

// PolicyName is the persisted spelling of a policy ("" for inherit), shared
// with store.SessionRow.ToolPolicy and store.AgentProfileRow.ToolPolicy so
// there is one spelling across the wire, the DB and the merge.
func PolicyName(p llmv1.ToolPolicy) string {
	if p == ProfileOnly {
		return toolmerge.PolicyProfileOnlyName
	}
	return ""
}

// ParsePolicyName is PolicyName's inverse. Anything unrecognised — including
// "" — is InheritServerWide, so a row written before the column existed reads
// back as today's behaviour rather than an empty tool list.
func ParsePolicyName(s string) llmv1.ToolPolicy {
	if toolmerge.ParsePolicy(s) == toolmerge.ProfileOnly {
		return ProfileOnly
	}
	return InheritServerWide
}

// ToolMapping names one server-wide tool and whether the profile enables it.
//
// Enabled=true means the tool is materialized into the session's initial tool
// list; Enabled=false means it is placed on the session's disabled list so the
// server-wide default is hidden. Core tools (internal/coretools) can never be
// disabled — Validate rejects such a mapping.
type ToolMapping struct {
	ToolName string
	Enabled  bool
}

// Profile is the in-memory model of an agent profile, shared by the static
// (code-defined) and dynamic (store-backed) tiers.
type Profile struct {
	ID          string
	Name        string
	Description string

	// SystemPromptSections are materialized into
	// EnsureSessionRequest.system_prompt_sections.
	SystemPromptSections []*llmv1.SystemPromptSection

	LlmProviderID string
	DefaultModel  string
	MemoryEnabled bool

	ToolMapping []ToolMapping

	// ToolPolicy decides what an unlisted server-wide tool means for sessions
	// created from this profile. The zero value (PolicyUnspecified) behaves as
	// InheritServerWide, so a profile written before this field existed keeps
	// its behaviour. Set ProfileOnly to turn ToolMapping into an allowlist.
	ToolPolicy llmv1.ToolPolicy

	// Static marks a code-defined profile: read-only, no timestamps.
	Static bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// clone returns a deep-enough copy of p for handing out of a registry: the
// struct and the ToolMapping slice are copied so a caller cannot mutate the
// registry's state. Sections are shared pointers — ToProto and the
// materializer proto.Clone them before they leave the package.
func (p *Profile) clone() *Profile {
	if p == nil {
		return nil
	}
	out := *p
	if p.ToolMapping != nil {
		out.ToolMapping = make([]ToolMapping, len(p.ToolMapping))
		copy(out.ToolMapping, p.ToolMapping)
	}
	if p.SystemPromptSections != nil {
		out.SystemPromptSections = make([]*llmv1.SystemPromptSection, len(p.SystemPromptSections))
		copy(out.SystemPromptSections, p.SystemPromptSections)
	}
	return &out
}

// cloneSections deep-copies the profile's sections (nil entries dropped).
func cloneSections(in []*llmv1.SystemPromptSection) []*llmv1.SystemPromptSection {
	if len(in) == 0 {
		return nil
	}
	out := make([]*llmv1.SystemPromptSection, 0, len(in))
	for _, s := range in {
		if s == nil {
			continue
		}
		out = append(out, proto.Clone(s).(*llmv1.SystemPromptSection))
	}
	return out
}

// ToProto converts the profile to its wire representation. Nil-safe: a nil
// receiver yields nil. Sections are cloned so the caller owns the result.
func (p *Profile) ToProto() *llmv1.AgentProfile {
	if p == nil {
		return nil
	}
	out := &llmv1.AgentProfile{
		Id:                   p.ID,
		Name:                 p.Name,
		Description:          p.Description,
		SystemPromptSections: cloneSections(p.SystemPromptSections),
		MemoryEnabled:        p.MemoryEnabled,
		IsStatic:             p.Static,
		ToolPolicy:           p.ToolPolicy,
	}
	if v := strings.TrimSpace(p.LlmProviderID); v != "" {
		out.LlmProviderId = proto.String(v)
	}
	if v := strings.TrimSpace(p.DefaultModel); v != "" {
		out.DefaultModel = proto.String(v)
	}
	if len(p.ToolMapping) > 0 {
		out.ToolMapping = make([]*llmv1.ToolMapping, 0, len(p.ToolMapping))
		for _, m := range p.ToolMapping {
			out.ToolMapping = append(out.ToolMapping, &llmv1.ToolMapping{
				ToolName: m.ToolName,
				Enabled:  m.Enabled,
			})
		}
	}
	if !p.CreatedAt.IsZero() {
		out.CreatedAt = p.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !p.UpdatedAt.IsZero() {
		out.UpdatedAt = p.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// Validate checks the structural rules every profile must satisfy regardless
// of tier: non-empty id and name, non-empty and unique tool names, a known
// tool policy, and no core tool disabled. It does NOT check that tool names
// exist — static
// profiles are registered before the tool registry is populated, so that
// check happens at materialization time (BuildEnsureSessionRequest).
func Validate(p *Profile) error {
	if p == nil {
		return errors.New("agent profile: nil profile")
	}
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return errors.New("agent profile: id is required")
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("agent profile %q: name is required", id)
	}
	// Reject an unknown policy rather than silently treating it as inherit: a
	// caller that meant to deny by default must not get the permissive
	// behaviour because it sent a value this build does not know.
	switch p.ToolPolicy {
	case PolicyUnspecified, InheritServerWide, ProfileOnly:
	default:
		return fmt.Errorf("agent profile %q: unknown tool policy %v", id, p.ToolPolicy)
	}
	// A section with content but no id would be dropped at send time
	// (normalizeSystemPromptSections in internal/server), so the agent would
	// silently lose its instructions. Refuse it here instead.
	for i, s := range p.SystemPromptSections {
		if s == nil {
			continue
		}
		secID := strings.TrimSpace(s.GetId())
		if secID == "" && strings.TrimSpace(s.GetContent()) != "" {
			return fmt.Errorf("agent profile %q: system prompt section %d has content but no id", id, i)
		}
		if secID == IdentitySectionID {
			return fmt.Errorf("agent profile %q: system prompt section id %q is reserved for the profile's name and description", id, IdentitySectionID)
		}
	}
	seen := make(map[string]struct{}, len(p.ToolMapping))
	for i, m := range p.ToolMapping {
		name := strings.TrimSpace(m.ToolName)
		if name == "" {
			return fmt.Errorf("agent profile %q: tool mapping %d has an empty tool name", id, i)
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("agent profile %q: duplicate tool mapping for %q", id, name)
		}
		seen[name] = struct{}{}
		// ask_user is core but optional (coretools.IsGuaranteed): a profile
		// for an agent nobody can answer may switch it off.
		if !m.Enabled && coretools.IsGuaranteed(name) {
			return fmt.Errorf("agent profile %q: tool %q is a core tool and core tools cannot be disabled", id, name)
		}
	}
	return nil
}
