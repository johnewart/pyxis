package server

import (
	"testing"

	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/store"
)

func TestResolveStatefulProviderSync_claudeFingerprintOnlyFullResync(t *testing.T) {
	// When the system prompt fingerprint changes (e.g. new workspace rule added),
	// Claude Code v2 must do a full resync so the new system prompt is guaranteed
	// to reach the provider — even if the transcript is otherwise stable.
	t.Parallel()
	prev := &store.ProviderSessionSync{
		BackendSessionID:   "claude-sess-1",
		SystemFingerprint:  "fp-old",
		SyncedMessageCount: 4,
	}
	plan := resolveStatefulProviderSync(
		providerkind.ClaudeCodeV2,
		prev,
		"fp-new",
		4,
		5,
	)
	if !plan.fullResync {
		t.Fatalf("expected full resync on fingerprint change, got incremental plan")
	}
	if plan.resumeID != "" {
		t.Fatalf("resumeID should be cleared on full resync, got %q", plan.resumeID)
	}
	if plan.watermark != 0 {
		t.Fatalf("watermark: got %d want 0", plan.watermark)
	}
}

func TestResolveStatefulProviderSync_claudeTranscriptMismatchFullResync(t *testing.T) {
	t.Parallel()
	prev := &store.ProviderSessionSync{
		BackendSessionID:   "claude-sess-1",
		SystemFingerprint:  "fp-old",
		SyncedMessageCount: 4,
	}
	plan := resolveStatefulProviderSync(
		providerkind.ClaudeCodeV2,
		prev,
		"fp-new",
		2,
		3,
	)
	if !plan.fullResync {
		t.Fatal("expected full resync when transcript count diverged")
	}
	if plan.resumeID != "" {
		t.Fatalf("resumeID should be cleared on transcript resync, got %q", plan.resumeID)
	}
	if plan.watermark != 0 {
		t.Fatalf("watermark: got %d want 0", plan.watermark)
	}
}

func TestResolveStatefulProviderSync_claudeFirstTurnFullResync(t *testing.T) {
	t.Parallel()
	plan := resolveStatefulProviderSync(providerkind.ClaudeCodeV2, nil, "fp", 0, 1)
	if !plan.fullResync || plan.resumeID != "" || plan.watermark != 0 {
		t.Fatalf("first turn: %+v", plan)
	}
}

func TestResolveStatefulProviderSync_cursorFingerprintFullResync(t *testing.T) {
	t.Parallel()
	prev := &store.ProviderSessionSync{
		BackendSessionID:   "cursor-sess",
		SystemFingerprint:  "fp-old",
		SyncedMessageCount: 2,
	}
	plan := resolveStatefulProviderSync(providerkind.CursorCLI, prev, "fp-new", 2, 3)
	if !plan.fullResync {
		t.Fatal("cursor should full-resync on fingerprint change")
	}
	if plan.resumeID == "" || plan.resumeID == "cursor-sess" {
		t.Fatalf("cursor full resync should mint new resume id, got %q", plan.resumeID)
	}
}
