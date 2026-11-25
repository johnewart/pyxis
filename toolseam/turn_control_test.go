package toolseam

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestTurnControl_NilSafe(t *testing.T) {
	var tc *TurnControl
	tc.RequestEnd(EndTurnRequest{ToolName: "x"})
	if tc.EndRequested() || tc.Requests() != nil {
		t.Fatal("nil TurnControl must be inert")
	}
	if TurnControlFrom(context.Background()) != nil {
		t.Fatal("no TurnControl expected on a bare context")
	}
}

func TestRunInlineBuiltin_EndsTurnOnlyOnSuccess(t *testing.T) {
	h := NewHandlers()
	_ = h.Register("ok", func(context.Context, json.RawMessage, Invocation) (Result, error) {
		return Result{Content: "done", EndsTurn: true}, nil
	})
	_ = h.Register("bad", func(context.Context, json.RawMessage, Invocation) (Result, error) {
		return Result{EndsTurn: true}, errors.New("boom")
	})

	tc := &TurnControl{}
	ctx := WithTurnControl(context.Background(), tc)
	RunInlineBuiltin(ctx, "bad", h, Invocation{ToolCallID: "c1"})
	if tc.EndRequested() {
		t.Fatal("failed handler must not request end of turn")
	}
	RunInlineBuiltin(ctx, "ok", h, Invocation{ToolCallID: "c2"})
	reqs := tc.Requests()
	if len(reqs) != 1 || reqs[0].ToolCallID != "c2" || reqs[0].ToolName != "ok" || reqs[0].Source != "inline" {
		t.Fatalf("requests = %+v", reqs)
	}
}
