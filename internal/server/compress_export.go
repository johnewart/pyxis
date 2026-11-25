package server

// CompressToolResultsForSend and ClearSessionDedup are thin exported wrappers
// over the unexported compressToolResultsForSend/clearSessionDedup, kept so
// TestCompressToolResultsForSend_* (moved to llmd/internal/serve alongside
// the rest of the serving shell in phase 4) can drive them from outside this
// package. No logic lives here — see server.go.

import (
	"context"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/toolresult"
)

// CompressToolResultsForSend is the exported form of compressToolResultsForSend.
func (s *LlmServer) CompressToolResultsForSend(ctx context.Context, aid, sid string, msgs []*llmv1.Message, settings toolresult.Settings) []*llmv1.Message {
	return s.compressToolResultsForSend(ctx, aid, sid, msgs, settings)
}

// ClearSessionDedup is the exported form of clearSessionDedup.
func (s *LlmServer) ClearSessionDedup(sessionID string) {
	s.clearSessionDedup(sessionID)
}
