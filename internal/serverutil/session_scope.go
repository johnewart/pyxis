package serverutil

import (
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RequireSessionID trims sessionID and errors if it is empty.
func RequireSessionID(sessionID string) (string, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return "", status.Error(codes.InvalidArgument, "session_id is required")
	}
	return sid, nil
}
