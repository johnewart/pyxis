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
)

// overlayScope is the shared front half of the overlay RPCs: base_id must
// name a static profile and the workspace is required, as for every write.
func (s *LlmServer) overlayScope(ctx context.Context, op, baseID string) (serverutil.Scope, *store.AgentProfileOverlayRepo, *agentprofile.Profile, error) {
	id, err := requireAgentProfileID(baseID)
	if err != nil {
		return serverutil.Scope{}, nil, nil, err
	}
	if s.AgentProfileOverlays == nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s refused: agent profile overlays not configured: profile=%q", op, id)
		return serverutil.Scope{}, nil, nil, status.Error(codes.FailedPrecondition, "agent profile overlays not configured")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s refused: tenant unavailable: profile=%q err=%v", op, id, err)
		return serverutil.Scope{}, nil, nil, err
	}
	if strings.TrimSpace(sc.TenantID) == "" {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s refused: tenant scoping not configured: profile=%q", op, id)
		return serverutil.Scope{}, nil, nil, status.Error(codes.FailedPrecondition, "workspace scoping not configured for agent profile overlays")
	}
	base, err := s.agentProfiles().Base(id)
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s rejected: scope=%s profile=%q err=%v", op, sc.String(), id, err)
		if errors.Is(err, agentprofile.ErrNotStatic) {
			return serverutil.Scope{}, nil, nil, status.Errorf(codes.NotFound, "%q is not a static agent profile; only static profiles take overlays", id)
		}
		return serverutil.Scope{}, nil, nil, status.Errorf(codes.Internal, "%v", err)
	}
	return sc, s.AgentProfileOverlays, base, nil
}

// GetAgentProfileOverlay returns the workspace's overlay for a static profile
// (only base_id set when there is none) and the base it applies to.
func (s *LlmServer) GetAgentProfileOverlay(ctx context.Context, req *llmv1.GetAgentProfileOverlayRequest) (*llmv1.GetAgentProfileOverlayResponse, error) {
	const op = "GetAgentProfileOverlay"
	sc, repo, base, err := s.overlayScope(ctx, op, req.GetBaseId())
	if err != nil {
		return nil, err
	}
	ov, err := repo.Get(ctx, sc, base.ID)
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s failed: scope=%s profile=%q err=%v", op, sc.String(), base.ID, err)
		return nil, status.Errorf(codes.Internal, "agent profile overlay read failed: %v", err)
	}
	if ov == nil {
		ov = &llmv1.AgentProfileOverlay{BaseId: base.ID}
	}
	return &llmv1.GetAgentProfileOverlayResponse{Overlay: ov, Base: base.ToProto()}, nil
}

// SetAgentProfileOverlay replaces the workspace's overlay wholesale. The
// merged profile must validate, its tool names must be known to the
// workspace, and a provider it names must be a valid binding.
func (s *LlmServer) SetAgentProfileOverlay(ctx context.Context, req *llmv1.SetAgentProfileOverlayRequest) (*llmv1.SetAgentProfileOverlayResponse, error) {
	const op = "SetAgentProfileOverlay"
	in := req.GetOverlay()
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "overlay is required")
	}
	sc, repo, base, err := s.overlayScope(ctx, op, in.GetBaseId())
	if err != nil {
		return nil, err
	}
	merged, err := agentprofile.ApplyOverlay(base, in)
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s rejected: scope=%s profile=%q err=%v", op, sc.String(), base.ID, err)
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	// Only the tools the overlay names are checked: the base's own mapping
	// was valid when the host registered it.
	var named []agentprofile.ToolMapping
	for _, t := range in.GetToolOverrides() {
		if t != nil {
			named = append(named, agentprofile.ToolMapping{ToolName: t.GetToolName(), Enabled: t.GetEnabled()})
		}
	}
	if err := validateToolMappingNames(named, s.tenantTools(sc.TenantID)); err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s rejected: scope=%s profile=%q err=%v", op, sc.String(), base.ID, err)
		return nil, err
	}
	if in.LlmProviderId != nil && strings.TrimSpace(in.GetLlmProviderId()) != "" {
		if err := s.validateAgentLlmBinding(ctx, in.GetLlmProviderId()); err != nil {
			llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
				"%s rejected: provider binding: scope=%s profile=%q err=%v", op, sc.String(), base.ID, err)
			return nil, err
		}
	}
	stored, err := repo.Set(ctx, sc, in)
	if err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s failed: scope=%s profile=%q err=%v", op, sc.String(), base.ID, err)
		return nil, status.Errorf(codes.Internal, "agent profile overlay write failed: %v", err)
	}
	return &llmv1.SetAgentProfileOverlayResponse{Overlay: stored, Profile: merged.ToProto()}, nil
}

// ResetAgentProfileOverlay removes the overlay; resetting a profile with no
// overlay succeeds and returns the base.
func (s *LlmServer) ResetAgentProfileOverlay(ctx context.Context, req *llmv1.ResetAgentProfileOverlayRequest) (*llmv1.ResetAgentProfileOverlayResponse, error) {
	const op = "ResetAgentProfileOverlay"
	sc, repo, base, err := s.overlayScope(ctx, op, req.GetBaseId())
	if err != nil {
		return nil, err
	}
	if _, err := repo.Delete(ctx, sc, base.ID); err != nil {
		llmdlog.Stdout(agentProfileLogModule, llmdlog.LevelWarn,
			"%s failed: scope=%s profile=%q err=%v", op, sc.String(), base.ID, err)
		return nil, status.Errorf(codes.Internal, "agent profile overlay delete failed: %v", err)
	}
	return &llmv1.ResetAgentProfileOverlayResponse{Profile: base.ToProto()}, nil
}
