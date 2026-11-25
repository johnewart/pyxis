package toolmerge

import (
	"sort"
	"strings"

	"github.com/johnewart/pyxis/coretools"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/protobuf/proto"
)

// MergeDefinitions merges server-wide and per-agent tool definitions. Agent entries override
// server entries with the same name — EXCEPT for llmd's core tools (see
// internal/coretools), where the server definition always wins.
//
// Core tools are un-shadowable because llmd implements them itself and the
// session depends on them behaving as llmd expects: an agent that registered
// its own "set_status" or "ask_user" would replace the inline builtin with a
// delegated/webhook definition, and llmd's frame emission, ask-card
// persistence and forced turn end would silently stop happening. Shadowing
// them was never a feature anyone asked for; it was just what an unconditional
// override did.
//
// The result is sorted by tool name. Full protobuf (including execution
// bindings) is preserved.
func MergeDefinitions(server, agent []*llmv1.ToolDefinition) []*llmv1.ToolDefinition {
	by := make(map[string]*llmv1.ToolDefinition)
	fromServer := make(map[string]struct{})
	for _, t := range server {
		if t == nil {
			continue
		}
		n := strings.TrimSpace(t.GetName())
		if n == "" {
			continue
		}
		by[n] = proto.Clone(t).(*llmv1.ToolDefinition)
		by[n].Name = n
		fromServer[n] = struct{}{}
	}
	for _, t := range agent {
		if t == nil {
			continue
		}
		n := strings.TrimSpace(t.GetName())
		if n == "" {
			continue
		}
		// Only protect a core name llmd actually registered: if core
		// registration were missing, dropping the agent's copy too would leave
		// the tool advertised by nobody, which is strictly worse.
		if _, served := fromServer[n]; served && coretools.IsCore(n) {
			continue
		}
		by[n] = proto.Clone(t).(*llmv1.ToolDefinition)
		by[n].Name = n
	}
	names := make([]string, 0, len(by))
	for n := range by {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*llmv1.ToolDefinition, 0, len(names))
	for _, n := range names {
		out = append(out, by[n])
	}
	return out
}

// MergeDefinitionsExcluding is MergeDefinitions followed by the per-session
// exclusion: an entry of the merged set is dropped when its trimmed name is a
// key of disabled and it is NOT one of llmd's guaranteed core tools
// (coretools.IsGuaranteed: every core tool but ask_user).
//
// DENY WINS. This used to exempt tools the session had registered itself, on
// the theory that explicit per-session registration was the more specific
// signal. That made the exclusion cosmetic: registering a session tool is
// reachable by any client, so a client could unblock anything it had been
// blocked from simply by naming it — which is not a control. Exclusion is now
// applied last and unconditionally.
//
// Guaranteed core tools remain exempt: a session that cannot set_status or
// keep its todo list is simply a broken session, so no configuration is
// allowed to hide them. ask_user is core but optional: a host whose runner
// cannot wait for an answer may disable it.
//
// Keys of disabled are looked up as given — callers supply trimmed names (the
// store codec trims and de-duplicates before persisting). A nil or empty
// disabled returns exactly what MergeDefinitions returns. Output order is the
// name-sorted order of MergeDefinitions.
//
// This is the InheritServerWide special case of Effective, kept as a named
// wrapper for callers with no policy of their own. Prefer Effective (or
// EffectiveDefinitions) when a session may carry a Policy, and note that
// advertisement alone is not enforcement: dispatch must resolve through the
// same result or a hidden tool stays callable.
func MergeDefinitionsExcluding(server, agent []*llmv1.ToolDefinition, disabled map[string]struct{}) []*llmv1.ToolDefinition {
	return EffectiveDefinitions(Request{
		ServerWide: server,
		Session:    agent,
		Disabled:   disabled,
		Policy:     InheritServerWide,
	})
}

// StripForProvider returns copies with only model-facing fields (name, description, JSON schema).
// Provider SDKs must not receive webhook URLs or secrets.
func StripForProvider(defs []*llmv1.ToolDefinition) []*llmv1.ToolDefinition {
	if len(defs) == 0 {
		return nil
	}
	out := make([]*llmv1.ToolDefinition, 0, len(defs))
	for _, t := range defs {
		if t == nil {
			continue
		}
		out = append(out, &llmv1.ToolDefinition{
			Name:                 strings.TrimSpace(t.GetName()),
			Description:          t.GetDescription(),
			ParametersSchemaJson: t.GetParametersSchemaJson(),
			Classification:       t.GetClassification(),
		})
	}
	return out
}

// RedactSecretsForList returns clones safe to return from ListTools (removes bearer token and HMAC secret).
func RedactSecretsForList(defs []*llmv1.ToolDefinition) []*llmv1.ToolDefinition {
	if len(defs) == 0 {
		return nil
	}
	out := make([]*llmv1.ToolDefinition, 0, len(defs))
	for _, t := range defs {
		if t == nil {
			continue
		}
		cp := proto.Clone(t).(*llmv1.ToolDefinition)
		if wh := cp.GetHttpWebhook(); wh != nil {
			wh.BearerToken = nil
			wh.HmacSecret = nil
		}
		out = append(out, cp)
	}
	return out
}
