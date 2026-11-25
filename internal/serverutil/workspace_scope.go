package serverutil

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// MetadataWorkspaceIDKey is the gRPC metadata key carrying the caller's workspace id.
const MetadataWorkspaceIDKey = "llmd-workspace-id"

// WorkspaceIDFromMetadata returns the workspace id carried in incoming gRPC metadata, or "".
func WorkspaceIDFromMetadata(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	v := md.Get(MetadataWorkspaceIDKey)
	if len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(v[0])
}

// RequireWorkspaceID returns the llmd-workspace-id from gRPC metadata; it errors if absent.
// There is no server-wide default workspace: every RPC must carry its workspace explicitly.
func RequireWorkspaceID(ctx context.Context) (string, error) {
	wid := WorkspaceIDFromMetadata(ctx)
	if wid == "" {
		return "", status.Error(codes.Unauthenticated, "set grpc metadata "+MetadataWorkspaceIDKey)
	}
	return wid, nil
}
