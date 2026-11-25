package store

import (
	"encoding/json"
	"strings"
)

// TodoStatus is the lifecycle state of one session todo item.
type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// TodoItem is one entry in a session's agent-authored todo list, persisted as
// a JSON array in SessionRow.TodosJSON (llmd_sessions.todos_json). Distinct
// from the client/UI-authored todos_json write path via SetSessionMeta: this
// is the shape the add_todo/update_todo/list_todos builtin tools read and
// write, so an agent's own task tracking survives a session restart and is
// visible to the client via GetSession without either side needing a second
// column.
//
// Turn tracking: CreatedTurnSeq/StartedTurnSeq/CompletedTurnSeq are the
// session's turn_seq (store.TurnRegister.Current) at the moment each
// transition happened. Zero means "not yet reached" (turn_seq is 1-based;
// see TurnRegister.Bootstrap/Advance).
type TodoItem struct {
	ID               string     `json:"id"`
	Description      string     `json:"description"`
	Status           TodoStatus `json:"status"`
	CreatedTurnSeq   int64      `json:"created_turn_seq"`
	StartedTurnSeq   int64      `json:"started_turn_seq,omitempty"`
	CompletedTurnSeq int64      `json:"completed_turn_seq,omitempty"`
}

// ParseTodos decodes a session's todos_json into a slice. Empty/blank input
// (never persisted, or a session with no todos yet) decodes to nil, not an
// error. A malformed blob (e.g. hand-edited via SetSessionMeta) also decodes
// to nil rather than erroring — callers that need to distinguish "no todos"
// from "corrupt todos" should check the input themselves; the todo tools
// treat both the same (start from an empty list).
func ParseTodos(todosJSON string) []TodoItem {
	s := strings.TrimSpace(todosJSON)
	if s == "" {
		return nil
	}
	var items []TodoItem
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return nil
	}
	return items
}

// MarshalTodos encodes a todo slice back to the wire/storage JSON shape.
// A nil/empty slice marshals to "[]", never "" or "null", so a downstream
// json.Unmarshal (ParseTodos, a UI reading todos_json directly) never has to
// special-case an empty list.
func MarshalTodos(items []TodoItem) string {
	if len(items) == 0 {
		return "[]"
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}
