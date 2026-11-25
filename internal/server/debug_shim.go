package server

// This file is a thin alias over llmd/internal/serve, kept so the many call
// sites left in this package (server.go, readonly_tools_test.go, etc.)
// compile unchanged after the debug logging/interceptor shell moved to
// serve in phase 4 of the llmd/internal/serve extraction. New code should
// call the serve package directly; this shim is a deferred-cleanup target
// once internal/server is renamed/retired (phase 5).

import (
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serve"
)

// DebugEnabled reports whether LLMD_DEBUG is on. Alias over serve.DebugEnabled.
func DebugEnabled() bool { return serve.DebugEnabled() }

// ChatDebugEnabled reports whether LLMD_DEBUG_CHAT (or a chat debug file) is
// on. Alias over serve.ChatDebugEnabled.
func ChatDebugEnabled() bool { return serve.ChatDebugEnabled() }

// TrafficDebugEnabled reports whether LLMD_DEBUG_TRAFFIC is on. Alias over
// serve.TrafficDebugEnabled.
func TrafficDebugEnabled() bool { return serve.TrafficDebugEnabled() }

func debugLogf(format string, args ...any) { serve.DebugLogf(format, args...) }

// debugChatf records a named chat debug event through the same sink.
func debugChatf(event, format string, args ...any) { serve.DebugChatf(event, format, args...) }

func debugLogCallOptions(rpc, agentID, sessionID string, wire, effective *llmv1.CallOptions) {
	serve.DebugLogCallOptions(rpc, agentID, sessionID, wire, effective)
}

func toolDefNames(defs []*llmv1.ToolDefinition) []string { return serve.ToolDefNames(defs) }

func mutationToolNamesFiltered(all, kept []*llmv1.ToolDefinition) []string {
	return serve.MutationToolNamesFiltered(all, kept)
}

func cursorModeForReadOnly(readOnly bool) string { return serve.CursorModeForReadOnly(readOnly) }

func truncateRunes(s string, maxBytes int) string { return serve.TruncateRunes(s, maxBytes) }

// debugMaxSystemPromptBytes caps chat-debug dumps of assembled system prompt text.
const debugMaxSystemPromptBytes = 262144

// DebugUnaryInterceptor is an alias over serve.DebugUnaryInterceptor, kept for
// existing callers (e.g. main.go before it is repointed at serve directly).
var DebugUnaryInterceptor = serve.DebugUnaryInterceptor

// DebugStreamInterceptor is an alias over serve.DebugStreamInterceptor.
var DebugStreamInterceptor = serve.DebugStreamInterceptor
