package agentprofile

import (
	"errors"
	"fmt"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/protobuf/proto"
)

// SessionOverrides are the per-session values a caller supplies on top of a
// profile when creating a session from it. Pointer fields override the
// profile's value only when non-nil and non-empty after trimming.
type SessionOverrides struct {
	SessionID   string
	DisplayName string
	ChatSurface string

	LlmProviderID *string
	DefaultModel  *string

	// AdditionalTools are appended (cloned) to the session's initial tools
	// after the profile's enabled tools.
	AdditionalTools []*llmv1.ToolDefinition
}

// toolDefByName indexes defs by trimmed name — same shape as the server's
// index, kept local so this package never imports internal/server.
func toolDefByName(defs []*llmv1.ToolDefinition) map[string]*llmv1.ToolDefinition {
	m := make(map[string]*llmv1.ToolDefinition, len(defs))
	for _, d := range defs {
		if d == nil {
			continue
		}
		n := strings.TrimSpace(d.GetName())
		if n == "" {
			continue
		}
		m[n] = d
	}
	return m
}

// pick returns the override when it is non-nil and non-empty after trimming,
// otherwise the (trimmed) fallback.
func pick(override *string, fallback string) string {
	if override != nil {
		if v := strings.TrimSpace(*override); v != "" {
			return v
		}
	}
	return strings.TrimSpace(fallback)
}

// IdentitySectionID is the id of the section BuildEnsureSessionRequest puts
// first in every session created from a profile, carrying the profile's name
// and description. Profiles may not define a section with this id themselves.
const IdentitySectionID = "agent.identity"

// identitySection tells the model who it is: the profile's name, and its
// description when set. Name and description are otherwise display-only, so
// without this an agent does not know its own name.
func identitySection(p *Profile) *llmv1.SystemPromptSection {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Your name is %s. When asked who you are or what your name is, answer as %s.", name, name)
	if desc := strings.TrimSpace(p.Description); desc != "" {
		fmt.Fprintf(&b, "\n\nDescription of you: %s", desc)
	}
	return &llmv1.SystemPromptSection{Id: IdentitySectionID, Content: b.String()}
}

// BuildEnsureSessionRequest materializes a profile plus per-session
// overrides into the EnsureSessionRequest the existing EnsureSession handler
// already understands. It is pure: no I/O, no logging.
//
// known is the server-wide tool list. Every ToolMapping entry must name a
// tool in it — static profiles cannot check names at registration time, so
// an unknown name surfaces here as an error carrying the tool and profile id.
// Enabled mappings clone the known definition into InitialTools; disabled
// mappings go on DisabledTools (Validate has already refused disabling a
// guaranteed core tool, so no exemption is applied here; a disabled ask_user
// lands there like any other). The profile's ToolPolicy is copied
// onto the request: under ProfileOnly the enabled mappings are an allowlist
// and negative mappings are unnecessary. The session's sections are the
// identity section (IdentitySectionID) followed by the profile's own.
func BuildEnsureSessionRequest(p *Profile, ov SessionOverrides, known []*llmv1.ToolDefinition) (*llmv1.EnsureSessionRequest, error) {
	if p == nil {
		return nil, errors.New("agent profile: nil profile")
	}
	sid := strings.TrimSpace(ov.SessionID)
	if sid == "" {
		return nil, fmt.Errorf("agent profile %q: session id is required", p.ID)
	}

	index := toolDefByName(known)
	sections := cloneSections(p.SystemPromptSections)
	if id := identitySection(p); id != nil {
		sections = append([]*llmv1.SystemPromptSection{id}, sections...)
	}
	req := &llmv1.EnsureSessionRequest{
		SessionId:            sid,
		SystemPromptSections: sections,
		// Carried through so the session enforces the profile's policy for its
		// whole life. Under ProfileOnly the enabled mappings below ARE the
		// allowlist: they land in InitialTools, and nothing else but core tools
		// reaches the session — including tools registered server-wide later.
		ToolPolicy: p.ToolPolicy,
	}
	for _, m := range p.ToolMapping {
		name := strings.TrimSpace(m.ToolName)
		def, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("agent profile %q: tool %q is not a registered tool", p.ID, name)
		}
		if m.Enabled {
			c := proto.Clone(def).(*llmv1.ToolDefinition)
			c.Name = name
			req.InitialTools = append(req.InitialTools, c)
		} else {
			req.DisabledTools = append(req.DisabledTools, name)
		}
	}
	for _, t := range ov.AdditionalTools {
		if t == nil {
			continue
		}
		req.InitialTools = append(req.InitialTools, proto.Clone(t).(*llmv1.ToolDefinition))
	}

	if v := strings.TrimSpace(ov.DisplayName); v != "" {
		req.DisplayName = proto.String(v)
	}
	if v := strings.TrimSpace(ov.ChatSurface); v != "" {
		req.ChatSurface = proto.String(v)
	}
	if v := pick(ov.LlmProviderID, p.LlmProviderID); v != "" {
		req.LlmProviderId = proto.String(v)
	}
	if v := pick(ov.DefaultModel, p.DefaultModel); v != "" {
		req.DefaultModel = proto.String(v)
	}
	return req, nil
}
