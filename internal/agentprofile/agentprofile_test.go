package agentprofile

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnewart/pyxis/coretools"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
)

// Tests build their own NewRegistry() — never RegisterStatic/Default — so no
// state leaks across tests.

func inlineBuiltinDef(name string) *llmv1.ToolDefinition {
	return &llmv1.ToolDefinition{
		Name:        name,
		Description: "llmd's own implementation",
		Execution: &llmv1.ToolDefinition_InlineBuiltin{
			InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{},
		},
	}
}

func webhookDef(name string) *llmv1.ToolDefinition {
	return &llmv1.ToolDefinition{
		Name:        name,
		Description: "host-registered product tool",
		Execution: &llmv1.ToolDefinition_HttpWebhook{
			HttpWebhook: &llmv1.ToolExecutionHttpWebhook{Url: "https://host.test/hook"},
		},
	}
}

func defByName(t *testing.T, defs []*llmv1.ToolDefinition, name string) *llmv1.ToolDefinition {
	t.Helper()
	for _, d := range defs {
		if d.GetName() == name {
			return d
		}
	}
	t.Fatalf("tool %q not present in set", name)
	return nil
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func sampleProfile(id string) Profile {
	return Profile{
		ID:          id,
		Name:        "Sample " + id,
		Description: "a sample profile",
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "persona", Content: "You are helpful.", Tags: []string{"base"}},
		},
		LlmProviderID: "provider-a",
		DefaultModel:  "model-a",
		MemoryEnabled: true,
		ToolMapping: []ToolMapping{
			{ToolName: "read_code", Enabled: true},
			{ToolName: "search", Enabled: false},
		},
	}
}

// --- Validate -------------------------------------------------------------

func TestValidate_RejectsDisabledCoreTool(t *testing.T) {
	p := sampleProfile("x")
	p.ToolMapping = []ToolMapping{{ToolName: coretools.SetStatus, Enabled: false}}
	err := Validate(&p)
	if err == nil {
		t.Fatal("expected error for disabled core tool")
	}
	if !strings.Contains(err.Error(), coretools.SetStatus) || !strings.Contains(err.Error(), "core tools cannot be disabled") {
		t.Fatalf("error should name the tool and say core tools cannot be disabled: %v", err)
	}
}

// ask_user is core but optional: a profile for an agent nobody can answer
// may switch it off.
func TestValidate_AcceptsDisabledAskUser(t *testing.T) {
	p := sampleProfile("x")
	p.ToolMapping = []ToolMapping{{ToolName: coretools.AskUser, Enabled: false}}
	if err := Validate(&p); err != nil {
		t.Fatalf("a profile disabling ask_user should validate: %v", err)
	}
}

func TestValidate_AcceptsEnabledCoreTool(t *testing.T) {
	p := sampleProfile("x")
	p.ToolMapping = []ToolMapping{{ToolName: coretools.SetStatus, Enabled: true}}
	if err := Validate(&p); err != nil {
		t.Fatalf("enabled core tool should validate: %v", err)
	}
}

func TestValidate_AcceptsEveryCoreToolEnabled(t *testing.T) {
	p := sampleProfile("x")
	p.ToolMapping = nil
	for _, n := range coretools.Names() {
		p.ToolMapping = append(p.ToolMapping, ToolMapping{ToolName: n, Enabled: true})
	}
	if err := Validate(&p); err != nil {
		t.Fatalf("all core tools enabled should validate: %v", err)
	}
}

func TestValidate_RejectsDuplicateToolNames(t *testing.T) {
	p := sampleProfile("x")
	p.ToolMapping = []ToolMapping{
		{ToolName: "read_code", Enabled: true},
		{ToolName: " read_code ", Enabled: false},
	}
	err := Validate(&p)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestValidate_RejectsEmptyIDNameAndToolName(t *testing.T) {
	if err := Validate(nil); err == nil {
		t.Fatal("nil profile should be rejected")
	}
	p := sampleProfile("  ")
	if err := Validate(&p); err == nil {
		t.Fatal("empty id should be rejected")
	}
	p = sampleProfile("x")
	p.Name = " "
	if err := Validate(&p); err == nil {
		t.Fatal("empty name should be rejected")
	}
	p = sampleProfile("x")
	p.ToolMapping = []ToolMapping{{ToolName: "  ", Enabled: true}}
	if err := Validate(&p); err == nil {
		t.Fatal("empty tool name should be rejected")
	}
}

// --- Registry -------------------------------------------------------------

func TestRegistry_DuplicateIDInSameCallRejectedAtomically(t *testing.T) {
	r := NewRegistry()
	err := r.Register(sampleProfile("a"), sampleProfile("b"), sampleProfile("a"))
	if err == nil {
		t.Fatal("expected duplicate-id error")
	}
	if got := r.List(); len(got) != 0 {
		t.Fatalf("registry should be unchanged after failed Register, got %d entries", len(got))
	}
}

func TestRegistry_DuplicateIDAgainstExistingRejectedAtomically(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(sampleProfile("a")); err != nil {
		t.Fatal(err)
	}
	err := r.Register(sampleProfile("b"), sampleProfile(" a "))
	if err == nil {
		t.Fatal("expected duplicate-id error against existing registration")
	}
	got := r.List()
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("registry should still hold only %q, got %+v", "a", got)
	}
}

func TestRegistry_RegisterRunsValidate(t *testing.T) {
	r := NewRegistry()
	p := sampleProfile("a")
	p.ToolMapping = []ToolMapping{{ToolName: coretools.SetStatus, Enabled: false}}
	if err := r.Register(p); err == nil {
		t.Fatal("Register should run Validate and reject a disabled core tool")
	}
	if len(r.List()) != 0 {
		t.Fatal("failed Register must not register anything")
	}
}

func TestRegistry_GetReturnsStaticCopy(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(sampleProfile("a")); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get(" a ")
	if !ok {
		t.Fatal("expected to find profile a (trimmed lookup)")
	}
	if !got.Static {
		t.Fatal("registered profile should be marked Static")
	}
	got.Name = "mutated"
	got.ToolMapping[0].Enabled = false
	again, _ := r.Get("a")
	if again.Name == "mutated" || !again.ToolMapping[0].Enabled {
		t.Fatal("Get must return a copy; registry state was mutated through it")
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("unknown id should not be found")
	}
}

func TestRegistry_ListSortedByID(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(sampleProfile("zeta"), sampleProfile("alpha"), sampleProfile("mid")); err != nil {
		t.Fatal(err)
	}
	got := r.List()
	if len(got) != 3 || got[0].ID != "alpha" || got[1].ID != "mid" || got[2].ID != "zeta" {
		t.Fatalf("List should be sorted by ID, got %v", []string{got[0].ID, got[1].ID, got[2].ID})
	}
}

func TestRegistry_NilReceiverSafe(t *testing.T) {
	var r *Registry
	if _, ok := r.Get("a"); ok {
		t.Fatal("nil registry Get should report not found")
	}
	if got := r.List(); len(got) != 0 {
		t.Fatal("nil registry List should be empty")
	}
	if err := r.Register(sampleProfile("a")); err == nil {
		t.Fatal("nil registry Register should error")
	}
}

// --- Resolver -------------------------------------------------------------

type fakeDynamic struct {
	byID    map[string]*Profile
	listErr error
	getErr  error
	calls   int
}

func (f *fakeDynamic) Get(_ context.Context, _ serverutil.Scope, id string) (*Profile, error) {
	f.calls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	p, ok := f.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	return p, nil
}

func (f *fakeDynamic) List(_ context.Context, _ serverutil.Scope) ([]*Profile, error) {
	f.calls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]*Profile, 0, len(f.byID))
	for _, p := range f.byID {
		out = append(out, p)
	}
	return out, nil
}

func TestResolver_StaticWinsOverDynamic(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(sampleProfile("shared")); err != nil {
		t.Fatal(err)
	}
	dynShared := sampleProfile("shared")
	dynShared.Name = "dynamic impostor"
	fake := &fakeDynamic{byID: map[string]*Profile{"shared": &dynShared}}
	res := NewResolver(r, fake)

	got, err := res.Get(context.Background(), serverutil.Scope{TenantID: "ws-1"}, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Static || got.Name == "dynamic impostor" {
		t.Fatalf("static profile should win over dynamic with same id, got %+v", got)
	}
	if fake.calls != 0 {
		t.Fatal("dynamic store must not be consulted when static has the id")
	}
	if !res.IsStatic("shared") || res.IsStatic("other") {
		t.Fatal("IsStatic wrong")
	}
}

func TestResolver_FallsThroughToDynamic(t *testing.T) {
	r := NewRegistry()
	dynOnly := sampleProfile("dyn")
	fake := &fakeDynamic{byID: map[string]*Profile{"dyn": &dynOnly}}
	res := NewResolver(r, fake)

	got, err := res.Get(context.Background(), serverutil.Scope{TenantID: "ws-1"}, "dyn")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "dyn" || got.Static {
		t.Fatalf("expected dynamic profile, got %+v", got)
	}
}

func TestResolver_SkipsDynamicWithoutWorkspace(t *testing.T) {
	r := NewRegistry()
	dynOnly := sampleProfile("dyn")
	fake := &fakeDynamic{byID: map[string]*Profile{"dyn": &dynOnly}}
	res := NewResolver(r, fake)

	_, err := res.Get(context.Background(), serverutil.Scope{TenantID: "  "}, "dyn")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound without workspace, got %v", err)
	}
	if fake.calls != 0 {
		t.Fatal("dynamic store must not be consulted when workspaceID is empty")
	}
}

func TestResolver_UnknownIsErrNotFound(t *testing.T) {
	res := NewResolver(NewRegistry(), &fakeDynamic{byID: map[string]*Profile{}})
	_, err := res.Get(context.Background(), serverutil.Scope{TenantID: "ws-1"}, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected errors.Is(err, ErrNotFound), got %v", err)
	}
	// No dynamic wired at all.
	_, err = NewResolver(NewRegistry(), nil).Get(context.Background(), serverutil.Scope{TenantID: "ws-1"}, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound with nil dynamic, got %v", err)
	}
}

func TestResolver_DynamicErrorsPropagate(t *testing.T) {
	boom := errors.New("db down")
	res := NewResolver(NewRegistry(), &fakeDynamic{getErr: boom, listErr: boom})
	if _, err := res.Get(context.Background(), serverutil.Scope{TenantID: "ws-1"}, "x"); !errors.Is(err, boom) {
		t.Fatalf("Get should propagate dynamic error, got %v", err)
	}
	if _, err := res.List(context.Background(), serverutil.Scope{TenantID: "ws-1"}); !errors.Is(err, boom) {
		t.Fatalf("List should propagate dynamic error, got %v", err)
	}
}

func TestResolver_ListStaticThenDynamic(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(sampleProfile("b-static"), sampleProfile("a-static")); err != nil {
		t.Fatal(err)
	}
	dyn := sampleProfile("dyn")
	shadowed := sampleProfile("a-static")
	fake := &fakeDynamic{byID: map[string]*Profile{"dyn": &dyn, "a-static": &shadowed}}
	res := NewResolver(r, fake)

	got, err := res.List(context.Background(), serverutil.Scope{TenantID: "ws-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "a-static" || got[1].ID != "b-static" || got[2].ID != "dyn" {
		ids := make([]string, 0, len(got))
		for _, p := range got {
			ids = append(ids, p.ID)
		}
		t.Fatalf("expected static (sorted) then dynamic, shadowed omitted; got %v", ids)
	}
}

func TestNewResolver_NilStaticFallsBackToDefault(t *testing.T) {
	res := NewResolver(nil, nil)
	if res.Static != Default {
		t.Fatal("nil static registry should fall back to Default")
	}
}

// --- BuildEnsureSessionRequest -------------------------------------------

func knownTools() []*llmv1.ToolDefinition {
	return []*llmv1.ToolDefinition{
		inlineBuiltinDef(coretools.SetStatus),
		webhookDef("read_code"),
		webhookDef("search"),
	}
}

func TestBuild_OverridesWinAndNilFallsBack(t *testing.T) {
	p := sampleProfile("x")
	prov := "provider-override"
	req, err := BuildEnsureSessionRequest(&p, SessionOverrides{
		SessionID:     "s1",
		LlmProviderID: &prov,
		DefaultModel:  nil,
	}, knownTools())
	if err != nil {
		t.Fatal(err)
	}
	if req.GetLlmProviderId() != "provider-override" {
		t.Fatalf("override provider should win, got %q", req.GetLlmProviderId())
	}
	if req.GetDefaultModel() != "model-a" {
		t.Fatalf("nil model override should fall back to profile, got %q", req.GetDefaultModel())
	}
	if req.GetSessionId() != "s1" {
		t.Fatalf("session id not set: %q", req.GetSessionId())
	}
	if req.DisplayName != nil || req.ChatSurface != nil {
		t.Fatal("empty display name / surface must stay unset (nil)")
	}
}

func TestBuild_EmptyOverrideFallsBackAndEmptyResultStaysNil(t *testing.T) {
	p := sampleProfile("x")
	p.DefaultModel = ""
	empty := "  "
	req, err := BuildEnsureSessionRequest(&p, SessionOverrides{
		SessionID:     "s1",
		LlmProviderID: &empty,
		DefaultModel:  &empty,
		DisplayName:   "Builder",
		ChatSurface:   "build",
	}, knownTools())
	if err != nil {
		t.Fatal(err)
	}
	if req.GetLlmProviderId() != "provider-a" {
		t.Fatalf("blank override should fall back to profile provider, got %q", req.GetLlmProviderId())
	}
	if req.DefaultModel != nil {
		t.Fatalf("no model anywhere should leave DefaultModel nil, got %q", req.GetDefaultModel())
	}
	if req.GetDisplayName() != "Builder" || req.GetChatSurface() != "build" {
		t.Fatal("display name / chat surface not propagated")
	}
}

func TestBuild_UnknownToolNamesToolAndProfile(t *testing.T) {
	p := sampleProfile("prof-42")
	p.ToolMapping = []ToolMapping{{ToolName: "ghost_tool", Enabled: true}}
	_, err := BuildEnsureSessionRequest(&p, SessionOverrides{SessionID: "s1"}, knownTools())
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "ghost_tool") || !strings.Contains(err.Error(), "prof-42") {
		t.Fatalf("error should mention tool name and profile id: %v", err)
	}
}

func TestBuild_DisabledLandsInDisabledToolsOnly(t *testing.T) {
	p := sampleProfile("x")
	req, err := BuildEnsureSessionRequest(&p, SessionOverrides{SessionID: "s1"}, knownTools())
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(req.GetDisabledTools(), "search") {
		t.Fatalf("search should be in DisabledTools, got %v", req.GetDisabledTools())
	}
	for _, d := range req.GetInitialTools() {
		if d.GetName() == "search" {
			t.Fatal("disabled tool must not be in InitialTools")
		}
	}
	if hasName(req.GetDisabledTools(), "read_code") {
		t.Fatal("enabled tool must not be in DisabledTools")
	}
}

func TestBuild_EnabledClonesKnownDef(t *testing.T) {
	p := sampleProfile("x")
	known := knownTools()
	req, err := BuildEnsureSessionRequest(&p, SessionOverrides{SessionID: "s1"}, known)
	if err != nil {
		t.Fatal(err)
	}
	got := defByName(t, req.GetInitialTools(), "read_code")
	orig := defByName(t, known, "read_code")
	if got == orig {
		t.Fatal("InitialTools entry must be a clone, not the known pointer")
	}
	if got.GetHttpWebhook().GetUrl() != orig.GetHttpWebhook().GetUrl() {
		t.Fatal("clone should preserve execution binding")
	}
}

func TestBuild_AdditionalToolsAppended(t *testing.T) {
	p := sampleProfile("x")
	extra := webhookDef("extra_tool")
	req, err := BuildEnsureSessionRequest(&p, SessionOverrides{
		SessionID:       "s1",
		AdditionalTools: []*llmv1.ToolDefinition{extra, nil},
	}, knownTools())
	if err != nil {
		t.Fatal(err)
	}
	got := defByName(t, req.GetInitialTools(), "extra_tool")
	if got == extra {
		t.Fatal("additional tool must be cloned")
	}
	if len(req.GetInitialTools()) != 2 { // read_code + extra_tool
		t.Fatalf("expected 2 initial tools, got %d", len(req.GetInitialTools()))
	}
}

func TestBuild_SectionsClonedAndSessionIDRequired(t *testing.T) {
	p := sampleProfile("x")
	req, err := BuildEnsureSessionRequest(&p, SessionOverrides{SessionID: "s1"}, knownTools())
	if err != nil {
		t.Fatal(err)
	}
	// [identity, persona]
	if len(req.GetSystemPromptSections()) != 2 || req.GetSystemPromptSections()[1] == p.SystemPromptSections[0] {
		t.Fatal("sections must be deep-cloned onto the request")
	}
	if req.GetSystemPromptSections()[1].GetContent() != "You are helpful." {
		t.Fatal("section content lost in clone")
	}
	if _, err := BuildEnsureSessionRequest(&p, SessionOverrides{SessionID: " "}, knownTools()); err == nil {
		t.Fatal("blank session id must be an error")
	}
	if _, err := BuildEnsureSessionRequest(nil, SessionOverrides{SessionID: "s1"}, knownTools()); err == nil {
		t.Fatal("nil profile must be an error")
	}
}

// --- ToProto --------------------------------------------------------------

func TestToProto_RoundTripsStaticProfile(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(sampleProfile("x")); err != nil {
		t.Fatal(err)
	}
	p, _ := r.Get("x")
	pb := p.ToProto()

	if pb.GetId() != "x" || pb.GetName() != "Sample x" || pb.GetDescription() != "a sample profile" {
		t.Fatalf("identity fields wrong: %+v", pb)
	}
	if !pb.GetIsStatic() {
		t.Fatal("IsStatic should be true for a registry profile")
	}
	if pb.GetCreatedAt() != "" || pb.GetUpdatedAt() != "" {
		t.Fatal("static profile should emit empty timestamps")
	}
	if pb.GetLlmProviderId() != "provider-a" || pb.GetDefaultModel() != "model-a" || !pb.GetMemoryEnabled() {
		t.Fatalf("provider/model/memory wrong: %+v", pb)
	}
	if len(pb.GetSystemPromptSections()) != 1 || pb.GetSystemPromptSections()[0] == p.SystemPromptSections[0] {
		t.Fatal("sections must be cloned onto the proto")
	}
	if pb.GetSystemPromptSections()[0].GetId() != "persona" {
		t.Fatal("section id lost")
	}
	tm := pb.GetToolMapping()
	if len(tm) != 2 || tm[0].GetToolName() != "read_code" || !tm[0].GetEnabled() || tm[1].GetToolName() != "search" || tm[1].GetEnabled() {
		t.Fatalf("tool mapping wrong: %+v", tm)
	}
	if (*Profile)(nil).ToProto() != nil {
		t.Fatal("nil profile ToProto should be nil")
	}
}

func TestToProto_EmptyProviderModelStayUnset(t *testing.T) {
	p := &Profile{ID: "x", Name: "X"}
	pb := p.ToProto()
	if pb.LlmProviderId != nil || pb.DefaultModel != nil {
		t.Fatal("empty provider/model must stay unset on the proto")
	}
}
