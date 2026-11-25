package toolexec

import (
	"sort"
	"testing"

	"github.com/johnewart/pyxis/coretools"
)

// The canonical set and the registration table must agree in BOTH directions.
// A name in coretools that RegisterSessionTools never registers would be
// promised to every agent and protected from shadowing while not existing;
// a tool registered here but absent from coretools would be shadowable by an
// agent despite being llmd's own.
func TestRegisterSessionTools_RegistersExactlyTheCoreSet(t *testing.T) {
	reg, handlers, _ := newSessionToolsHarness(t)

	var got []string
	for _, d := range reg.Snapshot() {
		got = append(got, d.GetName())
	}
	want := coretools.Names()
	sort.Strings(got)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("registered tools = %v, want exactly the core set %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("registered tools = %v, want %v", got, want)
		}
	}
	// Advertised and executable must come as a pair: a definition with no
	// handler is a tool the model will call and get "unknown tool" back from.
	for _, name := range coretools.Names() {
		if _, ok := handlers.Lookup(name); !ok {
			t.Errorf("core tool %q is registered but has no handler", name)
		}
	}
}

// Every core tool must be an inline builtin. Core tools are core precisely
// because llmd implements them in-process; a delegated or webhook execution
// would mean llmd is not the one running them.
func TestCoreTools_AreAllInlineBuiltins(t *testing.T) {
	reg, _, _ := newSessionToolsHarness(t)

	for _, d := range reg.Snapshot() {
		if !coretools.IsCore(d.GetName()) {
			continue
		}
		if d.GetInlineBuiltin() == nil {
			t.Errorf("core tool %q execution = %T, want inline builtin", d.GetName(), d.GetExecution())
		}
	}
}

// Core tools are advertised to the model, so each needs a description and a
// parameters schema — an unnamed blob is not callable in practice.
func TestCoreTools_AllHaveDescriptionAndSchema(t *testing.T) {
	reg, _, _ := newSessionToolsHarness(t)

	for _, d := range reg.Snapshot() {
		if !coretools.IsCore(d.GetName()) {
			continue
		}
		if d.GetDescription() == "" {
			t.Errorf("core tool %q has no description", d.GetName())
		}
		if d.GetParametersSchemaJson() == "" {
			t.Errorf("core tool %q has no parameters schema", d.GetName())
		}
	}
}

func TestCoreTools_NamesIsACopy(t *testing.T) {
	first := coretools.Names()
	first[0] = "mutated"
	if coretools.Names()[0] == "mutated" {
		t.Fatal("coretools.Names() exposed the canonical slice; a caller can corrupt the core set")
	}
}

func TestCoreTools_CountMatchesNames(t *testing.T) {
	if coretools.Count() != len(coretools.Names()) {
		t.Fatalf("Count() = %d, len(Names()) = %d", coretools.Count(), len(coretools.Names()))
	}
}

func TestCoreTools_IsCoreRejectsUnknownAndBlank(t *testing.T) {
	for _, name := range []string{"", "read_code", "traverse_graph", "set_status_v2"} {
		if coretools.IsCore(name) {
			t.Errorf("IsCore(%q) = true, want false", name)
		}
	}
	for _, name := range coretools.Names() {
		if !coretools.IsCore(name) {
			t.Errorf("IsCore(%q) = false, want true", name)
		}
	}
}
