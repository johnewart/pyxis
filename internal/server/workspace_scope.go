package server

import (
	"context"

	"github.com/johnewart/pyxis/internal/serverutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// metadataWorkspaceIDKey, workspaceIDFromMetadata and RequireWorkspaceID now live in
// serverutil; these aliases keep every existing call site in this package compiling unchanged.
const metadataWorkspaceIDKey = serverutil.MetadataWorkspaceIDKey

var (
	workspaceIDFromMetadata = serverutil.WorkspaceIDFromMetadata
	// RequireWorkspaceID returns the llmd-workspace-id from gRPC metadata; it errors if absent.
	RequireWorkspaceID = serverutil.RequireWorkspaceID
)

// dbWorkspaceID resolves the workspace id for the current RPC when a session manager is configured.
func (s *LlmServer) dbWorkspaceID(ctx context.Context) (string, error) {
	if s.SessionRuntimes == nil {
		return "", nil
	}
	wid := workspaceIDFromMetadata(ctx)
	if wid == "" {
		return "", status.Error(codes.Unauthenticated, "set grpc metadata "+metadataWorkspaceIDKey)
	}
	return wid, nil
}
