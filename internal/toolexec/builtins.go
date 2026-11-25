package toolexec

// This file used to hold `builtins`, a table of inline-builtin tools core
// registered directly, plus RegisterBuiltins to install them. The table was
// empty: every tool in an assembled surface — read_code, write_code, search,
// enumerate, traverse_graph, the work-item and document families — is
// contributed by the host or by an external subsystem through the registrar
// seam (see toolseam's package doc and llmdtools.InstallTools). Iterating an
// empty slice and reporting success made it look as though core contributed
// tools it does not have, so both the table and RegisterBuiltins are gone.
//
// Session tools (add_todo, update_todo, list_todos, set_status,
// set_session_title, ask_user) ARE core-owned and are registered by
// RegisterSessionTools in session_tools.go.
