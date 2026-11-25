package tooldelegate

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// Result is one client-submitted tool outcome.
type Result struct {
	Content string
	IsError bool
	// EndsTurn is the client's request to end the chat turn (ignored when IsError).
	EndsTurn bool
}

var (
	ErrNotFound         = errors.New("no pending tool wait for nonce")
	ErrAgentMismatch    = errors.New("agent_id does not match pending wait")
	ErrToolCallMismatch = errors.New("tool_call_id does not match pending wait")
	ErrScopeMismatch    = errors.New("scope does not match pending wait")
	ErrDuplicate        = errors.New("tool result already submitted for this wait")
	ErrWaitTimeout      = errors.New("delegate wait timed out")
	ErrWaitAborted      = errors.New("delegate wait aborted")
)

type entry struct {
	agentID    string
	toolCallID string
	scope      serverutil.Scope
	ch         chan Result
}

// Registry correlates SubmitToolResults with blocked ChatStream tool execution.
type Registry struct {
	mu      sync.Mutex
	pending map[string]*entry
}

func NewRegistry() *Registry {
	return &Registry{pending: make(map[string]*entry)}
}

// Register begins a wait for the given nonce. The returned channel receives at most one Result.
func (r *Registry) Register(nonce, agentID, toolCallID string, sc serverutil.Scope) <-chan Result {
	ch := make(chan Result, 1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending == nil {
		r.pending = make(map[string]*entry)
	}
	r.pending[nonce] = &entry{
		agentID:    agentID,
		toolCallID: toolCallID,
		scope:      sc,
		ch:         ch,
	}
	return ch
}

// Submit delivers a client result. The wait entry is removed before sending to avoid races with Abort.
func (r *Registry) Submit(nonce, agentID, toolCallID string, sc serverutil.Scope, content string, isError, endsTurn bool) error {
	r.mu.Lock()
	e, ok := r.pending[nonce]
	if ok {
		delete(r.pending, nonce)
	}
	r.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	if e.agentID != agentID {
		return ErrAgentMismatch
	}
	if e.toolCallID != toolCallID {
		return ErrToolCallMismatch
	}
	if e.scope != sc {
		return ErrScopeMismatch
	}
	select {
	case e.ch <- Result{Content: content, IsError: isError, EndsTurn: endsTurn}:
		return nil
	default:
		return ErrDuplicate
	}
}

// Abort drops a pending wait and closes its channel so the waiter observes cancellation.
func (r *Registry) Abort(nonce string) {
	r.mu.Lock()
	e, ok := r.pending[nonce]
	if ok {
		delete(r.pending, nonce)
	}
	r.mu.Unlock()
	if ok {
		close(e.ch)
	}
}

// Wait blocks until Submit delivers a result, ctx is done, or timeout. On ctx done or timeout, Abort is invoked.
func (r *Registry) Wait(ctx context.Context, nonce string, ch <-chan Result, timeout time.Duration) (Result, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res, ok := <-ch:
		if !ok {
			return Result{}, ErrWaitAborted
		}
		return res, nil
	case <-ctx.Done():
		r.Abort(nonce)
		return Result{}, ctx.Err()
	case <-timer.C:
		r.Abort(nonce)
		return Result{}, ErrWaitTimeout
	}
}
