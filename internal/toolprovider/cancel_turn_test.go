package toolprovider

import (
	"context"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/toolseam"
)

func goTurnCall(ctx context.Context, hub *Hub, d *llmv1.ToolDefinition, tenant, turn string) <-chan callOut {
	out := make(chan callOut, 1)
	go func() {
		inv := execInv(tenant, "deploy", `{}`)
		inv.TurnID = turn
		inv.Origin = toolseam.OriginMCP
		c, ok, e := hub.Call(ctx, d, inv)
		out <- callOut{c, ok, e}
	}()
	return out
}

func TestCancelTurnReleasesRefusesAndIsolates(t *testing.T) {
	hub, st, d := attachExec(t, 30)
	ctx := context.Background() // never cancelled

	resA := goTurnCall(ctx, hub, d, "t1", "turnA")
	tcA := recvCall(t, st)
	resB := goTurnCall(ctx, hub, d, "t1", "turnB")
	tcB := recvCall(t, st)

	// (d) other tenant with the same turn id is unaffected.
	if n := hub.CancelTurn("t2", "turnA"); n != 0 {
		t.Fatalf("CancelTurn other tenant = %d, want 0", n)
	}

	// (a) cancel turnA: cancel frame on the wire, waiter released.
	if n := hub.CancelTurn("t1", "turnA"); n != 1 {
		t.Fatalf("CancelTurn = %d, want 1", n)
	}
	in, err := st.Recv()
	if err != nil {
		t.Fatalf("recv cancel: %v", err)
	}
	if cc := in.GetToolCallCancel(); cc == nil || cc.GetCallId() != tcA.GetCallId() {
		t.Fatalf("expected cancel for %s, got %v", tcA.GetCallId(), in)
	}
	r := wait(t, resA, time.Second)
	if r.ok || r.errMsg != turnCancelledMsg {
		t.Fatalf("turnA call: got %+v", r)
	}

	// (b) follow-up call in the cancelled turn is refused without a frame.
	r = wait(t, goTurnCall(ctx, hub, d, "t1", "turnA"), time.Second)
	if r.ok || r.errMsg != turnCancelledMsg {
		t.Fatalf("refused call: got %+v", r)
	}

	// (c) idempotent.
	if n := hub.CancelTurn("t1", "turnA"); n != 0 {
		t.Fatalf("second CancelTurn = %d, want 0", n)
	}

	// (d) turnB still in flight and completes normally; the next frame on the
	// wire is turnC's tool_call, proving no frame was sent for the refused call.
	reply(t, st, output(tcB.GetCallId(), "ok", false))
	r = wait(t, resB, 2*time.Second)
	if !r.ok || r.content != "ok" {
		t.Fatalf("turnB call: got %+v", r)
	}
	resC := goTurnCall(ctx, hub, d, "t1", "turnC")
	tcC := recvCall(t, st)
	reply(t, st, output(tcC.GetCallId(), "c", false))
	if r = wait(t, resC, 2*time.Second); !r.ok {
		t.Fatalf("turnC call: got %+v", r)
	}
}

func TestCancelTurnTombstoneExpires(t *testing.T) {
	hub := &Hub{}
	hub.CancelTurn("t1", "x")
	if !hub.turnCancelled("t1", "x") {
		t.Fatal("expected tombstone")
	}
	hub.pendMu.Lock()
	hub.tombstones[turnKey{"t1", "x"}] = time.Now().Add(-time.Second)
	hub.pendMu.Unlock()
	if hub.turnCancelled("t1", "x") {
		t.Fatal("expired tombstone still active")
	}
	if _, ok := hub.tombstones[turnKey{"t1", "x"}]; ok {
		t.Fatal("expired tombstone not cleared")
	}
}
