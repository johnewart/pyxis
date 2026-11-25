package serverutil

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestWorkspaceIDFromMetadata(t *testing.T) {
	t.Parallel()

	if got := WorkspaceIDFromMetadata(context.Background()); got != "" {
		t.Fatalf("no metadata: got %q, want empty", got)
	}

	md := metadata.Pairs(MetadataWorkspaceIDKey, " ws-1 ")
	ctx := metadata.NewIncomingContext(context.Background(), md)
	if got := WorkspaceIDFromMetadata(ctx); got != "ws-1" {
		t.Fatalf("with metadata: got %q, want ws-1", got)
	}
}

func TestRequireWorkspaceID(t *testing.T) {
	t.Parallel()

	if _, err := RequireWorkspaceID(context.Background()); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing metadata: got code %v, want Unauthenticated", status.Code(err))
	}

	md := metadata.Pairs(MetadataWorkspaceIDKey, "ws-2")
	ctx := metadata.NewIncomingContext(context.Background(), md)
	wid, err := RequireWorkspaceID(ctx)
	if err != nil || wid != "ws-2" {
		t.Fatalf("present metadata: wid=%q err=%v", wid, err)
	}
}
