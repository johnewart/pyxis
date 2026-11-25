package server

import (
	"context"
	"strings"

	"github.com/google/uuid"
	claudecodeclillm "github.com/johnewart/pyxis/internal/llm/claudecode"
	cursorclillm "github.com/johnewart/pyxis/internal/llm/cursorcli"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
)

// statefulSyncPlan describes how much transcript to send to a stateful CLI backend.
type statefulSyncPlan struct {
	resumeID   string
	watermark  int
	fullResync bool
}

// resolveStatefulProviderSync decides resume id and transcript watermark for Cursor / Claude Code v2.
// Any fingerprint change (including workspace.rules updates) triggers a full resync so the new
// system prompt is guaranteed to reach the provider. For Claude Code v2 full resync means no
// --resume (watermark=0, resumeID=""); Claude Code replays the full transcript from stdin.
func resolveStatefulProviderSync(
	kind providerkind.Kind,
	prev *store.ProviderSessionSync,
	sysFP string,
	histMsgCount int,
	fullMsgCount int,
) statefulSyncPlan {
	transcriptStable := prev != nil && prev.SyncedMessageCount == histMsgCount
	hasBackend := prev != nil && strings.TrimSpace(prev.BackendSessionID) != ""
	fpChanged := prev != nil && prev.SystemFingerprint != sysFP

	needsTranscriptResync := prev == nil ||
		!transcriptStable ||
		!hasBackend ||
		prev.SyncedMessageCount > fullMsgCount

	if !needsTranscriptResync && !fpChanged {
		return statefulSyncPlan{
			resumeID:   strings.TrimSpace(prev.BackendSessionID),
			watermark:  prev.SyncedMessageCount,
			fullResync: false,
		}
	}

	if kind == providerkind.ClaudeCodeV2 {
		return statefulSyncPlan{resumeID: "", watermark: 0, fullResync: true}
	}
	return statefulSyncPlan{resumeID: uuid.New().String(), watermark: 0, fullResync: true}
}

// isStaleProviderResumeError reports whether a backend error means the CLI no
// longer has the conversation referenced by our stored resume id (e.g. the
// conversation store diverged between two app instances, ~/.claude was
// cleaned, or the machine changed). Claude Code emits
// "No conversation found with session ID: <id>".
func isStaleProviderResumeError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no conversation found")
}

// staleResumeRetryPlan drops a rejected resume pointer and full-resyncs, same
// semantics as the fingerprint-change path in resolveStatefulProviderSync.
func staleResumeRetryPlan(kind providerkind.Kind) statefulSyncPlan {
	if kind == providerkind.ClaudeCodeV2 {
		return statefulSyncPlan{resumeID: "", watermark: 0, fullResync: true}
	}
	return statefulSyncPlan{resumeID: uuid.New().String(), watermark: 0, fullResync: true}
}

func attachStatefulBackendSession(
	ctx context.Context,
	kind providerkind.Kind,
	llmdSessionID string,
	resumeID string,
	onSession func(string),
) context.Context {
	switch kind {
	case providerkind.CursorCLI:
		// Per-session workspace: sessions must not share (and overwrite) rules files.
		ctx = cursorclillm.WithLlmdSessionID(ctx, llmdSessionID)
		ctx = cursorclillm.WithResumeSessionID(ctx, resumeID)
		return cursorclillm.WithBackendSessionSink(ctx, onSession)
	case providerkind.ClaudeCodeV2:
		// Stable per-session neutral cwd: required for --resume to find the session.
		ctx = claudecodeclillm.WithLlmdSessionID(ctx, llmdSessionID)
		ctx = claudecodeclillm.WithResumeSessionID(ctx, resumeID)
		return claudecodeclillm.WithBackendSessionSink(ctx, onSession)
	default:
		return ctx
	}
}

func providerKindFromComposite(comp *providers.Composite, ctx context.Context, sc serverutil.Scope, providerID string) providerkind.Kind {
	if comp == nil {
		return providerkind.Unspecified
	}
	e, ok := comp.Get(ctx, sc, strings.TrimSpace(providerID))
	if !ok || e == nil {
		return providerkind.Unspecified
	}
	return e.Kind
}
