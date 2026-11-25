package session

import (
	"fmt"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func userTurnFrame(id string) *llmv1.ConversationStreamFrame {
	return &llmv1.ConversationStreamFrame{
		Frame: &llmv1.ConversationStreamFrame_UserTurnCommitted{
			UserTurnCommitted: &llmv1.EvUserTurnCommitted{UserMessageId: id},
		},
	}
}

func eventIDs(frames []*llmv1.ConversationStreamFrame) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, f.GetUserTurnCommitted().GetUserMessageId())
	}
	return out
}

// The event log must stay bounded, retain the newest frames, and folded state
// must keep reducing regardless of retention.
func TestHubEventLogBounded(t *testing.T) {
	t.Parallel()
	const max = 4
	h := NewHub(HubOptions{KeepEventLog: true, MaxEventLogLength: max})
	for i := 0; i < 20; i++ {
		h.Dispatch(userTurnFrame(fmt.Sprintf("m%d", i)))
	}
	snap := h.Snapshot()
	if got := snap.State.LastCommittedUserMessageID; got != "m19" {
		t.Fatalf("folded state: last user message = %q, want m19", got)
	}
	if len(snap.Events) != max {
		t.Fatalf("snapshot events = %d, want %d (ids=%v)", len(snap.Events), max, eventIDs(snap.Events))
	}
	want := []string{"m16", "m17", "m18", "m19"}
	for i, id := range eventIDs(snap.Events) {
		if id != want[i] {
			t.Fatalf("snapshot events = %v, want %v", eventIDs(snap.Events), want)
		}
	}
	// Internal retention may overshoot by the hysteresis margin, never more.
	if got, cap := len(h.eventLog), max+max/4; got > cap {
		t.Fatalf("retained event log = %d frames, want <= %d", got, cap)
	}
}

// KeepEventLog off (the production configuration) must retain nothing.
func TestHubNoEventLogByDefault(t *testing.T) {
	t.Parallel()
	h := NewHub(HubOptions{})
	for i := 0; i < 5; i++ {
		h.Dispatch(userTurnFrame(fmt.Sprintf("m%d", i)))
	}
	snap := h.Snapshot()
	if snap.Events != nil {
		t.Fatalf("expected no retained events, got %d", len(snap.Events))
	}
	if got := snap.State.LastCommittedUserMessageID; got != "m4" {
		t.Fatalf("folded state: last user message = %q, want m4", got)
	}
}

// Snapshot listeners get per-dispatch snapshots; unsubscribing (idempotently)
// returns the hub to the zero-listener fast path.
func TestHubSnapshotListenerLifecycle(t *testing.T) {
	t.Parallel()
	h := NewHub(HubOptions{KeepEventLog: true, MaxEventLogLength: 8})

	var calls int
	var last Snapshot
	unsub := h.SubscribeSnapshot(func(s Snapshot) {
		calls++
		last = s
	})
	if got := h.snapshotListenerN.Load(); got != 1 {
		t.Fatalf("snapshot listener count = %d, want 1", got)
	}

	h.Dispatch(userTurnFrame("a"))
	h.Dispatch(userTurnFrame("b"))
	if calls != 2 {
		t.Fatalf("snapshot listener calls = %d, want 2", calls)
	}
	if got := last.State.LastCommittedUserMessageID; got != "b" {
		t.Fatalf("last snapshot state = %q, want b", got)
	}
	if len(last.Events) != 2 {
		t.Fatalf("last snapshot events = %d, want 2", len(last.Events))
	}

	unsub()
	unsub() // double-unsubscribe must not corrupt the listener count
	if got := h.snapshotListenerN.Load(); got != 0 {
		t.Fatalf("snapshot listener count after unsubscribe = %d, want 0", got)
	}
	h.Dispatch(userTurnFrame("c"))
	if calls != 2 {
		t.Fatalf("snapshot listener called after unsubscribe: calls = %d", calls)
	}
}

// Frame listeners are unaffected by the lazy-snapshot path.
func TestHubFrameListener(t *testing.T) {
	t.Parallel()
	h := NewHub(HubOptions{})
	var got []string
	unsub := h.Subscribe(func(f *llmv1.ConversationStreamFrame) {
		got = append(got, f.GetUserTurnCommitted().GetUserMessageId())
	})
	h.Dispatch(userTurnFrame("x"))
	h.Dispatch(userTurnFrame("y"))
	unsub()
	h.Dispatch(userTurnFrame("z"))
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("frame listener got %v, want [x y]", got)
	}
}
