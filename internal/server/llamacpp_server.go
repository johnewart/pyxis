/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package server

import (
	"context"

	llamacppv1 "github.com/johnewart/pyxis/gen/llamacpp/v1"
	"github.com/johnewart/pyxis/internal/llamacpp"
	"github.com/johnewart/pyxis/internal/serverutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// LlamaCppServer exposes llmd's managed llama.cpp runtime.
//
// llmd is authoritative for everything derived from the live process: which
// binary was resolved, which GGUFs exist on disk, download progress, the
// lifecycle phase and which model is actually loaded. A client is a
// pass-through caller — it renders what this service reports and never
// recomputes a phase or a readiness verdict from its own cached state.
//
// Start/Stop/Status/StatusStream delegate to internal/llamacpp; the remaining
// provisioning RPCs still fall through to UnimplementedLlamaCppServiceServer.
// This type stays a thin mapping layer — no lifecycle logic lives in the
// handler.
type LlamaCppServer struct {
	llamacppv1.UnimplementedLlamaCppServiceServer

	svc *llamacpp.Service
}

// NewLlamaCppServer wires the handler to a lifecycle service. A nil service
// leaves the lifecycle RPCs unavailable rather than panicking.
func NewLlamaCppServer(svc *llamacpp.Service) *LlamaCppServer {
	return &LlamaCppServer{svc: svc}
}

var phaseToProto = map[llamacpp.Phase]llamacppv1.LlamaPhase{
	llamacpp.PhaseIdle:          llamacppv1.LlamaPhase_LLAMA_PHASE_IDLE,
	llamacpp.PhaseStarting:      llamacppv1.LlamaPhase_LLAMA_PHASE_STARTING,
	llamacpp.PhaseWaitingHealth: llamacppv1.LlamaPhase_LLAMA_PHASE_WAITING_HEALTH,
	llamacpp.PhaseLoadingModel:  llamacppv1.LlamaPhase_LLAMA_PHASE_LOADING_MODEL,
	llamacpp.PhaseReady:         llamacppv1.LlamaPhase_LLAMA_PHASE_READY,
	llamacpp.PhaseStopping:      llamacppv1.LlamaPhase_LLAMA_PHASE_STOPPING,
	llamacpp.PhaseError:         llamacppv1.LlamaPhase_LLAMA_PHASE_ERROR,
}

// toProtoStatus maps llmd's authoritative snapshot onto the wire verbatim.
func toProtoStatus(in llamacpp.Status) *llamacppv1.LlamaStatus {
	phase, ok := phaseToProto[in.Phase]
	if !ok {
		phase = llamacppv1.LlamaPhase_LLAMA_PHASE_UNSPECIFIED
	}
	return &llamacppv1.LlamaStatus{
		Phase:         phase,
		Message:       in.Message,
		ModelId:       in.ModelID,
		ModelDir:      in.ModelDir,
		BinaryPath:    in.BinaryPath,
		Port:          int32(in.Port),
		BaseUrl:       in.BaseURL,
		Pid:           int32(in.PID),
		TenantId:      in.Scope.TenantID,
		ScopeId:       in.Scope.ScopeID,
		Enabled:       in.Enabled,
		ActiveModelId: in.ActiveModelID,
		Error:         in.Error,
	}
}

func (s *LlamaCppServer) service() (*llamacpp.Service, error) {
	if s == nil || s.svc == nil {
		return nil, status.Error(codes.Unavailable, "managed llama.cpp runtime is not configured")
	}
	return s.svc, nil
}

// Start spawns (or replaces) the single managed llama-server and returns the
// terminal status llmd measured.
func (s *LlamaCppServer) Start(ctx context.Context, req *llamacppv1.StartRequest) (*llamacppv1.LlamaStatus, error) {
	svc, err := s.service()
	if err != nil {
		return nil, err
	}
	sc, err := serverutil.ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	out, err := svc.Start(ctx, llamacpp.StartSpec{
		ModelID:    req.GetModelId(),
		ModelDir:   req.GetModelDir(),
		BinaryPath: req.GetBinaryPath(),
		Port:       int(req.GetPort()),
		ExtraArgs:  req.GetExtraArgs(),
		Scope:      sc,
	})
	if err != nil {
		// The error status is still llmd's verdict; return it alongside the
		// failure so callers can render the phase rather than guess.
		return toProtoStatus(out), status.Error(codes.FailedPrecondition, err.Error())
	}
	return toProtoStatus(out), nil
}

// Stop terminates the managed llama-server.
func (s *LlamaCppServer) Stop(ctx context.Context, _ *llamacppv1.StopRequest) (*llamacppv1.LlamaStatus, error) {
	svc, err := s.service()
	if err != nil {
		return nil, err
	}
	out, stopErr := svc.Stop(ctx)
	if stopErr != nil {
		return toProtoStatus(out), status.Error(codes.Internal, stopErr.Error())
	}
	return toProtoStatus(out), nil
}

// SetManagedState flips the app-global managed toggles. Only the fields the
// caller actually set are applied; the returned status is llmd's own snapshot
// after the change, never a client-side projection of the request.
func (s *LlamaCppServer) SetManagedState(_ context.Context, req *llamacppv1.SetManagedStateRequest) (*llamacppv1.LlamaStatus, error) {
	svc, err := s.service()
	if err != nil {
		return nil, err
	}
	if req.ActiveModelId != nil {
		if _, err := svc.SetActiveModel(req.GetActiveModelId()); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	if req.Enabled != nil {
		if _, err := svc.SetEnabled(req.GetEnabled()); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	return toProtoStatus(svc.Status()), nil
}

// Status returns the current snapshot.
func (s *LlamaCppServer) Status(_ context.Context, _ *llamacppv1.StatusRequest) (*llamacppv1.LlamaStatus, error) {
	svc, err := s.service()
	if err != nil {
		return nil, err
	}
	return toProtoStatus(svc.Status()), nil
}

// StatusStream pushes a snapshot on every lifecycle transition until the client
// goes away. It is newest-state fan-out, not an event log.
func (s *LlamaCppServer) StatusStream(_ *llamacppv1.StatusStreamRequest, stream llamacppv1.LlamaCppService_StatusStreamServer) error {
	svc, err := s.service()
	if err != nil {
		return err
	}
	updates, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case snapshot, ok := <-updates:
			if !ok {
				return nil
			}
			if err := stream.Send(toProtoStatus(snapshot)); err != nil {
				return err
			}
		}
	}
}

// Guardrail: adding an RPC to the proto without implementing it here fails the
// build rather than silently going missing.
var _ llamacppv1.LlamaCppServiceServer = (*LlamaCppServer)(nil)
