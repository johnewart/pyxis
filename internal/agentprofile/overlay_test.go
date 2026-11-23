package agentprofile

import (
	"context"
	"strings"
	"testing"

	"github.com/johnewart/pyxis/coretools"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"google.golang.org/protobuf/proto"
)

func overlayBase() *Profile {
	return &Profile{
		ID: "plugin-editor", Name: "Editor", Static: true,
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "role", Content: "You review."},
			{Id: "style", Content: "Run the style check."},
		},
		DefaultModel: "base-model",
		ToolMapping: []ToolMapping{
			{ToolName: "review", Enabled: true},
			{ToolName: "check_chapter_style", Enabled: true},
		},
	}
}

func hasField(p *Profile, f llmv1.AgentProfileOverlayField) bool {
	for _, c := range p.Customized {
		if c == f {
			return true
		}
	}
	return false
}

func TestApplyOverlayNilReturnsBaseUncustomized(t *testing.T) {
	m, err := ApplyOverlay(overlayBase(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Customized) != 0 || m.DefaultModel != "base-model" || !m.Static {
		t.Errorf("merged = %+v", m)
	}
}

func TestApplyOverlayMergeRules(t *testing.T) {
	base := overlayBase()
	ov := &llmv1.AgentProfileOverlay{
		BaseId:        "plugin-editor",
		DefaultModel:  proto.String("my-model"),
		MemoryEnabled: proto.Bool(true),
		ToolOverrides: []*llmv1.ToolMapping{
			{ToolName: "check_chapter_style", Enabled: false},
			{ToolName: "web_search", Enabled: true},
		},
		SectionReplacements: []*llmv1.SystemPromptSection{
			{Id: "style", Content: "Only flag; never suggest."},
			{Id: "gone", Content: "ignored: the base has no such section"},
		},
		ExtraSections: []*llmv1.SystemPromptSection{{Id: "mine", Content: "Use British spelling."}},
	}
	m, err := ApplyOverlay(base, ov)
	if err != nil {
		t.Fatal(err)
	}
	if m.DefaultModel != "my-model" || !m.MemoryEnabled || m.LlmProviderID != "" {
		t.Errorf("scalars = model %q memory %v provider %q", m.DefaultModel, m.MemoryEnabled, m.LlmProviderID)
	}
	tools := map[string]bool{}
	for _, tm := range m.ToolMapping {
		tools[tm.ToolName] = tm.Enabled
	}
	if !tools["review"] || tools["check_chapter_style"] || !tools["web_search"] || len(tools) != 3 {
		t.Errorf("tools = %v", tools)
	}
	var ids, contents []string
	for _, s := range m.SystemPromptSections {
		ids = append(ids, s.GetId())
		contents = append(contents, s.GetContent())
	}
	if strings.Join(ids, ",") != "role,style,mine" || contents[1] != "Only flag; never suggest." {
		t.Errorf("sections = %v %v", ids, contents)
	}
	for _, f := range []llmv1.AgentProfileOverlayField{
		llmv1.AgentProfileOverlayField_AGENT_PROFILE_OVERLAY_FIELD_DEFAULT_MODEL,
		llmv1.AgentProfileOverlayField_AGENT_PROFILE_OVERLAY_FIELD_MEMORY_ENABLED,
		llmv1.AgentProfileOverlayField_AGENT_PROFILE_OVERLAY_FIELD_TOOLS,
		llmv1.AgentProfileOverlayField_AGENT_PROFILE_OVERLAY_FIELD_SYSTEM_PROMPT,
	} {
		if !hasField(m, f) {
			t.Errorf("customized missing %v", f)
		}
	}
	if hasField(m, llmv1.AgentProfileOverlayField_AGENT_PROFILE_OVERLAY_FIELD_LLM_PROVIDER) {
		t.Error("provider marked customized but overlay did not set it")
	}
	// The base is untouched.
	if base.DefaultModel != "base-model" || len(base.ToolMapping) != 2 || base.SystemPromptSections[1].GetContent() != "Run the style check." {
		t.Errorf("base modified: %+v", base)
	}
}

// A tool the base gains after the overlay was written keeps the base's
// setting: the overlay only pins the tools it names.
func TestApplyOverlayBaseUpgradeAddsToolsAndKeepsUserChanges(t *testing.T) {
	ov := &llmv1.AgentProfileOverlay{
		ToolOverrides: []*llmv1.ToolMapping{{ToolName: "review", Enabled: false}},
		ExtraSections: []*llmv1.SystemPromptSection{{Id: "mine", Content: "x"}},
	}
	upgraded := overlayBase()
	upgraded.ToolMapping = append(upgraded.ToolMapping, ToolMapping{ToolName: "new_tool", Enabled: true})
	upgraded.SystemPromptSections[0].Content = "You review, v2."
	m, err := ApplyOverlay(upgraded, ov)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]bool{}
	for _, tm := range m.ToolMapping {
		tools[tm.ToolName] = tm.Enabled
	}
	if tools["review"] || !tools["new_tool"] {
		t.Errorf("tools = %v", tools)
	}
	if m.SystemPromptSections[0].GetContent() != "You review, v2." || m.SystemPromptSections[len(m.SystemPromptSections)-1].GetId() != "mine" {
		t.Errorf("sections = %v", m.SystemPromptSections)
	}
}

func TestApplyOverlayRejects(t *testing.T) {
	for name, c := range map[string]struct {
		ov   *llmv1.AgentProfileOverlay
		want string
	}{
		"other base":      {&llmv1.AgentProfileOverlay{BaseId: "other"}, "applied to"},
		"extra collides":  {&llmv1.AgentProfileOverlay{ExtraSections: []*llmv1.SystemPromptSection{{Id: "style", Content: "x"}}}, "already a section"},
		"extra no id":     {&llmv1.AgentProfileOverlay{ExtraSections: []*llmv1.SystemPromptSection{{Content: "x"}}}, "no id"},
		"empty tool name": {&llmv1.AgentProfileOverlay{ToolOverrides: []*llmv1.ToolMapping{{ToolName: " "}}}, "empty tool name"},
		"core tool off":   {&llmv1.AgentProfileOverlay{ToolOverrides: []*llmv1.ToolMapping{{ToolName: coreToolForTest(t), Enabled: false}}}, "core tool"},
	} {
		if _, err := ApplyOverlay(overlayBase(), c.ov); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

// coreToolForTest returns a core tool that can never be disabled.
func coreToolForTest(t *testing.T) string {
	t.Helper()
	for _, n := range coretools.Names() {
		if coretools.IsGuaranteed(n) {
			return n
		}
	}
	t.Fatal("no guaranteed core tool")
	return ""
}

type fakeOverlays map[string]*llmv1.AgentProfileOverlay

func (f fakeOverlays) Get(_ context.Context, _ serverutil.Scope, id string) (*llmv1.AgentProfileOverlay, error) {
	return f[id], nil
}

func (f fakeOverlays) List(context.Context, serverutil.Scope) ([]*llmv1.AgentProfileOverlay, error) {
	var out []*llmv1.AgentProfileOverlay
	for id, ov := range f {
		c := proto.Clone(ov).(*llmv1.AgentProfileOverlay)
		c.BaseId = id
		out = append(out, c)
	}
	return out, nil
}

func TestResolverAppliesOverlayInWorkspaceOnly(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(*overlayBase()); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(reg, nil)
	r.Overlays = fakeOverlays{"plugin-editor": {DefaultModel: proto.String("my-model")}}
	ctx := context.Background()
	ws := serverutil.Scope{TenantID: "ws-1"}

	got, err := r.Get(ctx, ws, "plugin-editor")
	if err != nil || got.DefaultModel != "my-model" || !got.Static {
		t.Fatalf("Get in workspace = %+v, %v", got, err)
	}
	list, err := r.List(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range list {
		if p.ID == "plugin-editor" {
			found = p.DefaultModel == "my-model"
		}
	}
	if !found {
		t.Error("List did not apply the overlay")
	}
	// Without a workspace there is no overlay to apply.
	if got, _ := r.Get(ctx, serverutil.Scope{}, "plugin-editor"); got.DefaultModel != "base-model" {
		t.Errorf("Get without workspace = %q", got.DefaultModel)
	}
	if b, err := r.Base("plugin-editor"); err != nil || b.DefaultModel != "base-model" {
		t.Errorf("Base = %+v, %v", b, err)
	}
	if _, err := r.Base("nope"); err == nil {
		t.Error("Base of unknown id succeeded")
	}
}
