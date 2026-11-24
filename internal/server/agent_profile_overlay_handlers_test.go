package server

import (
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/agentprofile"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// newOverlayFixture is newAgentProfileFixture with the overlay store wired
// into both the server and its resolver, as llmdhost does.
func newOverlayFixture(t *testing.T) *agentProfileFixture {
	t.Helper()
	f := newAgentProfileFixture(t, staticProfileA())
	repo := store.NewAgentProfileOverlayRepo(f.db)
	f.srv.AgentProfileOverlays = repo
	f.srv.AgentProfiles.Overlays = repo
	return f
}

func TestAgentProfileOverlay_SetGetResetRoundTrip(t *testing.T) {
	f := newOverlayFixture(t)
	ctx := agentProfileCtx()

	got, err := f.srv.GetAgentProfileOverlay(ctx, &llmv1.GetAgentProfileOverlayRequest{BaseId: "static-a"})
	if err != nil {
		t.Fatalf("Get before Set: %v", err)
	}
	if got.GetOverlay().GetBaseId() != "static-a" || got.GetOverlay().DefaultModel != nil || got.GetBase().GetId() != "static-a" {
		t.Fatalf("Get before Set = %v", got)
	}

	set, err := f.srv.SetAgentProfileOverlay(ctx, &llmv1.SetAgentProfileOverlayRequest{Overlay: &llmv1.AgentProfileOverlay{
		BaseId:        "static-a",
		LlmProviderId: proto.String("provider_b"),
		DefaultModel:  proto.String("model-b"),
		ToolOverrides: []*llmv1.ToolMapping{{ToolName: "probe_tool", Enabled: true}},
		ExtraSections: []*llmv1.SystemPromptSection{{Id: "mine", Content: "my rule"}},
	}})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if set.GetProfile().GetDefaultModel() != "model-b" || !set.GetProfile().GetIsStatic() || len(set.GetProfile().GetCustomized()) == 0 {
		t.Errorf("Set profile = %v", set.GetProfile())
	}

	// Get and List now return the merged profile; the overlay RPC returns the base unmerged.
	gp, err := f.srv.GetAgentProfile(ctx, &llmv1.GetAgentProfileRequest{Id: "static-a"})
	if err != nil || gp.GetProfile().GetDefaultModel() != "model-b" {
		t.Fatalf("GetAgentProfile = %v, %v", gp, err)
	}
	ov, err := f.srv.GetAgentProfileOverlay(ctx, &llmv1.GetAgentProfileOverlayRequest{BaseId: "static-a"})
	if err != nil || ov.GetOverlay().GetDefaultModel() != "model-b" || ov.GetBase().GetDefaultModel() == "model-b" || len(ov.GetBase().GetCustomized()) != 0 {
		t.Fatalf("GetAgentProfileOverlay = %v, %v", ov, err)
	}

	// Another workspace does not see it.
	other, err := f.srv.GetAgentProfile(agentProfileCtxFor("ws-other"), &llmv1.GetAgentProfileRequest{Id: "static-a"})
	if err != nil || other.GetProfile().GetDefaultModel() == "model-b" {
		t.Fatalf("other workspace = %v, %v", other, err)
	}

	reset, err := f.srv.ResetAgentProfileOverlay(ctx, &llmv1.ResetAgentProfileOverlayRequest{BaseId: "static-a"})
	if err != nil || reset.GetProfile().GetDefaultModel() == "model-b" {
		t.Fatalf("Reset = %v, %v", reset, err)
	}
	gp, _ = f.srv.GetAgentProfile(ctx, &llmv1.GetAgentProfileRequest{Id: "static-a"})
	if gp.GetProfile().GetDefaultModel() == "model-b" || len(gp.GetProfile().GetCustomized()) != 0 {
		t.Errorf("after Reset = %v", gp.GetProfile())
	}
	// Resetting again is fine.
	if _, err := f.srv.ResetAgentProfileOverlay(ctx, &llmv1.ResetAgentProfileOverlayRequest{BaseId: "static-a"}); err != nil {
		t.Errorf("second Reset: %v", err)
	}
}

func TestAgentProfileOverlay_Rejections(t *testing.T) {
	f := newOverlayFixture(t)
	ctx := agentProfileCtx()
	dyn := mustCreateDynamic(t, f, ctx, "Dyn")

	for name, c := range map[string]struct {
		ov   *llmv1.AgentProfileOverlay
		want codes.Code
	}{
		"dynamic profile":  {&llmv1.AgentProfileOverlay{BaseId: dyn.GetId()}, codes.NotFound},
		"unknown profile":  {&llmv1.AgentProfileOverlay{BaseId: "nope"}, codes.NotFound},
		"no base id":       {&llmv1.AgentProfileOverlay{}, codes.InvalidArgument},
		"unknown tool":     {&llmv1.AgentProfileOverlay{BaseId: "static-a", ToolOverrides: []*llmv1.ToolMapping{{ToolName: "no_such_tool", Enabled: true}}}, codes.InvalidArgument},
		"unknown provider": {&llmv1.AgentProfileOverlay{BaseId: "static-a", LlmProviderId: proto.String("no_such_provider")}, codes.InvalidArgument},
		"section clash":    {&llmv1.AgentProfileOverlay{BaseId: "static-a", ExtraSections: []*llmv1.SystemPromptSection{{Id: staticProfileA().SystemPromptSections[0].GetId(), Content: "x"}}}, codes.InvalidArgument},
	} {
		_, err := f.srv.SetAgentProfileOverlay(ctx, &llmv1.SetAgentProfileOverlayRequest{Overlay: c.ov})
		if status.Code(err) != c.want {
			t.Errorf("%s: code = %v, want %v (err=%v)", name, status.Code(err), c.want, err)
		}
	}
	if _, err := f.srv.SetAgentProfileOverlay(ctx, &llmv1.SetAgentProfileOverlayRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("nil overlay: %v", err)
	}
}

func TestAgentProfileOverlay_NotConfigured(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	_, err := f.srv.GetAgentProfileOverlay(agentProfileCtx(), &llmv1.GetAgentProfileOverlayRequest{BaseId: "static-a"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (err=%v)", status.Code(err), err)
	}
}

// A session created from an overlaid profile uses the merged profile.
func TestAgentProfileOverlay_SessionUsesMergedProfile(t *testing.T) {
	f := newOverlayFixture(t)
	ctx := agentProfileCtx()
	if _, err := f.srv.SetAgentProfileOverlay(ctx, &llmv1.SetAgentProfileOverlayRequest{Overlay: &llmv1.AgentProfileOverlay{
		BaseId: "static-a", ExtraSections: []*llmv1.SystemPromptSection{{Id: "mine", Content: "my rule"}},
	}}); err != nil {
		t.Fatal(err)
	}
	p, err := f.srv.agentProfiles().Get(ctx, agentProfileTestScope, "static-a")
	if err != nil {
		t.Fatal(err)
	}
	req, err := agentprofile.BuildEnsureSessionRequest(p, agentprofile.SessionOverrides{SessionID: "s1"}, f.srv.tenantTools(agentProfileTestWorkspace))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range req.GetSystemPromptSections() {
		if s.GetId() == "mine" && s.GetContent() == "my rule" {
			found = true
		}
	}
	if !found {
		t.Errorf("session sections = %v, want the overlay's extra section", req.GetSystemPromptSections())
	}
}
