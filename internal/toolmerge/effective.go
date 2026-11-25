package toolmerge

import (
	"strings"

	"github.com/johnewart/pyxis/coretools"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// Policy decides what an unlisted server-wide tool means for a session.
//
// The distinction exists because a purely subtractive model cannot express an
// allowlist: with InheritServerWide, a profile that wants exactly read_code has
// to enumerate every other tool as disabled, and any tool registered
// server-wide AFTERWARDS is silently gained by every session written before it.
// ProfileOnly makes silence mean no, so a tool registered later cannot leak
// into an existing session.
type Policy int

const (
	// InheritServerWide: every server-wide tool is visible unless disabled.
	InheritServerWide Policy = iota
	// ProfileOnly: the session sees ONLY core tools and the tools it
	// registered itself (which is where a profile's enabled mappings land —
	// BuildEnsureSessionRequest clones them into initial_tools). A tool that
	// is reachable only because it is registered server-wide is not in the
	// set, whether it was registered before or after the session existed.
	ProfileOnly
)

// PolicyProfileOnlyName is the persisted spelling of ProfileOnly (see
// store.SessionRow.ToolPolicy). InheritServerWide persists as "" so a row
// written before the column existed reads back as today's behaviour.
const PolicyProfileOnlyName = "profile_only"

// ParsePolicy maps a persisted/transport string to a Policy. Anything other
// than PolicyProfileOnlyName — including "" — is InheritServerWide, so an
// unrecognised value fails OPEN to today's behaviour rather than silently
// stripping every server-wide tool from a session.
func ParsePolicy(s string) Policy {
	if strings.TrimSpace(s) == PolicyProfileOnlyName {
		return ProfileOnly
	}
	return InheritServerWide
}

// String returns the persisted spelling; InheritServerWide is "".
func (p Policy) String() string {
	if p == ProfileOnly {
		return PolicyProfileOnlyName
	}
	return ""
}

// Origin records WHY a tool is in a session's effective set, so "why does this
// agent have X" is answerable without reading code.
type Origin string

const (
	// OriginCore: provided by llmd itself (coretools). No policy hides it;
	// only an optional one (ask_user) can be disabled.
	OriginCore Origin = "core"
	// OriginSession: registered by/for this session — a profile's enabled
	// mapping (initial_tools) or a later RegisterSessionTool.
	OriginSession Origin = "session-registered"
	// OriginServerWide: inherited from the server-wide registry. Only
	// reachable under InheritServerWide.
	OriginServerWide Origin = "server-wide"
)

// Request is the full input to one effective-set computation.
type Request struct {
	// ServerWide is the process-wide tool registry snapshot.
	ServerWide []*llmv1.ToolDefinition
	// Session is what this session registered (initial_tools + any later
	// RegisterSessionTool calls).
	Session []*llmv1.ToolDefinition
	// Disabled are the session's exclusions, by trimmed name. Applied LAST
	// and unconditionally: deny wins over session registration.
	Disabled map[string]struct{}
	// Policy decides the fate of tools present only in ServerWide.
	Policy Policy
}

// Entry is one tool of the effective set plus its provenance.
type Entry struct {
	Def    *llmv1.ToolDefinition
	Origin Origin
}

// Name is the entry's trimmed tool name.
func (e Entry) Name() string { return strings.TrimSpace(e.Def.GetName()) }

// Effective computes the session's effective tool set — the single definition
// of what a session may see AND execute:
//
//	effective = (core ∪ session-registered ∪ server-wide?) − disabled
//
// where server-wide is included only under InheritServerWide, and `− disabled`
// is applied last so deny wins. Core tools survive the policy: every agent
// gets them by default. Guaranteed core tools (coretools.IsGuaranteed: all but
// ask_user) survive the disabled list too: a session that cannot set_status or
// keep its todo list is broken no matter how it was configured (the same
// exemption Validate enforces at the profile level). ask_user can be disabled:
// a host whose runner cannot wait for an answer switches it off rather than
// let the agent leave a question nobody will see.
//
// This is an ENFORCEMENT point, not a presentation filter. Every path that can
// reach a handler must resolve the tool through this result — advertisement
// (ListSessionTools, the provider tool list, MCP tools/list) and execution
// (the ChatStream tool loop, the unary InvokeTool RPC) alike. Hiding a tool
// without refusing it leaves it reachable by any client that names it anyway.
//
// Output is the name-sorted order of MergeDefinitions. Definitions are the
// clones MergeDefinitions produced, so the caller owns them.
func Effective(req Request) []Entry {
	merged := MergeDefinitions(req.ServerWide, req.Session)
	fromSession := trimmedNameSet(req.Session)
	out := make([]Entry, 0, len(merged))
	for _, d := range merged {
		name := strings.TrimSpace(d.GetName())
		isCore := coretools.IsCore(name)
		_, registered := fromSession[name]

		// Deny last and absolute. A blocked tool a client could unblock by
		// registering it would not be a control, and registering a session
		// tool is reachable by any client.
		if _, off := req.Disabled[name]; off && !coretools.IsGuaranteed(name) {
			continue
		}
		// Silence means no (core tools are on by default).
		if !isCore && req.Policy == ProfileOnly && !registered {
			continue
		}

		origin := OriginServerWide
		switch {
		case isCore:
			origin = OriginCore
		case registered:
			origin = OriginSession
		}
		out = append(out, Entry{Def: d, Origin: origin})
	}
	return out
}

// EffectiveDefinitions is Effective reduced to the definitions, for callers
// that do not need provenance.
func EffectiveDefinitions(req Request) []*llmv1.ToolDefinition {
	entries := Effective(req)
	out := make([]*llmv1.ToolDefinition, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Def)
	}
	return out
}

// Allows reports whether name is in the effective set — the membership test
// dispatch uses to refuse a tool that was never advertised.
func Allows(req Request, name string) bool {
	want := strings.TrimSpace(name)
	if want == "" {
		return false
	}
	for _, e := range Effective(req) {
		if e.Name() == want {
			return true
		}
	}
	return false
}

// trimmedNameSet indexes defs by trimmed, non-empty name.
func trimmedNameSet(defs []*llmv1.ToolDefinition) map[string]struct{} {
	out := make(map[string]struct{}, len(defs))
	for _, d := range defs {
		if d == nil {
			continue
		}
		n := strings.TrimSpace(d.GetName())
		if n == "" {
			continue
		}
		out[n] = struct{}{}
	}
	return out
}
