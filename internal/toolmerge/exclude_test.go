package toolmerge

import (
	"sort"
	"testing"

	"github.com/johnewart/pyxis/coretools"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/protobuf/proto"
)

func serverDef(name string) *llmv1.ToolDefinition {
	return &llmv1.ToolDefinition{
		Name:        name,
		Description: "server-wide definition",
		Execution: &llmv1.ToolDefinition_InlineBuiltin{
			InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{},
		},
	}
}

func mergedNames(defs []*llmv1.ToolDefinition) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.GetName())
	}
	return out
}

func hasName(defs []*llmv1.ToolDefinition, name string) bool {
	for _, d := range defs {
		if d.GetName() == name {
			return true
		}
	}
	return false
}

// (a) A server-wide tool named in disabled disappears; an un-named sibling
// stays.
func TestMergeDefinitionsExcluding_DisabledServerToolIsRemoved(t *testing.T) {
	server := []*llmv1.ToolDefinition{serverDef("probe_tool"), serverDef("read_code")}
	got := MergeDefinitionsExcluding(server, nil, map[string]struct{}{"probe_tool": {}})

	if hasName(got, "probe_tool") {
		t.Errorf("probe_tool survived exclusion; names = %v", mergedNames(got))
	}
	if !hasName(got, "read_code") {
		t.Errorf("read_code was dropped although not disabled; names = %v", mergedNames(got))
	}
	if len(got) != 1 {
		t.Errorf("len = %d, want 1; names = %v", len(got), mergedNames(got))
	}
}

// (b) DENY WINS: a disabled name is removed even when the agent registered it
// itself. This is the inverse of the original rule. Explicit per-session
// registration used to be treated as the more specific signal, which made the
// exclusion cosmetic — registering a session tool is reachable by any client,
// so anything blocked could be unblocked by naming it.
func TestMergeDefinitionsExcluding_AgentRegisteredNameIsStillDenied(t *testing.T) {
	server := []*llmv1.ToolDefinition{serverDef("probe_tool"), serverDef("read_code")}
	agent := []*llmv1.ToolDefinition{webhookDef("probe_tool")}
	got := MergeDefinitionsExcluding(server, agent, map[string]struct{}{"probe_tool": {}})

	if hasName(got, "probe_tool") {
		t.Errorf("probe_tool survived exclusion because the agent registered it; names = %v", mergedNames(got))
	}
	if !hasName(got, "read_code") {
		t.Errorf("read_code missing; names = %v", mergedNames(got))
	}
}

// (c) Guaranteed core tools can never be excluded, even when every core tool
// is named in disabled. ask_user, the optional one, goes.
func TestMergeDefinitionsExcluding_GuaranteedCoreToolsAreExempt(t *testing.T) {
	disabled := make(map[string]struct{}, coretools.Count())
	server := make([]*llmv1.ToolDefinition, 0, coretools.Count())
	for _, name := range coretools.Names() {
		disabled[name] = struct{}{}
		server = append(server, inlineBuiltinDef(name))
	}
	got := MergeDefinitionsExcluding(server, nil, disabled)

	if len(got) != coretools.Count()-1 {
		t.Errorf("len = %d, want %d (every guaranteed core tool must survive, ask_user must not); names = %v", len(got), coretools.Count()-1, mergedNames(got))
	}
	if hasName(got, coretools.AskUser) {
		t.Errorf("disabled ask_user survived; names = %v", mergedNames(got))
	}
	for _, name := range coretools.Names() {
		if !coretools.IsGuaranteed(name) {
			continue
		}
		t.Run(name, func(t *testing.T) {
			d := defByName(t, got, name)
			if d.GetInlineBuiltin() == nil {
				t.Errorf("%s: execution = %T, want inline builtin", name, d.GetExecution())
			}
		})
	}
}

// ask_user is on by default: no policy hides it, only an explicit disable.
// Under ProfileOnly a session that never registered it still has it, and
// Allows (what dispatch checks) agrees with the advertised set either way.
func TestEffective_AskUserIsOnByDefaultAndDisableable(t *testing.T) {
	server := []*llmv1.ToolDefinition{inlineBuiltinDef(coretools.AskUser), inlineBuiltinDef(coretools.SetStatus), serverDef("read_code")}
	for _, policy := range []Policy{InheritServerWide, ProfileOnly} {
		req := Request{ServerWide: server, Policy: policy}
		if !Allows(req, coretools.AskUser) {
			t.Errorf("policy %v: ask_user not offered by default", policy)
		}
		req.Disabled = map[string]struct{}{coretools.AskUser: {}, coretools.SetStatus: {}}
		if Allows(req, coretools.AskUser) {
			t.Errorf("policy %v: disabled ask_user is still allowed", policy)
		}
		if !Allows(req, coretools.SetStatus) {
			t.Errorf("policy %v: set_status was hidden by disabled", policy)
		}
		// Deny wins over the session registering it itself.
		req.Session = []*llmv1.ToolDefinition{webhookDef(coretools.AskUser)}
		if Allows(req, coretools.AskUser) {
			t.Errorf("policy %v: registering ask_user lifted the disable", policy)
		}
	}
}

// (d) A nil or empty disabled set is a no-op: the result is element-wise
// equal to MergeDefinitions.
func TestMergeDefinitionsExcluding_NilOrEmptyDisabledMatchesMergeDefinitions(t *testing.T) {
	server := []*llmv1.ToolDefinition{
		serverDef("probe_tool"),
		serverDef("read_code"),
		inlineBuiltinDef(coretools.SetStatus),
	}
	agent := []*llmv1.ToolDefinition{webhookDef("agent_only"), webhookDef("probe_tool")}
	want := MergeDefinitions(server, agent)

	for label, disabled := range map[string]map[string]struct{}{
		"nil":   nil,
		"empty": {},
	} {
		t.Run(label, func(t *testing.T) {
			got := MergeDefinitionsExcluding(server, agent, disabled)
			if len(got) != len(want) {
				t.Fatalf("len = %d, want %d; got %v want %v", len(got), len(want), mergedNames(got), mergedNames(want))
			}
			for i := range want {
				if !proto.Equal(got[i], want[i]) {
					t.Errorf("index %d: got %v, want %v", i, got[i], want[i])
				}
			}
		})
	}
}

// (e) Filtering preserves MergeDefinitions' name order: survivors are strictly
// ascending.
func TestMergeDefinitionsExcluding_OutputStaysSorted(t *testing.T) {
	server := []*llmv1.ToolDefinition{
		serverDef("zeta"),
		serverDef("alpha"),
		serverDef("mid_tool"),
		serverDef("probe_tool"),
		serverDef("beta"),
	}
	got := MergeDefinitionsExcluding(server, nil, map[string]struct{}{"mid_tool": {}})

	names := mergedNames(got)
	if !sort.StringsAreSorted(names) {
		t.Errorf("survivors not sorted: %v", names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("names not strictly ascending at %d: %v", i, names)
		}
	}
	if hasName(got, "mid_tool") {
		t.Errorf("mid_tool survived exclusion: %v", names)
	}
	if len(got) != 4 {
		t.Errorf("len = %d, want 4: %v", len(got), names)
	}
}
