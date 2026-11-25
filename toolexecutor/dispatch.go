package toolexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/tenantscope"
	"github.com/johnewart/pyxis/toolseam"
)

// DefaultMaxDuration applies when a call carries max_duration_seconds == 0.
const DefaultMaxDuration = 600 * time.Second

// Failure codes sent in ToolCallFailure.Code.
const (
	CodeHandlerError = "handler_error"
	CodeTimeout      = "timeout"
	CodeCancelled    = "cancelled"
	CodeUnknownTool  = "unknown_tool"
	CodeBadArgs      = "bad_args"
)

// frameStream is the minimal bidi stream Serve needs.
type frameStream interface {
	Send(*llmv1.ToolProviderToServer) error
	Recv() (*llmv1.ToolServerToProvider, error)
}

var _ frameStream = (llmv1.ToolProviderService_ConnectClient)(nil)

type inflightCall struct {
	cancel    context.CancelFunc
	cancelled bool // set when a ToolCallCancel arrived
}

func mapOrigin(o llmv1.ToolCallOrigin) toolseam.Origin {
	switch o {
	case llmv1.ToolCallOrigin_TOOL_CALL_ORIGIN_MODEL:
		return toolseam.OriginChatLoop
	case llmv1.ToolCallOrigin_TOOL_CALL_ORIGIN_CLIENT:
		return toolseam.OriginInvokeTool
	default:
		return toolseam.OriginUnspecified
	}
}

// Serve runs the receive loop on s until Recv fails or ctx ends; all in-flight
// calls are cancelled (and awaited) before it returns.
func (c *Client) Serve(ctx context.Context, s frameStream, scope tenantscope.Scope) error {
	ctx, cancelAll := context.WithCancel(ctx)
	var sendMu sync.Mutex
	var wg sync.WaitGroup
	defer func() {
		cancelAll()
		wg.Wait()
	}()
	send := func(res *llmv1.ToolCallResult) {
		sendMu.Lock()
		err := s.Send(&llmv1.ToolProviderToServer{Frame: &llmv1.ToolProviderToServer_ToolCallResult{ToolCallResult: res}})
		sendMu.Unlock()
		if err != nil {
			c.log(llmdlog.LevelWarn, "executor_id=%s call_id=%s: send ToolCallResult failed: %v", c.opts.ExecutorID, res.GetCallId(), err)
		}
	}

	type recvd struct {
		msg *llmv1.ToolServerToProvider
		err error
	}
	recvCh := make(chan recvd, 1)
	go func() {
		for {
			m, err := s.Recv()
			select {
			case recvCh <- recvd{m, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		var r recvd
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r = <-recvCh:
		}
		if r.err != nil {
			return r.err
		}
		switch m := r.msg.GetFrame().(type) {
		case *llmv1.ToolServerToProvider_ToolCall:
			c.startCall(ctx, m.ToolCall, scope, send, &wg)
		case *llmv1.ToolServerToProvider_ToolCallCancel:
			c.cancelCall(m.ToolCallCancel)
		default:
			c.log(llmdlog.LevelWarn, "executor_id=%s: unexpected server frame %T ignored", c.opts.ExecutorID, r.msg.GetFrame())
		}
	}
}

// callScope is the scope a call runs in: the stream's tenant (authoritative —
// the stream was authorized for it) with the session's scope_id from the call.
// An empty scope_id (no resolved scope, or an older llmd) keeps the stream's.
func callScope(stream tenantscope.Scope, tc *llmv1.ExecutorToolCall) tenantscope.Scope {
	sc := stream
	if s := strings.TrimSpace(tc.GetScopeId()); s != "" {
		sc.ScopeID = s
	}
	return sc
}

func failure(callID, code, msg string) *llmv1.ToolCallResult {
	return &llmv1.ToolCallResult{CallId: callID, Outcome: &llmv1.ToolCallResult_Failure{Failure: &llmv1.ToolCallFailure{Code: code, Message: msg}}}
}

func (c *Client) startCall(ctx context.Context, tc *llmv1.ExecutorToolCall, scope tenantscope.Scope, send func(*llmv1.ToolCallResult), wg *sync.WaitGroup) {
	callID := tc.GetCallId()
	name := tc.GetToolName()
	if callID == "" {
		c.log(llmdlog.LevelWarn, "executor_id=%s tool=%s: ExecutorToolCall without call_id ignored", c.opts.ExecutorID, name)
		return
	}
	var fn toolseam.Handler
	if c.isServed(name) {
		fn, _ = c.opts.Handlers.Lookup(name)
	}
	if fn == nil {
		c.log(llmdlog.LevelWarn, "executor_id=%s call_id=%s: tool %q not handled", c.opts.ExecutorID, callID, name)
		send(failure(callID, CodeUnknownTool, fmt.Sprintf("tool %q is not handled by executor %q", name, c.opts.ExecutorID)))
		return
	}
	dur := DefaultMaxDuration
	if s := tc.GetMaxDurationSeconds(); s > 0 {
		dur = time.Duration(s) * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, dur)
	entry := &inflightCall{cancel: cancel}
	c.mu.Lock()
	if _, dup := c.inflight[callID]; dup {
		c.mu.Unlock()
		cancel()
		c.log(llmdlog.LevelWarn, "executor_id=%s call_id=%s: duplicate call id ignored", c.opts.ExecutorID, callID)
		return
	}
	c.inflight[callID] = entry
	c.mu.Unlock()

	inv := toolseam.Invocation{
		ToolCallID:    callID,
		Name:          name,
		ArgumentsJSON: tc.GetArgumentsJson(),
		SessionID:     tc.GetSessionId(),
		Scope:         callScope(scope, tc),
		ToolContext:   copyStringMap(tc.GetToolContext()),
		TurnID:        strings.TrimSpace(tc.GetTurnId()),
		Origin:        mapOrigin(tc.GetOrigin()),
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		res := c.runHandler(cctx, fn, inv)
		c.mu.Lock()
		cur, ok := c.inflight[callID]
		owned := ok && cur == entry
		if owned {
			delete(c.inflight, callID)
		}
		wasCancelled := entry.cancelled
		c.mu.Unlock()
		if !owned {
			return
		}
		if ctxErr := cctx.Err(); ctxErr != nil {
			switch {
			case wasCancelled:
				res = failure(callID, CodeCancelled, fmt.Sprintf("tool call %s was cancelled", callID))
			case errors.Is(ctxErr, context.DeadlineExceeded):
				res = failure(callID, CodeTimeout, fmt.Sprintf("tool call %s timed out after %s", callID, dur))
			default:
				res = failure(callID, CodeCancelled, fmt.Sprintf("tool call %s was cancelled: executor shutting down", callID))
			}
		}
		send(res)
	}()
}

func (c *Client) runHandler(ctx context.Context, fn toolseam.Handler, inv toolseam.Invocation) *llmv1.ToolCallResult {
	args := json.RawMessage(`{}`)
	if raw := strings.TrimSpace(inv.ArgumentsJSON); raw != "" {
		if !json.Valid([]byte(raw)) {
			return failure(inv.ToolCallID, CodeBadArgs, fmt.Sprintf("invalid arguments JSON for tool %q", inv.Name))
		}
		args = json.RawMessage(raw)
	}
	out, err := fn(ctx, args, inv)
	if err != nil {
		return failure(inv.ToolCallID, CodeHandlerError, err.Error())
	}
	return &llmv1.ToolCallResult{CallId: inv.ToolCallID, Outcome: &llmv1.ToolCallResult_Output{Output: &llmv1.ToolCallOutput{
		Content: out.Content, EndsTurn: out.EndsTurn, Cards: cardsToProto(out.Cards),
	}}}
}

// cardsToProto carries a handler's display cards to llmd, which anchors them
// to the calling tool call (so after_tool_call_id is left unset).
func cardsToProto(in []toolseam.Card) []*llmv1.MessageAttachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]*llmv1.MessageAttachment, 0, len(in))
	for _, c := range in {
		out = append(out, &llmv1.MessageAttachment{Id: c.ID, Kind: c.Kind, PayloadJson: c.PayloadJSON, Content: c.Content})
	}
	return out
}

func (c *Client) cancelCall(cc *llmv1.ToolCallCancel) {
	callID := cc.GetCallId()
	c.mu.Lock()
	entry, ok := c.inflight[callID]
	if ok {
		entry.cancelled = true
	}
	c.mu.Unlock()
	if !ok {
		c.log(llmdlog.LevelWarn, "executor_id=%s call_id=%s: ToolCallCancel for unknown call id (reason=%q)", c.opts.ExecutorID, callID, cc.GetReason())
		return
	}
	entry.cancel()
}
