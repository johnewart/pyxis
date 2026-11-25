package toolprovider

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/toolseam"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// ErrNoProvider is returned when no bidirectional provider stream is registered for the scope.
var ErrNoProvider = errors.New("no tool provider stream connected for this scope")

// ErrProviderDisconnected is returned by Invoke when the provider stream that
// received the call ended (or was superseded) before a result arrived. The
// tool may or may not have run.
var ErrProviderDisconnected = errors.New("tool provider disconnected, outcome unknown")

// Result is one provider-submitted tool outcome (mirrors tooldelegate.Result).
type Result struct {
	Content string
	IsError bool
	// DedupKey is the optional, opaque tool-provided content-identity key.
	DedupKey string
	// Attachments are optional passive, out-of-band pointers the tool attached
	// to its result (nil/empty ⇒ none). llmd persists kind+uri verbatim and
	// never interprets either.
	Attachments []*llmv1.ToolResultAttachment
	// EndsTurn is the tool's request to end the chat turn (ignored when IsError).
	EndsTurn bool
	// Cards are display-only attachments for the calling session (executor
	// results only; see ToolCallOutput.cards). Ignored when IsError.
	Cards []*llmv1.MessageAttachment

	// disconnected marks the sentinel delivered by drain.
	disconnected bool
	// execErr is the llmd-facing error message for an executor call result
	// (empty on success). Only set by DeliverCallResult.
	execErr string
}

// Hub correlates ToolExecutionInvoke frames on a provider stream with waiting llmd callers.
type Hub struct {
	mu      sync.RWMutex
	streams map[serverutil.Scope]*activeStream // exact (tenant, scope) -> latest stream
	pendMu  sync.Mutex
	pending map[string]chan Result // execution_id -> waiter
	// execCalls holds metadata for executor calls (call_id -> pending call);
	// guarded by pendMu. The waiter channel is also in pending so drain fails it.
	execCalls map[string]*pendingCall
	// turnCalls indexes in-flight executor calls by (tenant, turn); guarded by pendMu.
	turnCalls map[turnKey]map[string]*pendingCall
	// tombstones marks cancelled turns until expiry; guarded by pendMu.
	tombstones map[turnKey]time.Time
	// refusedCalls counts calls refused because their turn was cancelled.
	refusedCalls atomic.Int64

	// InlineNames, when non-nil, returns tool names served inline by the host;
	// executors may not claim them. nil ⇒ no inline conflicts.
	InlineNames func() map[string]struct{}

	execReg executorRegistry // per-tenant executor ownership (executor.go)
}

type activeStream struct {
	id  string
	mu  sync.Mutex // guards Send on srv
	srv llmv1.ToolProviderService_ConnectServer

	pendMu  sync.Mutex
	pending map[string]chan Result // execution_id -> waiter sent on this stream
}

// drain fails every waiter registered on this stream with the disconnected
// sentinel and returns how many were failed. Each waiter is failed at most
// once: removal from h.pending under h.pendMu decides the race with
// DeliverResult.
func (as *activeStream) drain(h *Hub) int {
	as.pendMu.Lock()
	snap := as.pending
	as.pending = make(map[string]chan Result)
	as.pendMu.Unlock()
	n := 0
	for id, ch := range snap {
		h.pendMu.Lock()
		cur, ok := h.pending[id]
		if ok && cur == ch {
			delete(h.pending, id)
		}
		h.pendMu.Unlock()
		if !ok || cur != ch {
			continue // already delivered or cleaned up
		}
		select {
		case ch <- Result{disconnected: true}:
		default:
		}
		n++
	}
	return n
}

// NewHub constructs an empty hub.
func NewHub() *Hub {
	return &Hub{
		streams: make(map[serverutil.Scope]*activeStream),
		pending: make(map[string]chan Result),
	}
}

// normScope trims both halves so map keys match exactly on (tenant, scope).
func normScope(sc serverutil.Scope) serverutil.Scope {
	return serverutil.Scope{TenantID: strings.TrimSpace(sc.TenantID), ScopeID: strings.TrimSpace(sc.ScopeID)}
}

// HasProvider reports whether a live provider stream exists for sc.
func (h *Hub) HasProvider(sc serverutil.Scope) bool {
	if h == nil {
		return false
	}
	u := normScope(sc)
	if u.TenantID == "" {
		return false
	}
	h.mu.RLock()
	_, ok := h.streams[u]
	h.mu.RUnlock()
	return ok
}

// Register binds the bidi server stream for a scope. A newer registration replaces the prior one.
func (h *Hub) Register(sc serverutil.Scope, srv llmv1.ToolProviderService_ConnectServer) (unregister func()) {
	u := normScope(sc)
	as := &activeStream{id: uuid.New().String(), srv: srv, pending: make(map[string]chan Result)}
	h.mu.Lock()
	prev := h.streams[u]
	h.streams[u] = as
	h.mu.Unlock()
	if prev != nil && prev != as {
		if n := prev.drain(h); n > 0 {
			llmdlog.Stdout(logModule, llmdlog.LevelWarn,
				"tool provider stream superseded: failed %d pending call(s) as disconnected: scope=%s old_stream=%s new_stream=%s",
				n, u.String(), prev.id, as.id)
		}
	}
	return func() {
		h.mu.Lock()
		cur, ok := h.streams[u]
		current := ok && cur == as
		if current {
			delete(h.streams, u)
		}
		h.mu.Unlock()
		if !current {
			return // superseded; already drained by the newer Register
		}
		if n := as.drain(h); n > 0 {
			llmdlog.Stdout(logModule, llmdlog.LevelWarn,
				"tool provider stream ended: failed %d pending call(s) as disconnected: scope=%s stream=%s",
				n, u.String(), as.id)
		}
	}
}

// Invoke sends an execution request on the provider stream and waits for the matching result.
func (h *Hub) Invoke(
	ctx context.Context,
	sc serverutil.Scope,
	agentID, sessionID, clientWorkspaceID, clientSessionID, toolID, argsJSON string,
	timeout time.Duration,
) (content string, isError bool, dedupKey string, attachments []*llmv1.ToolResultAttachment, err error) {
	if h == nil {
		return "", false, "", nil, ErrNoProvider
	}
	u := normScope(sc)
	if u.TenantID == "" {
		return "", false, "", nil, status.Error(codes.InvalidArgument, "tenant is required for tool provider invoke")
	}
	h.mu.RLock()
	as := h.streams[u]
	h.mu.RUnlock()
	if as == nil {
		return "", false, "", nil, ErrNoProvider
	}

	execID := uuid.New().String()
	ch := make(chan Result, 1)
	h.pendMu.Lock()
	h.pending[execID] = ch
	h.pendMu.Unlock()
	as.pendMu.Lock()
	as.pending[execID] = ch
	as.pendMu.Unlock()
	defer func() {
		h.pendMu.Lock()
		if cur, ok := h.pending[execID]; ok && cur == ch {
			delete(h.pending, execID)
		}
		h.pendMu.Unlock()
		as.pendMu.Lock()
		if cur, ok := as.pending[execID]; ok && cur == ch {
			delete(as.pending, execID)
		}
		as.pendMu.Unlock()
	}()

	inv := &llmv1.ToolExecutionInvoke{
		ExecutionId:     execID,
		ToolId:          strings.TrimSpace(toolID),
		ArgumentsJson:   argsJSON,
		SessionId:       strPtr(sessionID),
		ClientSessionId: strPtr(clientSessionID),
	}
	out := &llmv1.ToolServerToProvider{
		Frame: &llmv1.ToolServerToProvider_Invoke{Invoke: inv},
	}

	as.mu.Lock()
	sendErr := as.srv.Send(out)
	as.mu.Unlock()
	if sendErr != nil {
		return "", false, "", nil, sendErr
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res, ok := <-ch:
		if !ok {
			return "", false, "", nil, ErrProviderDisconnected
		}
		if res.disconnected {
			return "", false, "", nil, ErrProviderDisconnected
		}
		if res.EndsTurn && !res.IsError {
			toolseam.TurnControlFrom(ctx).RequestEnd(toolseam.EndTurnRequest{ToolName: strings.TrimSpace(toolID), Source: "provider"})
		}
		return res.Content, res.IsError, res.DedupKey, res.Attachments, nil
	case <-ctx.Done():
		return "", false, "", nil, ctx.Err()
	case <-timer.C:
		return "", false, "", nil, status.Error(codes.DeadlineExceeded, "tool provider execution timed out")
	}
}

// DeliverResult routes a provider result to a waiting invoke (returns false if execution_id unknown).
func (h *Hub) DeliverResult(executionID, toolID, content string, isError bool, dedupKey string, attachments []*llmv1.ToolResultAttachment, endsTurn bool) bool {
	if h == nil {
		return false
	}
	id := strings.TrimSpace(executionID)
	if id == "" {
		return false
	}
	h.pendMu.Lock()
	ch, ok := h.pending[id]
	if ok {
		delete(h.pending, id)
	}
	h.pendMu.Unlock()
	if !ok {
		return false
	}
	_ = toolID // reserved for mismatch checks
	select {
	case ch <- Result{Content: content, IsError: isError, DedupKey: dedupKey, Attachments: attachments, EndsTurn: endsTurn}:
	default:
	}
	return true
}

func strPtr(s string) *string {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil
	}
	return proto.String(t)
}
