// Package workspacescope exports the workspace-resolution helpers used by knowledge RPCs
// so that modules outside this repo's internal tree can resolve
// the caller's workspace id without reaching into llmd/internal.
package workspacescope

import (
	"context"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// MetadataWorkspaceIDKey is the gRPC metadata key carrying the caller's workspace id.
const MetadataWorkspaceIDKey = serverutil.MetadataWorkspaceIDKey

// WorkspaceIDFromMetadata returns the workspace id carried in incoming gRPC metadata, or "".
func WorkspaceIDFromMetadata(ctx context.Context) string {
	return serverutil.WorkspaceIDFromMetadata(ctx)
}

// RequireWorkspaceID returns the llmd-workspace-id from gRPC metadata; it errors if absent.
// There is no server-wide default workspace: every RPC must carry its workspace explicitly.
func RequireWorkspaceID(ctx context.Context) (string, error) {
	return serverutil.RequireWorkspaceID(ctx)
}
