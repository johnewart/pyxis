package store

import "testing"

func TestParseTodos_EmptyAndBlank(t *testing.T) {
	if got := ParseTodos(""); got != nil {
		t.Fatalf("ParseTodos(\"\") = %#v, want nil", got)
	}
	if got := ParseTodos("   "); got != nil {
		t.Fatalf("ParseTodos(whitespace) = %#v, want nil", got)
	}
}

func TestParseTodos_Malformed(t *testing.T) {
	if got := ParseTodos("{not json"); got != nil {
		t.Fatalf("ParseTodos(malformed) = %#v, want nil", got)
	}
}

func TestMarshalParseTodos_RoundTrip(t *testing.T) {
	items := []TodoItem{
		{ID: "1", Description: "look for a fish", Status: TodoPending, CreatedTurnSeq: 3},
		{ID: "2", Description: "contemplate the universe", Status: TodoInProgress, CreatedTurnSeq: 3, StartedTurnSeq: 4},
	}
	wire := MarshalTodos(items)
	if wire == "" || wire == "null" {
		t.Fatalf("MarshalTodos produced %q", wire)
	}
	got := ParseTodos(wire)
	if len(got) != 2 {
		t.Fatalf("round trip: got %d items, want 2 (%#v)", len(got), got)
	}
	if got[0] != items[0] || got[1] != items[1] {
		t.Fatalf("round trip mismatch: got %#v, want %#v", got, items)
	}
}

func TestMarshalTodos_EmptyIsBracketArray(t *testing.T) {
	if got := MarshalTodos(nil); got != "[]" {
		t.Fatalf("MarshalTodos(nil) = %q, want \"[]\"", got)
	}
	if got := MarshalTodos([]TodoItem{}); got != "[]" {
		t.Fatalf("MarshalTodos(empty) = %q, want \"[]\"", got)
	}
}
