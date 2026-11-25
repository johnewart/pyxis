// Package coretools names the tools llmd itself implements and offers to
// every agent.
//
// These are all SESSION-INTERACTION tools — todos, status headlines, session
// titles, asking the user a question. They are not product tools: read_code,
// traverse_graph, search and friends belong to host subsystems that register
// themselves through toolseam. The distinction matters because it decides the
// guarantee: a product tool is optional and deployment-specific, whereas an
// agent with no way to set its status or keep its todo list is broken no
// matter which host it runs under. Asking the user is offered by default but
// is optional (IsGuaranteed): it only helps where someone can answer.
//
// This package is the SINGLE source of these names. It is deliberately a leaf
// with no imports: both internal/toolexec (which registers them) and
// internal/toolmerge (which protects them from being shadowed) depend on it,
// and toolexec already imports toolmerge, so the set cannot live in either
// without an import cycle.
//
// Adding a core tool means adding it here AND registering it in
// toolexec.RegisterSessionTools; the two are pinned against each other by
// TestRegisterSessionTools_RegistersExactlyTheCoreSet, so drift is a test
// failure rather than a tool that silently never reaches agents.
package coretools

// Core tool names. Every agent gets all of these by default, on every
// transport; all but ask_user are guaranteed (see IsGuaranteed).
const (
	AddTodo         = "add_todo"
	UpdateTodo      = "update_todo"
	ListTodos       = "list_todos"
	SetStatus       = "set_status"
	SetSessionTitle = "set_session_title"
	AskUser         = "ask_user"
)

// names is the canonical set, in registration order.
var names = []string{
	AddTodo,
	UpdateTodo,
	ListTodos,
	SetStatus,
	SetSessionTitle,
	AskUser,
}

// Names returns the canonical core tool names. The returned slice is a copy:
// callers (tests especially) must not be able to mutate the canonical set.
func Names() []string {
	out := make([]string, len(names))
	copy(out, names)
	return out
}

// Count is how many tools every agent must be offered.
func Count() int { return len(names) }

// byName is the membership index, built once.
var byName = func() map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}()

// IsCore reports whether name is one of llmd's own core tools. Used by
// toolmerge to keep an agent-registered definition from shadowing llmd's
// implementation.
func IsCore(name string) bool {
	_, ok := byName[name]
	return ok
}

// optional are the core tools a host may switch off for a session (its
// profile's disabled mapping, or its disabled_tools). Every agent still gets
// them by default, tool policy notwithstanding.
//
// ask_user is the one: unlike the bookkeeping tools it is a channel to a
// person. It ends the turn and only helps when someone can answer and the
// agent resumes on the answer. An agent run where nobody can (a background
// job whose runner does not wait for answers) is better off without it: it
// reports and stops instead of leaving a question nobody will see.
var optional = map[string]struct{}{
	AskUser: {},
}

// IsGuaranteed reports whether name is a core tool no configuration may hide:
// every core tool except the optional ones. The bookkeeping tools (status,
// todos, title) are harmless anywhere and an agent without them is broken.
func IsGuaranteed(name string) bool {
	if !IsCore(name) {
		return false
	}
	_, opt := optional[name]
	return !opt
}
