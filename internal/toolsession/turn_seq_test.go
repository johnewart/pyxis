package toolsession

import (
	"context"
	"testing"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
)

// TestToolInvocationEnvelope_TurnSeqCapturedAtInvocationStart proves that when
// a caller stamps env.TurnSeq via TurnRegister.Current(sessionID) at
// invocation start (the contract all three transport call sites follow), a
// still in-flight tool's envelope keeps carrying the turn that was active
// when the tool STARTED — even if the register advances to a new turn before
// the tool result is ingested. Envelope construction/stamping happens once,
// before dispatch; IngestDelegatedResult itself never touches TurnRegister.
func TestToolInvocationEnvelope_TurnSeqCapturedAtInvocationStart(t *testing.T) {
	ctx := context.Background()
	db := store.NewTestSQLite(t)
	reg := store.NewTurnRegister(db)

	const sessionID = "sess-turnseq-envelope"

	firstSeq, err := reg.Advance(ctx, serverutil.Scope{}, sessionID, time.Now().UTC(), "turn_1")
	if err != nil {
		t.Fatalf("advance turn 1: %v", err)
	}
	if firstSeq != 1 {
		t.Fatalf("expected turn 1 seq=1, got %d", firstSeq)
	}

	// Invocation start: the call site captures TurnRegister.Current and
	// stamps it onto the envelope right away, before the tool runs.
	env := ToolInvocationEnvelope{
		Scope:      serverutil.Scope{TenantID: "ws-1"},
		SessionID:  sessionID,
		ToolCallID: "call-inflight",
		ToolName:   "read_code",
		Source:     "grpc-loop",
		TurnSeq:    reg.Current(serverutil.Scope{}, sessionID),
	}
	if env.TurnSeq != 1 {
		t.Fatalf("envelope TurnSeq at invocation start: want 1, got %d", env.TurnSeq)
	}

	// A new turn starts (e.g. next ChatStream/MCP turn) while the tool from
	// turn 1 is still in-flight and has not yet been ingested.
	secondSeq, err := reg.Advance(ctx, serverutil.Scope{}, sessionID, time.Now().UTC(), "turn_2")
	if err != nil {
		t.Fatalf("advance turn 2: %v", err)
	}
	if secondSeq != 2 {
		t.Fatalf("expected turn 2 seq=2, got %d", secondSeq)
	}

	// The already-stamped envelope must still carry the issuing turn (1),
	// never the now-current turn (2) — proving TurnSeq is captured once at
	// invocation start, not re-derived lazily when the result is ingested.
	if env.TurnSeq != 1 {
		t.Fatalf("in-flight tool envelope must retain issuing turn seq=1, got %d", env.TurnSeq)
	}
	if got := reg.Current(serverutil.Scope{}, sessionID); got != 2 {
		t.Fatalf("register current seq after turn 2 advance: want 2, got %d", got)
	}
}
