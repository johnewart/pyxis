package server

import (
	"context"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SessionCacheServer implements the generic session-scoped K/V cache (SessionCacheService).
// It stores opaque bytes keyed by (workspace, session, namespace, key); llmd never interprets
// values. Tools (e.g. read_code range-coverage tracking) use it to remember derivable state
// across calls within a session. It is intentionally a separate, small server (not LlmServer)
// so it has no shared mutable state to coordinate — both the in-process and CLI/MCP tool paths
// reach the same rows simply by going through the same DB-backed repo.
type SessionCacheServer struct {
	llmv1.UnimplementedSessionCacheServiceServer
	Repo *store.SessionCacheRepo
}

// Guardrail: SessionCacheService must be fully implemented.
var _ llmv1.SessionCacheServiceServer = (*SessionCacheServer)(nil)

func (s *SessionCacheServer) validate(sessionID, namespace string) (string, string, error) {
	sid := strings.TrimSpace(sessionID)
	ns := strings.TrimSpace(namespace)
	if sid == "" {
		return "", "", status.Error(codes.InvalidArgument, "session_id is required")
	}
	if ns == "" {
		return "", "", status.Error(codes.InvalidArgument, "namespace is required")
	}
	if s.Repo == nil {
		return "", "", status.Error(codes.FailedPrecondition, "session cache not configured")
	}
	return sid, ns, nil
}

func (s *SessionCacheServer) Get(ctx context.Context, req *llmv1.SessionCacheGetRequest) (*llmv1.SessionCacheGetResponse, error) {
	sid, ns, err := s.validate(req.GetSessionId(), req.GetNamespace())
	if err != nil {
		return nil, err
	}
	sc, err := serverutil.ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	val, found, err := s.Repo.Get(ctx, sc, sid, ns, req.GetKey())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "session cache get: %v", err)
	}
	return &llmv1.SessionCacheGetResponse{Value: val, Found: found}, nil
}

func (s *SessionCacheServer) Set(ctx context.Context, req *llmv1.SessionCacheSetRequest) (*llmv1.SessionCacheSetResponse, error) {
	sid, ns, err := s.validate(req.GetSessionId(), req.GetNamespace())
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(req.GetKey())
	if key == "" {
		return nil, status.Error(codes.InvalidArgument, "key is required")
	}
	sc, err := serverutil.ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Repo.Set(ctx, sc, sid, ns, key, req.GetValue()); err != nil {
		return nil, status.Errorf(codes.Internal, "session cache set: %v", err)
	}
	return &llmv1.SessionCacheSetResponse{Success: true}, nil
}

func (s *SessionCacheServer) Delete(ctx context.Context, req *llmv1.SessionCacheDeleteRequest) (*llmv1.SessionCacheDeleteResponse, error) {
	sid, ns, err := s.validate(req.GetSessionId(), req.GetNamespace())
	if err != nil {
		return nil, err
	}
	sc, err := serverutil.ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	n, err := s.Repo.Delete(ctx, sc, sid, ns, req.GetKey())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "session cache delete: %v", err)
	}
	return &llmv1.SessionCacheDeleteResponse{Deleted: n}, nil
}
