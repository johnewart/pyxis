package toolmerge

import (
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/coretools"
)

// IsMutationTool reports whether def may mutate WORKSPACE state.
// Unspecified classification is treated as mutation (safe default).
//
// llmd's own core session tools are never workspace mutations, whatever their
// classification says. add_todo / update_todo / set_status / set_session_title
// are classified MUTATION because they write session bookkeeping rows (todo
// list, title) — not because they touch anything a read-only agent is being
// kept away from. Read-only is about the user's workspace; a read-only agent
// that cannot report its status, keep a todo list or ask a question is just a
// broken agent.
//
// This predicate, not FilterForReadOnly, is where the exemption lives: it also
// gates dispatch-time enforcement (internal/toolexec/exec.go,
// internal/server/server.go's InvokeTool, internal/mcpserver's tools/call), so
// exempting only the advertisement path would advertise a core tool to a
// read-only agent and then refuse the call.
func IsMutationTool(def *llmv1.ToolDefinition) bool {
	if def == nil {
		return true
	}
	if coretools.IsCore(strings.TrimSpace(def.GetName())) {
		return false
	}
	switch def.GetClassification() {
	case llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY:
		return false
	case llmv1.ToolClassification_TOOL_CLASSIFICATION_MUTATION:
		return true
	default:
		return true
	}
}

// FilterForReadOnly returns only query-classified tools from defs.
func FilterForReadOnly(defs []*llmv1.ToolDefinition) []*llmv1.ToolDefinition {
	if len(defs) == 0 {
		return nil
	}
	out := make([]*llmv1.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		if d == nil {
			continue
		}
		if !IsMutationTool(d) {
			out = append(out, d)
		}
	}
	return out
}
