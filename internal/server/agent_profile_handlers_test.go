package server

import (
	"context"
	"github.com/johnewart/pyxis/internal/serverutil"
	"strings"
	"testing"
	"time"

	"github.com/johnewart/pyxis/coretools"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/agentprofile"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

const agentProfileTestWorkspace = "ws-agent-profile"

// agentProfileTestScope is the Scope the agent-profile fixtures act under.
var agentProfileTestScope = serverutil.Scope{TenantID: agentProfileTestWorkspace}

type agentProfileFixture struct {
	srv *LlmServer
	mgr *sessionmanager.Manager
	db  *gorm.DB
}

// newAgentProfileFixture wires an LlmServer with a SQLite-backed session
// manager, two fake providers (so a profile's llm_provider_id passes
// validateAgentLlmBinding), one server-wide tool ("probe_tool"), an
// isolated static registry holding the given profiles AND the SQLite-backed
// dynamic tier (AgentProfileRepo + a resolver built over it).
func newAgentProfileFixture(t *testing.T, profiles ...agentprofile.Profile) *agentProfileFixture {
	t.Helper()
	return newAgentProfileFixtureWith(t, true, profiles...)
}

// newAgentProfileFixtureStaticOnly is newAgentProfileFixture without the
// dynamic tier: no AgentProfileRepo and a resolver whose Dynamic is nil.
func newAgentProfileFixtureStaticOnly(t *testing.T, profiles ...agentprofile.Profile) *agentProfileFixture {
	t.Helper()
	return newAgentProfileFixtureWith(t, false, profiles...)
}

func newAgentProfileFixtureWith(t *testing.T, dynamic bool, profiles ...agentprofile.Profile) *agentProfileFixture {
	t.Helper()
	mgr, db := sessionManagerWithSQLite(t, session.HubOptions{})
	preg, err := providers.NewRegistryFromEntries("provider_a", map[string]*providers.Entry{
		"provider_a": {ID: "provider_a", Kind: providerkind.OpenAI, Backend: &captureStreamBackend{}, DefaultModel: "model-a"},
		"provider_b": {ID: "provider_b", Kind: providerkind.OpenAI, Backend: &captureStreamBackend{}, DefaultModel: "model-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := toolregistry.New()
	if err := tools.Register(&llmv1.ToolDefinition{Name: "probe_tool", Description: "probe"}); err != nil {
		t.Fatal(err)
	}
	reg := agentprofile.NewRegistry()
	if err := reg.Register(profiles...); err != nil {
		t.Fatal(err)
	}
	srv := &LlmServer{
		Providers:       providers.NewComposite(preg, nil),
		Tools:           tools,
		SessionRuntimes: mgr,
		SessionRepo:     store.NewSessionRepo(db),
		AgentProfiles:   agentprofile.NewResolver(reg, nil),
	}
	if dynamic {
		repo := store.NewAgentProfileRepo(db)
		srv.AgentProfileRepo = repo
		srv.AgentProfiles = agentprofile.NewResolver(reg, repo)
	}
	return &agentProfileFixture{srv: srv, mgr: mgr, db: db}
}

func agentProfileCtxFor(ws string) context.Context {
	return serverutil.Scope{TenantID: ws}.IncomingContext(context.Background())
}

// dynamicCreateReq is a complete, valid CreateAgentProfileRequest for the
// fixture: provider_a/model-a, probe_tool enabled, one section.
func dynamicCreateReq(name string) *llmv1.CreateAgentProfileRequest {
	return &llmv1.CreateAgentProfileRequest{
		Name:        name,
		Description: "dynamic profile",
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "intro", Content: "You are " + name + "."},
		},
		LlmProviderId: proto.String("provider_a"),
		DefaultModel:  proto.String("model-a"),
		MemoryEnabled: true,
		ToolMapping:   []*llmv1.ToolMapping{{ToolName: "probe_tool", Enabled: true}},
	}
}

func mustCreateDynamic(t *testing.T, f *agentProfileFixture, ctx context.Context, name string) *llmv1.AgentProfile {
	t.Helper()
	resp, err := f.srv.CreateAgentProfile(ctx, dynamicCreateReq(name))
	if err != nil {
		t.Fatalf("CreateAgentProfile(%q): %v", name, err)
	}
	if resp.GetProfile().GetId() == "" {
		t.Fatalf("CreateAgentProfile(%q) returned blank id", name)
	}
	return resp.GetProfile()
}

func TestAgentProfile_CreateGetListDynamic(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	ctx := agentProfileCtx()

	created := mustCreateDynamic(t, f, ctx, "Dyn One")
	if created.GetIsStatic() {
		t.Error("created profile IsStatic = true, want false")
	}
	if created.GetCreatedAt() == "" || created.GetUpdatedAt() == "" {
		t.Errorf("created profile timestamps empty: created=%q updated=%q", created.GetCreatedAt(), created.GetUpdatedAt())
	}

	got, err := f.srv.GetAgentProfile(ctx, &llmv1.GetAgentProfileRequest{Id: created.GetId()})
	if err != nil {
		t.Fatalf("GetAgentProfile(dynamic): %v", err)
	}
	p := got.GetProfile()
	if p.GetIsStatic() {
		t.Error("IsStatic = true, want false")
	}
	if p.GetName() != "Dyn One" || p.GetDescription() != "dynamic profile" {
		t.Errorf("name/description = %q/%q", p.GetName(), p.GetDescription())
	}
	if p.GetLlmProviderId() != "provider_a" || p.GetDefaultModel() != "model-a" {
		t.Errorf("provider/model = %q/%q, want provider_a/model-a", p.GetLlmProviderId(), p.GetDefaultModel())
	}
	if !p.GetMemoryEnabled() {
		t.Error("MemoryEnabled = false, want true")
	}
	if len(p.GetSystemPromptSections()) != 1 || p.GetSystemPromptSections()[0].GetId() != "intro" {
		t.Errorf("sections = %v, want [intro]", p.GetSystemPromptSections())
	}
	if len(p.GetToolMapping()) != 1 || p.GetToolMapping()[0].GetToolName() != "probe_tool" || !p.GetToolMapping()[0].GetEnabled() {
		t.Errorf("tool mapping = %v, want [probe_tool enabled]", p.GetToolMapping())
	}

	list, err := f.srv.ListAgentProfiles(ctx, &llmv1.ListAgentProfilesRequest{})
	if err != nil {
		t.Fatalf("ListAgentProfiles: %v", err)
	}
	if n := len(list.GetProfiles()); n != 2 {
		t.Fatalf("len(ListAgentProfiles) = %d, want 2 (%v)", n, list.GetProfiles())
	}
	if list.GetProfiles()[0].GetId() != "static-a" || !list.GetProfiles()[0].GetIsStatic() {
		t.Errorf("first listed = %v, want static-a (static)", list.GetProfiles()[0])
	}
	if list.GetProfiles()[1].GetId() != created.GetId() || list.GetProfiles()[1].GetIsStatic() {
		t.Errorf("second listed = %v, want %s (dynamic)", list.GetProfiles()[1], created.GetId())
	}
}

func TestAgentProfile_CreateRejections(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	ctx := agentProfileCtx()

	badTool := dynamicCreateReq("bad tool")
	badTool.ToolMapping = []*llmv1.ToolMapping{{ToolName: "nope", Enabled: true}}
	if _, err := f.srv.CreateAgentProfile(ctx, badTool); status.Code(err) != codes.InvalidArgument {
		t.Errorf("unknown tool code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}

	badProvider := dynamicCreateReq("bad provider")
	badProvider.LlmProviderId = proto.String("provider_zzz")
	if _, err := f.srv.CreateAgentProfile(ctx, badProvider); status.Code(err) != codes.InvalidArgument {
		t.Errorf("unknown provider code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}

	blankName := dynamicCreateReq("   ")
	if _, err := f.srv.CreateAgentProfile(ctx, blankName); status.Code(err) != codes.InvalidArgument {
		t.Errorf("blank name code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}

	staticID := dynamicCreateReq("shadow")
	staticID.Id = proto.String("static-a")
	if _, err := f.srv.CreateAgentProfile(ctx, staticID); status.Code(err) != codes.AlreadyExists {
		t.Errorf("static id code = %v, want AlreadyExists (err=%v)", status.Code(err), err)
	}

	mustCreateDynamic(t, f, ctx, "Twice")
	if _, err := f.srv.CreateAgentProfile(ctx, dynamicCreateReq("Twice")); status.Code(err) != codes.AlreadyExists {
		t.Errorf("duplicate name code = %v, want AlreadyExists (err=%v)", status.Code(err), err)
	}

	if _, err := f.srv.CreateAgentProfile(context.Background(), dynamicCreateReq("no ws")); status.Code(err) != codes.Unauthenticated {
		t.Errorf("no workspace code = %v, want Unauthenticated (err=%v)", status.Code(err), err)
	}

	// Rejected creates must not leave rows behind.
	list, err := f.srv.ListAgentProfiles(ctx, &llmv1.ListAgentProfilesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(list.GetProfiles()); n != 2 {
		t.Errorf("len(ListAgentProfiles) = %d, want 2 (static-a + Twice)", n)
	}
}

func TestAgentProfile_UpdateWholesale(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	ctx := agentProfileCtx()

	created := mustCreateDynamic(t, f, ctx, "Before")
	upd, err := f.srv.UpdateAgentProfile(ctx, &llmv1.UpdateAgentProfileRequest{
		Id:   created.GetId(),
		Name: "After",
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "a", Content: "A"},
			{Id: "b", Content: "B"},
		},
		ToolMapping: []*llmv1.ToolMapping{{ToolName: "probe_tool", Enabled: false}},
	})
	if err != nil {
		t.Fatalf("UpdateAgentProfile: %v", err)
	}
	if upd.GetProfile().GetName() != "After" {
		t.Errorf("update response name = %q, want After", upd.GetProfile().GetName())
	}

	got, err := f.srv.GetAgentProfile(ctx, &llmv1.GetAgentProfileRequest{Id: created.GetId()})
	if err != nil {
		t.Fatalf("GetAgentProfile after update: %v", err)
	}
	p := got.GetProfile()
	if p.GetName() != "After" || p.GetDescription() != "" {
		t.Errorf("name/description = %q/%q, want After/\"\"", p.GetName(), p.GetDescription())
	}
	if p.LlmProviderId != nil || p.DefaultModel != nil || p.GetMemoryEnabled() {
		t.Errorf("omitted fields not cleared: provider=%v model=%v memory=%v", p.LlmProviderId, p.DefaultModel, p.GetMemoryEnabled())
	}
	if len(p.GetSystemPromptSections()) != 2 || p.GetSystemPromptSections()[1].GetId() != "b" {
		t.Errorf("sections = %v, want [a b]", p.GetSystemPromptSections())
	}
	if len(p.GetToolMapping()) != 1 || p.GetToolMapping()[0].GetEnabled() {
		t.Errorf("tool mapping = %v, want [probe_tool disabled]", p.GetToolMapping())
	}
	createdAt, err1 := time.Parse(time.RFC3339, p.GetCreatedAt())
	updatedAt, err2 := time.Parse(time.RFC3339, p.GetUpdatedAt())
	if err1 != nil || err2 != nil {
		t.Fatalf("timestamps unparsable: created=%q (%v) updated=%q (%v)", p.GetCreatedAt(), err1, p.GetUpdatedAt(), err2)
	}
	if updatedAt.Before(createdAt) {
		t.Errorf("UpdatedAt %v < CreatedAt %v", updatedAt, createdAt)
	}
	if p.GetCreatedAt() != created.GetCreatedAt() {
		t.Errorf("CreatedAt changed on update: %q -> %q", created.GetCreatedAt(), p.GetCreatedAt())
	}

	_, err = f.srv.UpdateAgentProfile(ctx, &llmv1.UpdateAgentProfileRequest{Id: "ghost", Name: "x"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("update unknown code = %v, want NotFound (err=%v)", status.Code(err), err)
	}
	_, err = f.srv.UpdateAgentProfile(ctx, &llmv1.UpdateAgentProfileRequest{Id: "static-a", Name: "x"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("update static code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
	badTool := &llmv1.UpdateAgentProfileRequest{Id: created.GetId(), Name: "x", ToolMapping: []*llmv1.ToolMapping{{ToolName: "nope", Enabled: true}}}
	if _, err = f.srv.UpdateAgentProfile(ctx, badTool); status.Code(err) != codes.InvalidArgument {
		t.Errorf("update unknown tool code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
	other := mustCreateDynamic(t, f, ctx, "Other")
	_, err = f.srv.UpdateAgentProfile(ctx, &llmv1.UpdateAgentProfileRequest{Id: other.GetId(), Name: "After"})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("update to taken name code = %v, want AlreadyExists (err=%v)", status.Code(err), err)
	}
}

func TestAgentProfile_DeleteDynamic(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	ctx := agentProfileCtx()

	created := mustCreateDynamic(t, f, ctx, "Doomed")
	if _, err := f.srv.DeleteAgentProfile(ctx, &llmv1.DeleteAgentProfileRequest{Id: created.GetId()}); err != nil {
		t.Fatalf("DeleteAgentProfile: %v", err)
	}
	_, err := f.srv.GetAgentProfile(ctx, &llmv1.GetAgentProfileRequest{Id: created.GetId()})
	if status.Code(err) != codes.NotFound {
		t.Errorf("get after delete code = %v, want NotFound (err=%v)", status.Code(err), err)
	}
	_, err = f.srv.DeleteAgentProfile(ctx, &llmv1.DeleteAgentProfileRequest{Id: created.GetId()})
	if status.Code(err) != codes.NotFound {
		t.Errorf("delete unknown code = %v, want NotFound (err=%v)", status.Code(err), err)
	}
	_, err = f.srv.DeleteAgentProfile(ctx, &llmv1.DeleteAgentProfileRequest{Id: "static-a"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("delete static code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
	_, err = f.srv.DeleteAgentProfile(context.Background(), &llmv1.DeleteAgentProfileRequest{Id: "whatever"})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("delete without workspace code = %v, want Unauthenticated (err=%v)", status.Code(err), err)
	}
}

func TestAgentProfile_WorkspaceIsolationOnGet(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	created := mustCreateDynamic(t, f, agentProfileCtx(), "Mine")

	_, err := f.srv.GetAgentProfile(agentProfileCtxFor("ws-other"), &llmv1.GetAgentProfileRequest{Id: created.GetId()})
	if status.Code(err) != codes.NotFound {
		t.Errorf("cross-workspace get code = %v, want NotFound (err=%v)", status.Code(err), err)
	}
	list, err := f.srv.ListAgentProfiles(agentProfileCtxFor("ws-other"), &llmv1.ListAgentProfilesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(list.GetProfiles()); n != 1 || list.GetProfiles()[0].GetId() != "static-a" {
		t.Errorf("cross-workspace list = %v, want [static-a]", list.GetProfiles())
	}
}

// A session created from a dynamic profile must carry the profile's runtime
// and persist a row identical to a hand-built EnsureSession from the same
// materialized request.
func TestAgentProfile_CreateSessionFromDynamicProfile(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	ctx := agentProfileCtx()
	created := mustCreateDynamic(t, f, ctx, "Dyn Session")

	resp, err := f.srv.CreateSessionFromAgentProfile(ctx, &llmv1.CreateSessionFromAgentProfileRequest{
		AgentProfileId: created.GetId(),
		SessionId:      "d1",
	})
	if err != nil {
		t.Fatalf("CreateSessionFromAgentProfile(dynamic): %v", err)
	}
	if resp.GetAgentProfileId() != created.GetId() || resp.GetSessionId() != "d1" {
		t.Errorf("response = %v", resp)
	}
	rt, ok := f.mgr.Get(agentProfileTestScope, "d1")
	if !ok {
		t.Fatal("session d1 not in manager")
	}
	if rt.ProviderID != "provider_a" || rt.DefaultModel != "model-a" {
		t.Errorf("runtime = %q/%q, want provider_a/model-a", rt.ProviderID, rt.DefaultModel)
	}
	// The profile's one section, after the identity section llmd adds.
	if len(rt.SystemPromptSections) != 2 || rt.SystemPromptSections[0].GetId() != agentprofile.IdentitySectionID {
		t.Errorf("SystemPromptSections = %v, want [%s + profile's]", rt.SystemPromptSections, agentprofile.IdentitySectionID)
	}

	profile, err := f.srv.agentProfiles().Get(ctx, serverutil.Scope{TenantID: agentProfileTestWorkspace}, created.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if profile.Static {
		t.Fatal("resolved profile is static, want dynamic")
	}
	manualReq, err := agentprofile.BuildEnsureSessionRequest(profile, agentprofile.SessionOverrides{SessionID: "d1-manual"}, f.srv.registeredTools())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.EnsureSession(ctx, manualReq); err != nil {
		t.Fatalf("manual EnsureSession: %v", err)
	}

	repo := store.NewSessionRepo(f.db)
	got, err := repo.GetSession(ctx, serverutil.Scope{TenantID: agentProfileTestWorkspace}, "d1")
	if err != nil {
		t.Fatalf("GetSession(d1): %v", err)
	}
	want, err := repo.GetSession(ctx, serverutil.Scope{TenantID: agentProfileTestWorkspace}, "d1-manual")
	if err != nil {
		t.Fatalf("GetSession(d1-manual): %v", err)
	}
	if got.ProviderID != want.ProviderID {
		t.Errorf("ProviderID = %q, want %q", got.ProviderID, want.ProviderID)
	}
	if got.DefaultModel != want.DefaultModel {
		t.Errorf("DefaultModel = %q, want %q", got.DefaultModel, want.DefaultModel)
	}
	if got.ToolsJSON != want.ToolsJSON {
		t.Errorf("ToolsJSON = %q, want %q", got.ToolsJSON, want.ToolsJSON)
	}
	if got.SystemPromptSectionsJSON != want.SystemPromptSectionsJSON {
		t.Errorf("SystemPromptSectionsJSON = %q, want %q", got.SystemPromptSectionsJSON, want.SystemPromptSectionsJSON)
	}
	if got.ProviderID != "provider_a" || got.ToolsJSON == "" {
		t.Errorf("row not populated from profile: provider=%q tools=%q", got.ProviderID, got.ToolsJSON)
	}
}

func agentProfileCtx() context.Context {
	return serverutil.Scope{TenantID: agentProfileTestWorkspace}.IncomingContext(context.Background())
}

func staticProfileA() agentprofile.Profile {
	return agentprofile.Profile{
		ID:          "static-a",
		Name:        "Static A",
		Description: "test profile",
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "intro", Content: "You are static A."},
		},
		LlmProviderID: "provider_a",
		DefaultModel:  "model-a",
		ToolMapping:   []agentprofile.ToolMapping{{ToolName: "probe_tool", Enabled: true}},
	}
}

// The lazy accessor must wire AgentProfileRepo as the dynamic tier and must
// leave Dynamic an untyped nil when no repo is set (a typed-nil pointer
// stored in the interface would make the resolver call methods on nil).
func TestAgentProfile_LazyResolverUsesRepo(t *testing.T) {
	_, db := sessionManagerWithSQLite(t, session.HubOptions{})
	withRepo := &LlmServer{AgentProfileRepo: store.NewAgentProfileRepo(db)}
	if withRepo.agentProfiles().Dynamic == nil {
		t.Fatal("agentProfiles().Dynamic = nil with AgentProfileRepo set, want repo")
	}
	bare := &LlmServer{}
	if bare.agentProfiles().Dynamic != nil {
		t.Fatalf("agentProfiles().Dynamic = %#v without a repo, want untyped nil", bare.agentProfiles().Dynamic)
	}
	_, err := bare.CreateAgentProfile(agentProfileCtx(), &llmv1.CreateAgentProfileRequest{Name: "dyn"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("CreateAgentProfile on bare server code = %v, want FailedPrecondition (err=%v)", status.Code(err), err)
	}
}

func TestAgentProfile_GetStaticReturnsIsStatic(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	resp, err := f.srv.GetAgentProfile(agentProfileCtx(), &llmv1.GetAgentProfileRequest{Id: "static-a"})
	if err != nil {
		t.Fatalf("GetAgentProfile: %v", err)
	}
	p := resp.GetProfile()
	if p == nil {
		t.Fatal("GetAgentProfile returned nil profile")
	}
	if !p.GetIsStatic() {
		t.Errorf("IsStatic = false, want true")
	}
	if p.GetName() != "Static A" {
		t.Errorf("Name = %q, want %q", p.GetName(), "Static A")
	}
	if p.GetId() != "static-a" {
		t.Errorf("Id = %q, want %q", p.GetId(), "static-a")
	}
}

// The static tier must be reachable without tenant/scope metadata — the
// Unauthenticated error from scope resolution is not fatal for a static hit.
func TestAgentProfile_GetStaticWithoutWorkspaceMetadata(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	resp, err := f.srv.GetAgentProfile(context.Background(), &llmv1.GetAgentProfileRequest{Id: "static-a"})
	if err != nil {
		t.Fatalf("GetAgentProfile without metadata: %v", err)
	}
	if !resp.GetProfile().GetIsStatic() {
		t.Errorf("IsStatic = false, want true")
	}
	list, err := f.srv.ListAgentProfiles(context.Background(), &llmv1.ListAgentProfilesRequest{})
	if err != nil {
		t.Fatalf("ListAgentProfiles without metadata: %v", err)
	}
	if len(list.GetProfiles()) != 1 || list.GetProfiles()[0].GetId() != "static-a" {
		t.Errorf("ListAgentProfiles = %v, want [static-a]", list.GetProfiles())
	}
}

func TestAgentProfile_GetUnknownIsNotFound(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	_, err := f.srv.GetAgentProfile(agentProfileCtx(), &llmv1.GetAgentProfileRequest{Id: "nope"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("GetAgentProfile(unknown) code = %v, want NotFound (err=%v)", status.Code(err), err)
	}
	_, err = f.srv.GetAgentProfile(agentProfileCtx(), &llmv1.GetAgentProfileRequest{Id: "  "})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetAgentProfile(blank) code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
}

func TestAgentProfile_CreateFailsPreconditionWithoutDynamicStore(t *testing.T) {
	f := newAgentProfileFixtureStaticOnly(t, staticProfileA())
	ctx := agentProfileCtx()

	_, err := f.srv.CreateAgentProfile(ctx, &llmv1.CreateAgentProfileRequest{Name: "dyn"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("CreateAgentProfile code = %v, want FailedPrecondition (err=%v)", status.Code(err), err)
	}
	_, err = f.srv.UpdateAgentProfile(ctx, &llmv1.UpdateAgentProfileRequest{Id: "static-a", Name: "x"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("UpdateAgentProfile code = %v, want FailedPrecondition (err=%v)", status.Code(err), err)
	}
	_, err = f.srv.DeleteAgentProfile(ctx, &llmv1.DeleteAgentProfileRequest{Id: "static-a"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("DeleteAgentProfile code = %v, want FailedPrecondition (err=%v)", status.Code(err), err)
	}
}

func TestAgentProfile_CreateSessionCarriesProfileRuntime(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	ctx := agentProfileCtx()

	resp, err := f.srv.CreateSessionFromAgentProfile(ctx, &llmv1.CreateSessionFromAgentProfileRequest{
		AgentProfileId: "static-a",
		SessionId:      "s1",
	})
	if err != nil {
		t.Fatalf("CreateSessionFromAgentProfile: %v", err)
	}
	if resp.GetSessionId() != "s1" || resp.GetAgentProfileId() != "static-a" {
		t.Errorf("response = %v, want session s1 / profile static-a", resp)
	}
	rt, ok := f.mgr.Get(agentProfileTestScope, "s1")
	if !ok {
		t.Fatal("session s1 not in manager after CreateSessionFromAgentProfile")
	}
	if rt.ProviderID != "provider_a" {
		t.Errorf("ProviderID = %q, want provider_a", rt.ProviderID)
	}
	if rt.DefaultModel != "model-a" {
		t.Errorf("DefaultModel = %q, want model-a", rt.DefaultModel)
	}
	// The profile's one section, after the identity section llmd adds.
	if len(rt.SystemPromptSections) != 2 || rt.SystemPromptSections[0].GetId() != agentprofile.IdentitySectionID {
		t.Errorf("SystemPromptSections = %v, want [%s + profile's]", rt.SystemPromptSections, agentprofile.IdentitySectionID)
	}

	// Overrides win over the profile's provider/model defaults.
	_, err = f.srv.CreateSessionFromAgentProfile(ctx, &llmv1.CreateSessionFromAgentProfileRequest{
		AgentProfileId:        "static-a",
		SessionId:             "s2",
		LlmProviderIdOverride: proto.String("provider_b"),
		DefaultModelOverride:  proto.String("model-b"),
	})
	if err != nil {
		t.Fatalf("CreateSessionFromAgentProfile(overrides): %v", err)
	}
	rt2, ok := f.mgr.Get(agentProfileTestScope, "s2")
	if !ok {
		t.Fatal("session s2 not in manager")
	}
	if rt2.ProviderID != "provider_b" || rt2.DefaultModel != "model-b" {
		t.Errorf("override runtime = %q/%q, want provider_b/model-b", rt2.ProviderID, rt2.DefaultModel)
	}
}

// A profile-created session's persisted row must be indistinguishable from
// one created by hand through EnsureSession with the same materialized
// request — CreateSessionFromAgentProfile is an adapter, not a second path.
func TestAgentProfile_CreateSessionRowMatchesManualEnsureSession(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	ctx := agentProfileCtx()

	if _, err := f.srv.CreateSessionFromAgentProfile(ctx, &llmv1.CreateSessionFromAgentProfileRequest{
		AgentProfileId: "static-a",
		SessionId:      "s1",
	}); err != nil {
		t.Fatalf("CreateSessionFromAgentProfile: %v", err)
	}

	profile, err := f.srv.agentProfiles().Get(ctx, serverutil.Scope{TenantID: agentProfileTestWorkspace}, "static-a")
	if err != nil {
		t.Fatal(err)
	}
	manualReq, err := agentprofile.BuildEnsureSessionRequest(profile, agentprofile.SessionOverrides{SessionID: "s1-manual"}, f.srv.registeredTools())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.EnsureSession(ctx, manualReq); err != nil {
		t.Fatalf("manual EnsureSession: %v", err)
	}

	repo := store.NewSessionRepo(f.db)
	got, err := repo.GetSession(ctx, serverutil.Scope{TenantID: agentProfileTestWorkspace}, "s1")
	if err != nil {
		t.Fatalf("GetSession(s1): %v", err)
	}
	want, err := repo.GetSession(ctx, serverutil.Scope{TenantID: agentProfileTestWorkspace}, "s1-manual")
	if err != nil {
		t.Fatalf("GetSession(s1-manual): %v", err)
	}
	if got.ProviderID != want.ProviderID {
		t.Errorf("ProviderID = %q, want %q", got.ProviderID, want.ProviderID)
	}
	if got.DefaultModel != want.DefaultModel {
		t.Errorf("DefaultModel = %q, want %q", got.DefaultModel, want.DefaultModel)
	}
	if got.ToolsJSON != want.ToolsJSON {
		t.Errorf("ToolsJSON = %q, want %q", got.ToolsJSON, want.ToolsJSON)
	}
	if got.SystemPromptSectionsJSON != want.SystemPromptSectionsJSON {
		t.Errorf("SystemPromptSectionsJSON = %q, want %q", got.SystemPromptSectionsJSON, want.SystemPromptSectionsJSON)
	}
	if got.ProviderID != "provider_a" || got.ToolsJSON == "" {
		t.Errorf("row not populated from profile: provider=%q tools=%q", got.ProviderID, got.ToolsJSON)
	}
}

func TestAgentProfile_CreateSessionUnknownToolIsInvalidArgument(t *testing.T) {
	bad := staticProfileA()
	bad.ID = "static-bad-tool"
	bad.ToolMapping = []agentprofile.ToolMapping{{ToolName: "nope", Enabled: true}}
	f := newAgentProfileFixture(t, bad)

	_, err := f.srv.CreateSessionFromAgentProfile(agentProfileCtx(), &llmv1.CreateSessionFromAgentProfileRequest{
		AgentProfileId: "static-bad-tool",
		SessionId:      "s-bad",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
	if _, ok := f.mgr.Get(agentProfileTestScope, "s-bad"); ok {
		t.Error("session s-bad was created despite the unknown tool")
	}
}

func TestAgentProfile_CreateSessionUnknownProfileIsNotFound(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	_, err := f.srv.CreateSessionFromAgentProfile(agentProfileCtx(), &llmv1.CreateSessionFromAgentProfileRequest{
		AgentProfileId: "ghost",
		SessionId:      "s-ghost",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound (err=%v)", status.Code(err), err)
	}
	_, err = f.srv.CreateSessionFromAgentProfile(agentProfileCtx(), &llmv1.CreateSessionFromAgentProfileRequest{
		SessionId: "s-noprofile",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing profile id code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
}

// ---------------------------------------------------------------------------
// Profile-driven disabled tools: a ToolMapping{Enabled:false} on a profile
// lands on EnsureSessionRequest.disabled_tools (agentprofile.Build...) and
// from there on the session runtime, which ListSessionTools and
// mergedToolDefinitions honour through toolmerge.MergeDefinitionsExcluding.
// ---------------------------------------------------------------------------

// installCoreToolsOnFixture registers every core tool on the fixture server
// exactly the way a composition root does (newCoreToolsServer), so the
// "core tools are never hidden" assertions below measure something: without
// this call the fixture's registry holds only probe_tool and
// assertHasEveryCoreTool would be vacuous.
func installCoreToolsOnFixture(t *testing.T, f *agentProfileFixture) {
	t.Helper()
	if err := InstallCoreTools(f.srv, toolexec.SessionToolsDeps{
		Sessions:    store.NewSessionRepo(f.db),
		Events:      store.NewSessionEventRepo(f.db),
		Turns:       store.NewTurnRegister(f.db),
		Attachments: store.NewChatAttachmentRepo(f.db),
	}); err != nil {
		t.Fatalf("InstallCoreTools: %v", err)
	}
}

// staticDisableProfile is staticProfileA with probe_tool mapped as disabled
// instead of enabled.
func staticDisableProfile() agentprofile.Profile {
	p := staticProfileA()
	p.ID = "static-disable"
	p.Name = "Static Disable"
	p.ToolMapping = []agentprofile.ToolMapping{{ToolName: "probe_tool", Enabled: false}}
	return p
}

func createSessionFromProfile(t *testing.T, f *agentProfileFixture, ctx context.Context, req *llmv1.CreateSessionFromAgentProfileRequest) {
	t.Helper()
	if _, err := f.srv.CreateSessionFromAgentProfile(ctx, req); err != nil {
		t.Fatalf("CreateSessionFromAgentProfile(profile=%q, session=%q): %v", req.GetAgentProfileId(), req.GetSessionId(), err)
	}
}

func assertRuntimeDisables(t *testing.T, f *agentProfileFixture, sid, tool string) {
	t.Helper()
	rt, ok := f.mgr.Get(agentProfileTestScope, sid)
	if !ok {
		t.Fatalf("session %q not in manager", sid)
	}
	if _, disabled := rt.DisabledToolSet()[tool]; !disabled {
		t.Errorf("session %q DisabledToolSet() = %v, want it to contain %q", sid, rt.DisabledToolSet(), tool)
	}
}

// A static profile that maps probe_tool as disabled produces a session that
// does not advertise probe_tool — on the client-facing ListSessionTools AND
// on the provider-facing mergedToolDefinitions — while every core tool is
// still offered.
func TestAgentProfile_DisabledMappingHidesToolFromSession(t *testing.T) {
	f := newAgentProfileFixture(t, staticDisableProfile())
	installCoreToolsOnFixture(t, f)
	ctx := agentProfileCtx()

	createSessionFromProfile(t, f, ctx, &llmv1.CreateSessionFromAgentProfileRequest{
		AgentProfileId: "static-disable",
		SessionId:      "sd1",
	})

	listed := listSessionTools(t, ctx, f.srv, "sd1")
	if toolDefinitionsContainName(listed, "probe_tool") {
		t.Errorf("ListSessionTools(sd1) advertises disabled probe_tool: %v", toolDefNames(listed))
	}
	assertHasEveryCoreTool(t, listed, "ListSessionTools(sd1)")

	merged := f.srv.mergedToolDefinitions(agentProfileTestScope.IncomingContext(context.Background()), "sd1")
	if toolDefinitionsContainName(merged, "probe_tool") {
		t.Errorf("mergedToolDefinitions(sd1) offers disabled probe_tool to the provider: %v", toolDefNames(merged))
	}
	assertHasEveryCoreTool(t, merged, "mergedToolDefinitions(sd1)")

	assertRuntimeDisables(t, f, "sd1", "probe_tool")
}

// The disabled set is a boundary, not a soft default: a tool the session
// registers explicitly (AdditionalTools, or later RegisterSessionTool) stays
// blocked when the profile disabled it. Acceptance check 9 — without this, any
// client could lift a profile's exclusion by registering the tool.
func TestAgentProfile_AdditionalToolDoesNotOverrideDisabledMapping(t *testing.T) {
	f := newAgentProfileFixture(t, staticDisableProfile())
	installCoreToolsOnFixture(t, f)
	ctx := agentProfileCtx()

	createSessionFromProfile(t, f, ctx, &llmv1.CreateSessionFromAgentProfileRequest{
		AgentProfileId:  "static-disable",
		SessionId:       "sd2",
		AdditionalTools: []*llmv1.ToolDefinition{{Name: "probe_tool", Description: "probe (session-registered)"}},
	})

	listed := listSessionTools(t, ctx, f.srv, "sd2")
	if toolDefinitionsContainName(listed, "probe_tool") {
		t.Fatalf("ListSessionTools(sd2) advertises probe_tool although the profile disabled it: %v", toolDefNames(listed))
	}
	if toolDefinitionsContainName(f.srv.mergedToolDefinitions(agentProfileTestScope.IncomingContext(context.Background()), "sd2"), "probe_tool") {
		t.Errorf("mergedToolDefinitions(sd2) offers the disabled probe_tool to the provider")
	}
	assertHasEveryCoreTool(t, listed, "ListSessionTools(sd2) with probe_tool disabled")
	assertRuntimeDisables(t, f, "sd2", "probe_tool")

	// Hidden AND blocked: InvokeTool must refuse it too, or the control is
	// only cosmetic (acceptance check 3).
	if _, err := f.srv.InvokeTool(ctx, &llmv1.InvokeToolRequest{
		ToolId:    "probe_tool",
		SessionId: proto.String("sd2"),
	}); err == nil {
		t.Error("InvokeTool executed probe_tool on a session whose profile disabled it")
	} else if !strings.Contains(err.Error(), "probe_tool") {
		t.Errorf("InvokeTool refusal does not name the tool: %v", err)
	}
}

// A dynamic profile created through CreateAgentProfile with probe_tool
// disabled behaves identically to the static one: the false survives the
// store codec, the session hides the tool, and the session row persists the
// disabled set so a restart restores it.
func TestAgentProfile_DynamicDisabledMappingHidesTool(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	installCoreToolsOnFixture(t, f)
	ctx := agentProfileCtx()

	req := dynamicCreateReq("Dyn Disable")
	req.ToolMapping = []*llmv1.ToolMapping{{ToolName: "probe_tool", Enabled: false}}
	resp, err := f.srv.CreateAgentProfile(ctx, req)
	if err != nil {
		t.Fatalf("CreateAgentProfile(Dyn Disable): %v", err)
	}
	created := resp.GetProfile()
	if len(created.GetToolMapping()) != 1 || created.GetToolMapping()[0].GetEnabled() {
		t.Fatalf("created ToolMapping = %v, want exactly [probe_tool enabled=false]", created.GetToolMapping())
	}
	got, err := f.srv.GetAgentProfile(ctx, &llmv1.GetAgentProfileRequest{Id: created.GetId()})
	if err != nil {
		t.Fatalf("GetAgentProfile(%q): %v", created.GetId(), err)
	}
	if tm := got.GetProfile().GetToolMapping(); len(tm) != 1 || tm[0].GetToolName() != "probe_tool" || tm[0].GetEnabled() {
		t.Fatalf("re-read ToolMapping = %v, want [probe_tool enabled=false] (store codec dropped the false?)", tm)
	}

	createSessionFromProfile(t, f, ctx, &llmv1.CreateSessionFromAgentProfileRequest{
		AgentProfileId: created.GetId(),
		SessionId:      "dd1",
	})
	listed := listSessionTools(t, ctx, f.srv, "dd1")
	if toolDefinitionsContainName(listed, "probe_tool") {
		t.Errorf("ListSessionTools(dd1) advertises disabled probe_tool: %v", toolDefNames(listed))
	}
	assertHasEveryCoreTool(t, listed, "ListSessionTools(dd1)")
	assertRuntimeDisables(t, f, "dd1", "probe_tool")

	row, err := store.NewSessionRepo(f.db).GetSession(ctx, serverutil.Scope{TenantID: agentProfileTestWorkspace}, "dd1")
	if err != nil {
		t.Fatalf("GetSession(dd1): %v", err)
	}
	if row.DisabledToolsJSON == "" {
		t.Fatal("session row dd1 has empty DisabledToolsJSON; the profile's disabled mapping was not persisted")
	}
	names, err := store.UnmarshalDisabledToolsJSON(row.DisabledToolsJSON)
	if err != nil {
		t.Fatalf("UnmarshalDisabledToolsJSON(%q): %v", row.DisabledToolsJSON, err)
	}
	if len(names) != 1 || names[0] != "probe_tool" {
		t.Errorf("persisted disabled tools = %v, want [probe_tool]", names)
	}
}

func assertCoreDisableRejected(t *testing.T, op string, err error) {
	t.Helper()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("%s: code = %v, want InvalidArgument (err=%v)", op, status.Code(err), err)
	}
	if !strings.Contains(err.Error(), "core tools cannot be disabled") {
		t.Errorf("%s: error %q should say core tools cannot be disabled", op, err.Error())
	}
}

// Disabling a core tool is refused at both tiers: the dynamic handlers
// (Create and Update) answer InvalidArgument via agentprofile.Validate —
// which runs before the registered-name check, so it holds even though the
// fixture never registers set_status — and the static Registry refuses to
// register such a profile at all.
func TestAgentProfile_CoreToolCannotBeDisabledAtEitherTier(t *testing.T) {
	f := newAgentProfileFixture(t, staticProfileA())
	ctx := agentProfileCtx()
	coreOff := []*llmv1.ToolMapping{{ToolName: coretools.SetStatus, Enabled: false}}

	req := dynamicCreateReq("Core Off")
	req.ToolMapping = coreOff
	_, err := f.srv.CreateAgentProfile(ctx, req)
	assertCoreDisableRejected(t, "CreateAgentProfile", err)

	list, err := f.srv.ListAgentProfiles(ctx, &llmv1.ListAgentProfilesRequest{})
	if err != nil {
		t.Fatalf("ListAgentProfiles: %v", err)
	}
	if n := len(list.GetProfiles()); n != 1 || list.GetProfiles()[0].GetId() != "static-a" {
		t.Errorf("ListAgentProfiles after rejected create = %d profiles (%v), want only static-a", n, list.GetProfiles())
	}

	created := mustCreateDynamic(t, f, ctx, "Core Update")
	_, err = f.srv.UpdateAgentProfile(ctx, &llmv1.UpdateAgentProfileRequest{Id: created.GetId(), Name: "Core Update", ToolMapping: coreOff})
	assertCoreDisableRejected(t, "UpdateAgentProfile", err)

	err = agentprofile.NewRegistry().Register(agentprofile.Profile{
		ID:          "core-off",
		Name:        "x",
		ToolMapping: []agentprofile.ToolMapping{{ToolName: coretools.SetStatus, Enabled: false}},
	})
	if err == nil {
		t.Fatal("Registry.Register accepted a profile that disables a core tool")
	}
	if !strings.Contains(err.Error(), "core tools cannot be disabled") {
		t.Errorf("Registry.Register error %q should say core tools cannot be disabled", err.Error())
	}
}
