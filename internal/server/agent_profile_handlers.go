package server

import (
	"context"
	"errors"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/agentprofile"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// agentProfileLogModule is the llmdlog module tag for every profile RPC.
const agentProfileLogModule = "agentprofile"

// requireAgentProfileID trims id and refuses an empty one with InvalidArgument,
// mirroring requireSessionID.
func requireAgentProfileID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", status.Error(codes.InvalidArgument, "agent_profile_id is required")
	}
	return id, nil
}

// profileScope resolves the scope for a profile lookup. Unlike session RPCs,
// the static tier is served without a tenant, so the error is returned
// alongside a zero Scope and the caller decides whether it matters (only when
// the static tier turns up nothing).
func (s *LlmServer) profileScope(ctx context.Context) (serverutil.Scope, error) {
	sc, err := s.scope(ctx)
	if err != nil {
		return serverutil.Scope{}, err
	}
	return sc, nil
}

// resolveAgentProfile looks id up across both tiers. When the scope is
// unavailable (uidErr != nil, sc is zero) only the static tier is consulted,
// and a miss surfaces the original scope error rather than NotFound so a
// caller who forgot the metadata learns that instead of "no such profile".
func (s *LlmServer) resolveAgentProfile(ctx context.Context, sc serverutil.Scope, uidErr error, id string) (*agentprofile.Profile, error) {
	p, err := s.agentProfiles().Get(ctx, sc, id)
	if err == nil {
		return p, nil
	}
	if errors.Is(err, agentprofile.ErrNotFound) {
		if uidErr != nil {
			llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
				"profile lookup: not in static tier and scope unavailable: profile=%q err=%v", id, uidErr)
			return nil, uidErr
		}
		return nil, status.Errorf(codes.NotFound, "unknown agent_profile_id %q", id)
	}
	llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
		"profile lookup failed: profile=%q scope=%+v err=%v", id, sc, err)
	return nil, status.Errorf(codes.Internal, "agent profile lookup failed: %v", err)
}

// GetAgentProfile returns one profile by id, static tier first.
func (s *LlmServer) GetAgentProfile(ctx context.Context, req *llmv1.GetAgentProfileRequest) (*llmv1.GetAgentProfileResponse, error) {
	id, err := requireAgentProfileID(req.GetId())
	if err != nil {
		return nil, err
	}
	psc, uidErr := s.profileScope(ctx)
	p, err := s.resolveAgentProfile(ctx, psc, uidErr, id)
	if err != nil {
		return nil, err
	}
	return &llmv1.GetAgentProfileResponse{Profile: p.ToProto()}, nil
}

// ListAgentProfiles returns the static profiles followed by the workspace's
// dynamic ones. Without workspace metadata the static tier is still listed;
// the workspace error is surfaced only when there is nothing static to show.
func (s *LlmServer) ListAgentProfiles(ctx context.Context, _ *llmv1.ListAgentProfilesRequest) (*llmv1.ListAgentProfilesResponse, error) {
	psc, uidErr := s.profileScope(ctx)
	profiles, err := s.agentProfiles().List(ctx, psc)
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"profile list failed: scope=%s err=%v", psc.String(), err)
		return nil, status.Errorf(codes.Internal, "agent profile list failed: %v", err)
	}
	if len(profiles) == 0 && uidErr != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"profile list: static tier empty and scope unavailable: err=%v", uidErr)
		return nil, uidErr
	}
	out := make([]*llmv1.AgentProfile, 0, len(profiles))
	for _, p := range profiles {
		if p == nil {
			continue
		}
		out = append(out, p.ToProto())
	}
	return &llmv1.ListAgentProfilesResponse{Profiles: out}, nil
}

// agentProfileWriteScope is the shared front half of the CRUD RPCs. Writes
// are workspace-scoped rows, so — unlike Get/List, which tolerate a missing
// workspace for the static tier — the workspace id is REQUIRED here:
// s.scope's Unauthenticated error propagates unchanged, and a blank
// workspace (SessionRuntimes not wired) is FailedPrecondition so a mis-wired
// host can never write rows under an empty workspace id. A missing dynamic
// store (no AgentProfileRepo, or an injected resolver with no Dynamic tier)
// also fails closed with FailedPrecondition.
func (s *LlmServer) agentProfileWriteScope(ctx context.Context, op, id string) (serverutil.Scope, *store.AgentProfileRepo, error) {
	if s.agentProfiles().Dynamic == nil || s.AgentProfileRepo == nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s refused: dynamic agent profiles not configured: profile=%q", op, id)
		return serverutil.Scope{}, nil, status.Error(codes.FailedPrecondition, "dynamic agent profiles not configured")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s refused: tenant unavailable: profile=%q err=%v", op, id, err)
		return serverutil.Scope{}, nil, err
	}
	if strings.TrimSpace(sc.TenantID) == "" {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s refused: tenant scoping not configured (SessionRuntimes is nil): profile=%q", op, id)
		return serverutil.Scope{}, nil, status.Error(codes.FailedPrecondition, "workspace scoping not configured for agent profile writes")
	}
	return sc, s.AgentProfileRepo, nil
}

// profileFromAgentProfileFields builds the in-memory profile from the shared
// Create/Update request fields. Strings are trimmed, sections are cloned so
// the request buffer is not aliased, and every tool mapping is copied as-is —
// an Enabled=false entry is meaningful (it blocks a server-wide default) and
// must never be dropped. The tool policy is copied through as given; Validate
// rejects a value this build does not know rather than defaulting it to the
// permissive INHERIT_SERVER_WIDE.
func profileFromAgentProfileFields(id, name, description string, sections []*llmv1.SystemPromptSection, providerID, model *string, memoryEnabled bool, mapping []*llmv1.ToolMapping, policy llmv1.ToolPolicy) *agentprofile.Profile {
	return agentprofile.FromFields(id, name, description, sections, providerID, model, memoryEnabled, mapping, policy)
}

// validateToolMappingNames rejects the first mapping whose tool is not in
// known (the caller tenant's tools, see tenantTools) with InvalidArgument. Structural rules
// (blank/duplicate names, disabled core tools) are agentprofile.Validate's.
func validateToolMappingNames(mapping []agentprofile.ToolMapping, known []*llmv1.ToolDefinition) error {
	if len(mapping) == 0 {
		return nil
	}
	byName := toolDefByName(known)
	for _, m := range mapping {
		name := strings.TrimSpace(m.ToolName)
		if _, ok := byName[name]; !ok {
			return status.Errorf(codes.InvalidArgument, "unknown tool %q", name)
		}
	}
	return nil
}

// validateAgentProfileWrite runs the checks shared by Create and Update:
// structural validation, tool names against the registry and (when set) the
// provider binding. Every failure is logged and returned as a gRPC status.
func (s *LlmServer) validateAgentProfileWrite(ctx context.Context, op string, sc serverutil.Scope, p *agentprofile.Profile, providerID *string) error {
	if err := agentprofile.Validate(p); err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s rejected: invalid profile: scope=%s profile=%q name=%q err=%v", op, sc.String(), p.ID, p.Name, err)
		return status.Errorf(codes.InvalidArgument, "%v", err)
	}
	// Same set ListTools reports for this tenant: host tools plus the tenant's
	// executor tools, so a profile can map a tool served over the executor
	// protocol.
	if err := validateToolMappingNames(p.ToolMapping, s.tenantTools(sc.TenantID)); err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s rejected: scope=%s profile=%q name=%q err=%v", op, sc.String(), p.ID, p.Name, err)
		return err
	}
	if providerID != nil && strings.TrimSpace(*providerID) != "" {
		if err := s.validateAgentLlmBinding(ctx, *providerID); err != nil {
			llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
				"%s rejected: provider binding: scope=%s profile=%q name=%q err=%v", op, sc.String(), p.ID, p.Name, err)
			return err
		}
	}
	return nil
}

// agentProfileRepoStatus maps a repo write error to its gRPC status:
// name collision → AlreadyExists, missing row → NotFound, anything else →
// Internal.
func agentProfileRepoStatus(err error) error {
	switch {
	case errors.Is(err, store.ErrAgentProfileNameTaken):
		return status.Errorf(codes.AlreadyExists, "%v", err)
	case errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, agentprofile.ErrNotFound):
		return status.Errorf(codes.NotFound, "%v", err)
	default:
		return status.Errorf(codes.Internal, "agent profile write failed: %v", err)
	}
}

// CreateAgentProfile inserts a dynamic profile into the caller's workspace.
// The id is optional: a blank one is assigned by the repo. A static id is
// refused with AlreadyExists (the proto promises AlreadyExists for any taken
// id, static ones included).
func (s *LlmServer) CreateAgentProfile(ctx context.Context, req *llmv1.CreateAgentProfileRequest) (*llmv1.CreateAgentProfileResponse, error) {
	const op = "CreateAgentProfile"
	id := ""
	if req.Id != nil {
		id = strings.TrimSpace(req.GetId())
	}
	sc, repo, err := s.agentProfileWriteScope(ctx, op, id)
	if err != nil {
		return nil, err
	}
	if id != "" && s.agentProfiles().IsStatic(id) {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s rejected: id is statically registered: scope=%s profile=%q", op, sc.String(), id)
		return nil, status.Errorf(codes.AlreadyExists, "profile %q is statically registered", id)
	}
	p := profileFromAgentProfileFields(id, req.GetName(), req.GetDescription(), req.GetSystemPromptSections(),
		req.LlmProviderId, req.DefaultModel, req.GetMemoryEnabled(), req.GetToolMapping(), req.GetToolPolicy())
	// Validate demands a non-blank id; the repo assigns the uuid only on
	// Create, so validate a copy carrying a placeholder when none was given.
	check := *p
	if check.ID == "" {
		check.ID = "(pending)"
	}
	if err := s.validateAgentProfileWrite(ctx, op, sc, &check, req.LlmProviderId); err != nil {
		return nil, err
	}
	if err := repo.Create(ctx, sc, p); err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s failed: scope=%s profile=%q name=%q err=%v", op, sc.String(), p.ID, p.Name, err)
		return nil, agentProfileRepoStatus(err)
	}
	// Return the row as stored (as UpdateAgentProfile does), not the request:
	// the store normalizes fields such as an unspecified tool policy, and the
	// response must match what Get and List return for the same profile.
	stored, err := repo.Get(ctx, sc, p.ID)
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s: re-read after create failed: scope=%s profile=%q err=%v", op, sc.String(), p.ID, err)
		return nil, agentProfileRepoStatus(err)
	}
	return &llmv1.CreateAgentProfileResponse{Profile: stored.ToProto()}, nil
}

// UpdateAgentProfile replaces a dynamic profile wholesale (like
// UpdateLlmProviderConfig): fields omitted from the request become empty.
func (s *LlmServer) UpdateAgentProfile(ctx context.Context, req *llmv1.UpdateAgentProfileRequest) (*llmv1.UpdateAgentProfileResponse, error) {
	const op = "UpdateAgentProfile"
	id, err := requireAgentProfileID(req.GetId())
	if err != nil {
		return nil, err
	}
	sc, repo, err := s.agentProfileWriteScope(ctx, op, id)
	if err != nil {
		return nil, err
	}
	if s.agentProfiles().IsStatic(id) {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s rejected: static profile is read-only: scope=%s profile=%q", op, sc.String(), id)
		return nil, status.Errorf(codes.InvalidArgument, "profile %q is statically registered and read-only", id)
	}
	p := profileFromAgentProfileFields(id, req.GetName(), req.GetDescription(), req.GetSystemPromptSections(),
		req.LlmProviderId, req.DefaultModel, req.GetMemoryEnabled(), req.GetToolMapping(), req.GetToolPolicy())
	if err := s.validateAgentProfileWrite(ctx, op, sc, p, req.LlmProviderId); err != nil {
		return nil, err
	}
	if err := repo.Update(ctx, sc, p); err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s failed: scope=%s profile=%q name=%q err=%v", op, sc.String(), id, p.Name, err)
		return nil, agentProfileRepoStatus(err)
	}
	stored, err := repo.Get(ctx, sc, id)
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s: re-read after update failed: scope=%s profile=%q err=%v", op, sc.String(), id, err)
		return nil, agentProfileRepoStatus(err)
	}
	return &llmv1.UpdateAgentProfileResponse{Profile: stored.ToProto()}, nil
}

// DeleteAgentProfile removes a dynamic profile; a static id is refused with
// InvalidArgument and a missing row is NotFound.
func (s *LlmServer) DeleteAgentProfile(ctx context.Context, req *llmv1.DeleteAgentProfileRequest) (*llmv1.DeleteAgentProfileResponse, error) {
	const op = "DeleteAgentProfile"
	id, err := requireAgentProfileID(req.GetId())
	if err != nil {
		return nil, err
	}
	sc, repo, err := s.agentProfileWriteScope(ctx, op, id)
	if err != nil {
		return nil, err
	}
	if s.agentProfiles().IsStatic(id) {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s rejected: static profile is read-only: scope=%s profile=%q", op, sc.String(), id)
		return nil, status.Errorf(codes.InvalidArgument, "profile %q is statically registered and read-only", id)
	}
	ok, err := repo.Delete(ctx, sc, id)
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s failed: scope=%s profile=%q err=%v", op, sc.String(), id, err)
		return nil, status.Errorf(codes.Internal, "agent profile delete failed: %v", err)
	}
	if !ok {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s: no such profile: scope=%s profile=%q", op, sc.String(), id)
		return nil, status.Errorf(codes.NotFound, "unknown agent_profile_id %q", id)
	}
	return &llmv1.DeleteAgentProfileResponse{}, nil
}

// CreateSessionFromAgentProfile materializes a profile (plus per-session
// overrides) into an EnsureSessionRequest and hands it to the unmodified
// EnsureSession handler, so provider validation, workspace scoping and
// persistence stay in exactly one place. EnsureSession's gRPC status is
// returned unchanged.
func (s *LlmServer) CreateSessionFromAgentProfile(ctx context.Context, req *llmv1.CreateSessionFromAgentProfileRequest) (*llmv1.CreateSessionFromAgentProfileResponse, error) {
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	id, err := requireAgentProfileID(req.GetAgentProfileId())
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"create session from profile: missing profile id: session=%q err=%v", sid, err)
		return nil, err
	}
	psc, uidErr := s.profileScope(ctx)
	profile, err := s.resolveAgentProfile(ctx, psc, uidErr, id)
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"create session from profile: resolve failed: profile=%q session=%q err=%v", id, sid, err)
		return nil, err
	}
	ov := agentprofile.SessionOverrides{
		SessionID:       sid,
		DisplayName:     req.GetDisplayName(),
		ChatSurface:     req.GetChatSurface(),
		LlmProviderID:   req.LlmProviderIdOverride,
		DefaultModel:    req.DefaultModelOverride,
		AdditionalTools: req.GetAdditionalTools(),
	}
	// Resolve mappings against the same tenant tool set the profile was
	// validated against, so enabled executor tools land in InitialTools.
	// Without a tenant scope only host tools are known.
	tenant := ""
	if uidErr == nil {
		tenant = psc.TenantID
	}
	ensureReq, err := agentprofile.BuildEnsureSessionRequest(profile, ov, s.tenantTools(tenant))
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"create session from profile: materialize failed: profile=%q session=%q err=%v", profile.ID, sid, err)
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	if _, err := s.EnsureSession(ctx, ensureReq); err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"create session from profile: EnsureSession failed: profile=%q session=%q err=%v", profile.ID, sid, err)
		return nil, err
	}
	return &llmv1.CreateSessionFromAgentProfileResponse{SessionId: sid, AgentProfileId: profile.ID}, nil
}
