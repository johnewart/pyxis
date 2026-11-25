package session

import (
	"sync"
	"sync/atomic"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
)

// Snapshot is a point-in-time view of session observation state.
type Snapshot struct {
	State  conversationevents.ConversationState
	Events []*llmv1.ConversationStreamFrame
}

// HubOptions configures optional event retention.
//
// KeepEventLog retains raw frames in memory for Snapshot().Events. Production
// has no reader for this (replay is served from the session-events store), so
// it should stay off outside tests: with it on, every session pins up to
// MaxEventLogLength frames of delta strings until the runtime is evicted.
type HubOptions struct {
	KeepEventLog      bool
	MaxEventLogLength int
}

// Hub multicasts conversation stream frames and maintains a canonical fold (TS ConversationSessionHub).
type Hub struct {
	mu                sync.RWMutex
	state             conversationevents.ConversationState
	listeners         sync.Map // int64 -> func(*llmv1.ConversationStreamFrame)
	snapshotListeners sync.Map // int64 -> func(Snapshot)
	// snapshotListenerN mirrors the size of snapshotListeners so Dispatch can
	// skip building (and copying) a Snapshot when nobody is listening — the
	// common case, and previously an O(event log) allocation on every frame.
	snapshotListenerN atomic.Int32
	nextListenerID    atomic.Int64
	opts              HubOptions
	eventLog          []*llmv1.ConversationStreamFrame
}

func NewHub(options HubOptions) *Hub {
	if options.MaxEventLogLength <= 0 {
		options.MaxEventLogLength = 10_000
	}
	h := &Hub{state: conversationevents.InitialConversationState(), opts: options}
	if options.KeepEventLog {
		h.eventLog = make([]*llmv1.ConversationStreamFrame, 0, 256)
	}
	return h
}

// Dispatch applies a frame to folded state and notifies subscribers.
func (h *Hub) Dispatch(frame *llmv1.ConversationStreamFrame) {
	if frame == nil {
		return
	}
	h.mu.Lock()
	h.state = conversationevents.ReduceConversation(h.state, frame)
	if h.eventLog != nil {
		h.eventLog = appendBounded(h.eventLog, frame, h.opts.MaxEventLogLength)
	}
	var snap Snapshot
	notifySnapshot := h.snapshotListenerN.Load() > 0
	if notifySnapshot {
		snap = h.snapshotLocked()
	}
	h.mu.Unlock()

	h.listeners.Range(func(_, v any) bool {
		if fn, ok := v.(func(*llmv1.ConversationStreamFrame)); ok && fn != nil {
			fn(frame)
		}
		return true
	})
	if notifySnapshot {
		h.snapshotListeners.Range(func(_, v any) bool {
			if fn, ok := v.(func(Snapshot)); ok && fn != nil {
				fn(snap)
			}
			return true
		})
	}
}

// appendBounded appends frame and enforces the retention cap. Trimming copies
// the retained tail into a fresh slice: re-slicing in place would keep every
// trimmed frame reachable through the old backing array. The copy runs only
// once the log overshoots the cap by 25% (hysteresis) so steady-state appends
// stay amortized O(1) instead of O(cap) per frame; readers re-apply the exact
// cap (see snapshotLocked).
func appendBounded(
	log []*llmv1.ConversationStreamFrame,
	frame *llmv1.ConversationStreamFrame,
	max int,
) []*llmv1.ConversationStreamFrame {
	log = append(log, frame)
	if max <= 0 || len(log) <= max+max/4 {
		return log
	}
	compact := make([]*llmv1.ConversationStreamFrame, max, max+max/4)
	copy(compact, log[len(log)-max:])
	return compact
}

func (h *Hub) snapshotLocked() Snapshot {
	var evts []*llmv1.ConversationStreamFrame
	if h.eventLog != nil {
		src := h.eventLog
		if max := h.opts.MaxEventLogLength; max > 0 && len(src) > max {
			src = src[len(src)-max:]
		}
		evts = append(evts, src...)
	}
	return Snapshot{State: h.state, Events: evts}
}

// Snapshot returns state and optional retained events.
func (h *Hub) Snapshot() Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.snapshotLocked()
}

// Subscribe registers a listener; returned func unsubscribes.
func (h *Hub) Subscribe(fn func(*llmv1.ConversationStreamFrame)) (unsubscribe func()) {
	if fn == nil {
		return func() {}
	}
	id := h.nextListenerID.Add(1)
	h.listeners.Store(id, fn)
	return func() { h.listeners.Delete(id) }
}

// SubscribeSnapshot is invoked after each dispatch with updated snapshot.
func (h *Hub) SubscribeSnapshot(fn func(Snapshot)) (unsubscribe func()) {
	if fn == nil {
		return func() {}
	}
	id := h.nextListenerID.Add(1)
	h.snapshotListeners.Store(id, fn)
	h.snapshotListenerN.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			h.snapshotListeners.Delete(id)
			h.snapshotListenerN.Add(-1)
		})
	}
}
