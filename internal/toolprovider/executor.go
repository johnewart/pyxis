package toolprovider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/tooltimeout"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/toolseam"
	"google.golang.org/protobuf/proto"
)

var _ toolseam.ExecutorDispatcher = (*Hub)(nil)


// pendingCall is one in-flight executor call awaiting a ToolCallResult.
// Guarded by Hub.pendMu (stored in Hub.execCalls).
type pendingCall struct {
	ch         chan Result
	stream     *activeStream
	tenant     string
	name       string
	executorID string
	turnID     string
}

// turnKey identifies a chat turn within a tenant.
type turnKey struct {
	tenantID string
	turnID   string
}

// turnCancelledMsg is the error returned for calls in a cancelled turn.
const turnCancelledMsg = "cancelled: turn cancelled"

// MaxResultBytes is the largest serialized tool result llmd accepts from an
// executor per call (advertised in ExecutorHelloAck), NOT counting its cards:
// it protects the model's context, which cards never reach.
const MaxResultBytes = 1 << 20

// MaxCardsBytes bounds the cards (ToolCallOutput.cards) on one result.
// Each card is further bounded by what renders it (llmd/visualcards caps a
// display card at 3 MiB); this only stops one call flooding the session.
const MaxCardsBytes = 8 << 20

// executorKey identifies an executor within a tenant. Ownership is per tenant,
// not per (tenant, scope).
type executorKey struct {
	tenantID   string
	executorID string
}

// executorEntry is the live connection currently serving an executor_id.
type executorEntry struct {
	instanceID string
	stream     *activeStream
	names      []string // tool names accepted for this instance
	// superseded is closed when a newer instance replaces this one, so the
	// stream serving it can end.
	superseded chan struct{}
}

// executorRegistry holds per-tenant executor ownership. Guarded by mu; maps are
// initialised lazily by ensure.
type executorRegistry struct {
	mu        sync.Mutex
	owners    map[string]map[string]string                // tenant -> tool name -> executorID
	defs      map[string]map[string]*llmv1.ToolDefinition // tenant -> tool name -> def
	executors map[executorKey]*executorEntry
}

func (r *executorRegistry) ensure() {
	if r.owners == nil {
		r.owners = make(map[string]map[string]string)
	}
	if r.defs == nil {
		r.defs = make(map[string]map[string]*llmv1.ToolDefinition)
	}
	if r.executors == nil {
		r.executors = make(map[executorKey]*executorEntry)
	}
}

// releaseLocked drops ownership of e's names (defs stay registered, ownerless).
func (r *executorRegistry) releaseLocked(k executorKey, e *executorEntry) {
	owners := r.owners[k.tenantID]
	for _, n := range e.names {
		if owners[n] == k.executorID {
			delete(owners, n)
		}
	}
}

// HandleExecutorHello registers an executor's declared tools for sc's tenant
// and returns the ack to send plus a detach func bound to this instance.
// The detach func is never nil.
func (h *Hub) HandleExecutorHello(sc serverutil.Scope, hello *llmv1.ExecutorHello, srv llmv1.ToolProviderService_ConnectServer) (*llmv1.ExecutorHelloAck, func()) {
	ack, detach, _ := h.handleExecutorHello(sc, hello, srv)
	return ack, detach
}

// handleExecutorHello is HandleExecutorHello plus a channel closed when this
// instance is superseded (nil when nothing was registered).
func (h *Hub) handleExecutorHello(sc serverutil.Scope, hello *llmv1.ExecutorHello, srv llmv1.ToolProviderService_ConnectServer) (*llmv1.ExecutorHelloAck, func(), <-chan struct{}) {
	noop := func() {}
	tenant := strings.TrimSpace(sc.TenantID)
	execID := strings.TrimSpace(hello.GetExecutorId())
	ack := &llmv1.ExecutorHelloAck{ExecutorId: execID, MaxResultBytes: MaxResultBytes}
	reject := func(name, reason string) {
		ack.RejectedTools = append(ack.RejectedTools, &llmv1.RejectedTool{Name: name, Reason: reason})
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"executor hello: rejected tool %q: tenant=%s executor_id=%s: %s", name, tenant, execID, reason)
	}
	if tenant == "" || execID == "" {
		reason := "tenant is required for executor registration"
		if tenant != "" {
			reason = "executor_id is required"
		}
		for _, d := range hello.GetTools() {
			reject(d.GetName(), reason)
		}
		if len(hello.GetTools()) == 0 {
			llmdlog.Stdout(logModule, llmdlog.LevelWarn,
				"executor hello refused: tenant=%q executor_id=%q: %s", tenant, execID, reason)
		}
		return ack, noop, nil
	}
	instanceID := strings.TrimSpace(hello.GetInstanceId())
	if instanceID == "" {
		instanceID = uuid.New().String()
	}

	var inline map[string]struct{}
	if h.InlineNames != nil {
		inline = h.InlineNames()
	}

	key := executorKey{tenantID: tenant, executorID: execID}
	entry := &executorEntry{
		instanceID: instanceID,
		superseded: make(chan struct{}),
		stream:     &activeStream{id: uuid.New().String(), srv: srv, pending: make(map[string]chan Result)},
	}

	r := &h.execReg
	r.mu.Lock()
	r.ensure()
	prev := r.executors[key]
	if prev != nil {
		r.releaseLocked(key, prev)
	}
	r.executors[key] = entry
	owners := r.owners[tenant]
	if owners == nil {
		owners = make(map[string]string)
		r.owners[tenant] = owners
	}
	defs := r.defs[tenant]
	if defs == nil {
		defs = make(map[string]*llmv1.ToolDefinition)
		r.defs[tenant] = defs
	}
	seen := make(map[string]struct{})
	for _, d := range hello.GetTools() {
		name := strings.TrimSpace(d.GetName())
		switch {
		case name == "":
			reject(d.GetName(), "tool name is required")
			continue
		case func() bool { _, dup := seen[name]; return dup }():
			reject(name, "tool declared more than once in hello")
			continue
		}
		seen[name] = struct{}{}
		if _, ok := inline[name]; ok {
			reject(name, fmt.Sprintf("tool name %q is already provided inline by the llmd host", name))
			continue
		}
		if owner, ok := owners[name]; ok && owner != execID {
			reject(name, fmt.Sprintf("tool name %q is already owned by executor %q in this tenant", name, owner))
			continue
		}
		ex := d.GetExecutor()
		if ex == nil {
			reject(name, "tool execution must be an executor binding")
			continue
		}
		if strings.TrimSpace(ex.GetExecutorId()) != execID {
			reject(name, fmt.Sprintf("tool executor binding %q does not match hello executor_id %q", ex.GetExecutorId(), execID))
			continue
		}
		owners[name] = execID
		defs[name] = proto.Clone(d).(*llmv1.ToolDefinition)
		entry.names = append(entry.names, name)
		ack.AcceptedTools = append(ack.AcceptedTools, name)
	}
	r.mu.Unlock()

	if prev != nil {
		close(prev.superseded)
		n := prev.stream.drain(h)
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"executor superseded: failed %d pending call(s) as disconnected: tenant=%s executor_id=%s old_instance=%s new_instance=%s",
			n, tenant, execID, prev.instanceID, instanceID)
	}

	detach := func() {
		r.mu.Lock()
		cur := r.executors[key]
		current := cur == entry
		if current {
			delete(r.executors, key)
			r.releaseLocked(key, entry)
		}
		r.mu.Unlock()
		if !current {
			return // superseded; the newer hello already drained this stream
		}
		n := entry.stream.drain(h)
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"executor detached: tools left registered without owner; failed %d pending call(s) as disconnected: tenant=%s executor_id=%s instance_id=%s",
			n, tenant, execID, instanceID)
	}
	return ack, detach, entry.superseded
}

// ExecutorDefinitions returns the executor tool definitions registered for a
// tenant (owned or ownerless), sorted by name.
func (h *Hub) ExecutorDefinitions(tenantID string) []*llmv1.ToolDefinition {
	if h == nil {
		return nil
	}
	tenant := strings.TrimSpace(tenantID)
	r := &h.execReg
	r.mu.Lock()
	defs := r.defs[tenant]
	out := make([]*llmv1.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		out = append(out, d)
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].GetName() < out[j].GetName() })
	return out
}

// resolveExecutor returns the registered def for (tenant, name) and the live
// owning entry (nil when the def is ownerless). def is nil when unregistered.
func (h *Hub) resolveExecutor(tenant, name string) (*llmv1.ToolDefinition, string, *executorEntry) {
	r := &h.execReg
	r.mu.Lock()
	defer r.mu.Unlock()
	def := r.defs[tenant][name]
	if def == nil {
		return nil, "", nil
	}
	execID, ok := r.owners[tenant][name]
	if !ok {
		return def, def.GetExecutor().GetExecutorId(), nil
	}
	return def, execID, r.executors[executorKey{tenantID: tenant, executorID: execID}]
}

// turnCancelledLocked reports whether (tenant, turnID) is tombstoned, clearing
// an expired tombstone lazily. Caller holds h.pendMu.
func (h *Hub) turnCancelledLocked(tenant, turnID string) bool {
	if turnID == "" || h.tombstones == nil {
		return false
	}
	k := turnKey{tenantID: tenant, turnID: turnID}
	exp, ok := h.tombstones[k]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(h.tombstones, k)
		return false
	}
	return true
}

// turnCancelled reports whether the call's turn has been cancelled.
func (h *Hub) turnCancelled(tenant, turnID string) bool {
	h.pendMu.Lock()
	defer h.pendMu.Unlock()
	return h.turnCancelledLocked(tenant, turnID)
}

// refuseCancelledTurn logs and counts a call refused for a cancelled turn.
func (h *Hub) refuseCancelledTurn(tenant, turnID, name string) (string, bool, string) {
	n := h.refusedCalls.Add(1)
	llmdlog.Stdout(logModule, llmdlog.LevelInfo,
		"executor call refused: turn cancelled: tenant=%s turn=%s tool=%s refused_total=%d", tenant, turnID, name, n)
	return "", false, turnCancelledMsg
}

// tombstoneTTL is how long a cancelled turn refuses new calls: at least the
// largest effective timeout of any registered tool, never less than the
// default (see tooltimeout).
func (h *Hub) tombstoneTTL() time.Duration {
	ttl := tooltimeout.Default()
	r := &h.execReg
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, defs := range r.defs {
		for _, d := range defs {
			if dur := tooltimeout.ForTool(d); dur > ttl {
				ttl = dur
			}
		}
	}
	return ttl
}

// CancelTurn cancels every in-flight executor call in (tenantID, turnID):
// it sends ToolCallCancel on each owning stream, releases each waiter with a
// turn-cancelled error, and tombstones the turn so later Calls are refused.
// Returns the number of calls cancelled; a repeat call returns 0.
func (h *Hub) CancelTurn(tenantID, turnID string) int {
	if h == nil {
		return 0
	}
	tenant := strings.TrimSpace(tenantID)
	turn := strings.TrimSpace(turnID)
	if turn == "" {
		return 0
	}
	ttl := h.tombstoneTTL()
	k := turnKey{tenantID: tenant, turnID: turn}

	h.pendMu.Lock()
	if h.tombstones == nil {
		h.tombstones = make(map[turnKey]time.Time)
	}
	h.tombstones[k] = time.Now().Add(ttl)
	calls := h.turnCalls[k]
	delete(h.turnCalls, k)
	targets := make(map[string]*pendingCall, len(calls))
	for id, pc := range calls {
		if h.execCalls[id] != pc {
			continue // already delivered
		}
		delete(h.execCalls, id)
		delete(h.pending, id)
		targets[id] = pc
	}
	h.pendMu.Unlock()

	for id, pc := range targets {
		frame := &llmv1.ToolServerToProvider{Frame: &llmv1.ToolServerToProvider_ToolCallCancel{
			ToolCallCancel: &llmv1.ToolCallCancel{CallId: id, Reason: "turn_cancelled"}}}
		pc.stream.mu.Lock()
		err := pc.stream.srv.Send(frame)
		pc.stream.mu.Unlock()
		if err != nil {
			llmdlog.Stdout(logModule, llmdlog.LevelWarn,
				"executor tool_call_cancel send failed: tenant=%s turn=%s executor_id=%s call_id=%s: %v", tenant, turn, pc.executorID, id, err)
		}
		select {
		case pc.ch <- Result{Content: turnCancelledMsg, IsError: true, execErr: turnCancelledMsg}:
		default:
		}
	}
	llmdlog.Stdout(logModule, llmdlog.LevelInfo,
		"executor turn cancelled: tenant=%s turn=%s cancelled=%d", tenant, turn, len(targets))
	return len(targets)
}

// resultSizes returns a result's serialized size without its cards, and the
// size of its cards.
func resultSizes(r *llmv1.ToolCallResult) (size, cards int) {
	out := r.GetOutput()
	if len(out.GetCards()) == 0 {
		return proto.Size(r), 0
	}
	for _, c := range out.GetCards() {
		cards += proto.Size(c)
	}
	stripped := proto.Clone(r).(*llmv1.ToolCallResult)
	stripped.GetOutput().Cards = nil
	return proto.Size(stripped), cards
}

// cardsFromProto converts wire cards to toolseam cards. after_tool_call_id is
// dropped: llmd anchors every card to the call that returned it.
func cardsFromProto(in []*llmv1.MessageAttachment) []toolseam.Card {
	out := make([]toolseam.Card, 0, len(in))
	for _, a := range in {
		out = append(out, toolseam.Card{ID: a.GetId(), Kind: a.GetKind(), PayloadJSON: a.GetPayloadJson(), Content: a.GetContent()})
	}
	return out
}

// toProtoOrigin maps the dispatch path to the wire origin. The proto has no
// MCP value; MCP calls are client-initiated.
func toProtoOrigin(o toolseam.Origin) llmv1.ToolCallOrigin {
	switch o {
	case toolseam.OriginChatLoop:
		return llmv1.ToolCallOrigin_TOOL_CALL_ORIGIN_MODEL
	case toolseam.OriginMCP, toolseam.OriginInvokeTool:
		return llmv1.ToolCallOrigin_TOOL_CALL_ORIGIN_CLIENT
	default:
		return llmv1.ToolCallOrigin_TOOL_CALL_ORIGIN_UNSPECIFIED
	}
}

// Call dispatches one executor tool call to the stream owning inv.Name in the
// invocation's tenant and waits for its result, ctx cancellation, or the
// tool's max duration. Implements toolseam.ExecutorDispatcher.
func (h *Hub) Call(ctx context.Context, def *llmv1.ToolDefinition, inv toolseam.Invocation) (string, bool, string) {
	if h == nil {
		return "", false, "executor dispatch is not configured"
	}
	tenant := strings.TrimSpace(inv.Scope.TenantID)
	name := strings.TrimSpace(inv.Name)
	if name == "" {
		name = strings.TrimSpace(def.GetName())
	}
	if tenant == "" {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn, "executor call refused: empty tenant: tool=%s", name)
		return "", false, "tenant is required for executor tool calls"
	}
	regDef, execID, entry := h.resolveExecutor(tenant, name)
	if regDef == nil {
		return "", false, fmt.Sprintf("executor tool %q is not registered for this tenant", name)
	}
	if entry == nil {
		llmdlog.Stdout(logModule, llmdlog.LevelInfo,
			"executor call failed fast: executor not attached: tenant=%s executor_id=%s tool=%s", tenant, execID, name)
		return "", false, fmt.Sprintf("executor %q is not attached", execID)
	}
	turnID := strings.TrimSpace(inv.TurnID)
	if h.turnCancelled(tenant, turnID) {
		return h.refuseCancelledTurn(tenant, turnID, name)
	}
	as := entry.stream

	// Declared max_duration_seconds, else LLMD_TOOL_DEFAULT_TIMEOUT (12m), capped
	// by LLMD_TOOL_MAX_TIMEOUT when the operator sets one.
	maxDur := tooltimeout.ForTool(regDef)
	callCtx, cancel := context.WithTimeout(ctx, maxDur)
	defer cancel()

	ch := make(chan Result, 1)
	pc := &pendingCall{ch: ch, stream: as, tenant: tenant, name: name, executorID: execID, turnID: turnID}
	tk := turnKey{tenantID: tenant, turnID: turnID}
	h.pendMu.Lock()
	// Re-check under the lock so a concurrent CancelTurn cannot miss this call.
	if h.turnCancelledLocked(tenant, turnID) {
		h.pendMu.Unlock()
		return h.refuseCancelledTurn(tenant, turnID, name)
	}
	callID := uuid.New().String()
	if turnID != "" {
		if h.turnCalls == nil {
			h.turnCalls = make(map[turnKey]map[string]*pendingCall)
		}
		if h.turnCalls[tk] == nil {
			h.turnCalls[tk] = make(map[string]*pendingCall)
		}
		h.turnCalls[tk][callID] = pc
	}
	h.pending[callID] = ch
	if h.execCalls == nil {
		h.execCalls = make(map[string]*pendingCall)
	}
	h.execCalls[callID] = pc
	h.pendMu.Unlock()
	as.pendMu.Lock()
	as.pending[callID] = ch
	as.pendMu.Unlock()
	defer func() {
		h.pendMu.Lock()
		if cur, ok := h.pending[callID]; ok && cur == ch {
			delete(h.pending, callID)
		}
		if cur, ok := h.execCalls[callID]; ok && cur == pc {
			delete(h.execCalls, callID)
		}
		if m := h.turnCalls[tk]; m != nil && m[callID] == pc {
			delete(m, callID)
			if len(m) == 0 {
				delete(h.turnCalls, tk)
			}
		}
		h.pendMu.Unlock()
		as.pendMu.Lock()
		if cur, ok := as.pending[callID]; ok && cur == ch {
			delete(as.pending, callID)
		}
		as.pendMu.Unlock()
	}()

	out := &llmv1.ToolServerToProvider{Frame: &llmv1.ToolServerToProvider_ToolCall{ToolCall: &llmv1.ExecutorToolCall{
		CallId:             callID,
		ToolName:           name,
		ArgumentsJson:      inv.ArgumentsJSON,
		SessionId:          strings.TrimSpace(inv.SessionID),
		TenantId:           tenant,
		ScopeId:            strings.TrimSpace(inv.Scope.ScopeID),
		TurnId:             turnID,
		Origin:             toProtoOrigin(inv.Origin),
		ToolContext:        cloneStringMap(inv.ToolContext),
		MaxDurationSeconds: uint32(maxDur / time.Second),
	}}}
	start := time.Now()
	as.mu.Lock()
	sendErr := as.srv.Send(out)
	as.mu.Unlock()
	if sendErr != nil {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"executor tool_call send failed: tenant=%s executor_id=%s call_id=%s tool=%s: %v", tenant, execID, callID, name, sendErr)
		return "", false, fmt.Sprintf("executor %q send failed: %v", execID, sendErr)
	}

	select {
	case res := <-ch:
		if res.disconnected {
			return "", false, fmt.Sprintf("executor %q disconnected, outcome unknown", execID)
		}
		if !res.IsError && len(res.Cards) > 0 {
			if cerr := toolseam.ShowCards(ctx, inv, cardsFromProto(res.Cards)); cerr != nil {
				llmdlog.Stdout(logModule, llmdlog.LevelWarn,
					"executor call cards not shown: tenant=%s executor_id=%s call_id=%s tool=%s cards=%d: %v",
					tenant, execID, callID, name, len(res.Cards), cerr)
				return toolseam.CardFailure(res.Content, cerr), false, cerr.Error()
			}
		}
		if res.EndsTurn && !res.IsError {
			toolseam.TurnControlFrom(ctx).RequestEnd(toolseam.EndTurnRequest{ToolCallID: inv.ToolCallID, ToolName: name, Source: "executor"})
		}
		return res.Content, !res.IsError, res.execErr
	case <-callCtx.Done():
		timedOut := ctx.Err() == nil && errors.Is(callCtx.Err(), context.DeadlineExceeded)
		reason, msg := "cancelled", "cancelled"
		if timedOut {
			reason = "deadline"
			msg = fmt.Sprintf("timed out after %ds", int(maxDur/time.Second))
		}
		cancelFrame := &llmv1.ToolServerToProvider{Frame: &llmv1.ToolServerToProvider_ToolCallCancel{
			ToolCallCancel: &llmv1.ToolCallCancel{CallId: callID, Reason: reason}}}
		as.mu.Lock()
		cErr := as.srv.Send(cancelFrame)
		as.mu.Unlock()
		if cErr != nil {
			llmdlog.Stdout(logModule, llmdlog.LevelWarn,
				"executor tool_call_cancel send failed: tenant=%s executor_id=%s call_id=%s: %v", tenant, execID, callID, cErr)
		}
		llmdlog.Stdout(logModule, llmdlog.LevelInfo,
			"executor call %s: tenant=%s executor_id=%s call_id=%s tool=%s origin=%s elapsed=%s max_duration=%s",
			msg, tenant, execID, callID, name, inv.Origin, time.Since(start).Round(time.Millisecond), maxDur)
		return "", false, msg
	}
}

// DeliverCallResult routes an executor ToolCallResult to its waiting Call.
// Returns false when the call_id is unknown/late or the result is refused.
func (h *Hub) DeliverCallResult(tenantID string, r *llmv1.ToolCallResult) bool {
	return h.deliverCallResult(tenantID, nil, r)
}

// deliverCallResult is DeliverCallResult with an optional source stream; when
// from is non-nil the result must arrive on the stream that received the call.
func (h *Hub) deliverCallResult(tenantID string, from llmv1.ToolProviderService_ConnectServer, r *llmv1.ToolCallResult) bool {
	if h == nil || r == nil {
		return false
	}
	tenant := strings.TrimSpace(tenantID)
	id := strings.TrimSpace(r.GetCallId())
	h.pendMu.Lock()
	pc, ok := h.execCalls[id]
	if !ok || h.pending[id] != pc.ch {
		h.pendMu.Unlock()
		llmdlog.Stdout(logModule, llmdlog.LevelDebug,
			"executor result for unknown or late call dropped: tenant=%s call_id=%s", tenant, id)
		return false
	}
	if pc.tenant != tenant || (from != nil && pc.stream.srv != from) {
		h.pendMu.Unlock()
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"executor result dropped: wrong tenant or stream: call_tenant=%s result_tenant=%s executor_id=%s call_id=%s",
			pc.tenant, tenant, pc.executorID, id)
		return false
	}
	delete(h.pending, id)
	delete(h.execCalls, id)
	h.pendMu.Unlock()

	var res Result
	size, cardsSize := resultSizes(r)
	if size > MaxResultBytes || cardsSize > MaxCardsBytes {
		msg := fmt.Sprintf("executor result too large: %d bytes exceeds limit of %d bytes", size, MaxResultBytes)
		if cardsSize > MaxCardsBytes {
			msg = fmt.Sprintf("executor result cards too large: %d bytes exceeds limit of %d bytes", cardsSize, MaxCardsBytes)
		}
		llmdlog.Stdout(logModule, llmdlog.LevelWarn, "%s: tenant=%s executor_id=%s call_id=%s", msg, tenant, pc.executorID, id)
		res = Result{Content: msg, IsError: true, execErr: msg}
	} else {
		switch o := r.GetOutcome().(type) {
		case *llmv1.ToolCallResult_Output:
			out := o.Output
			res = Result{Content: out.GetContent(), IsError: out.GetIsError(), DedupKey: out.GetDedupKey(), Attachments: out.GetAttachments(), EndsTurn: out.GetEndsTurn(), Cards: out.GetCards()}
			if out.GetIsError() {
				res.execErr = out.GetContent()
			}
		case *llmv1.ToolCallResult_Failure:
			f := o.Failure
			res = Result{Content: f.GetMessage(), IsError: true, execErr: fmt.Sprintf("%s: %s", f.GetCode(), f.GetMessage())}
		default:
			msg := "executor result has no outcome"
			llmdlog.Stdout(logModule, llmdlog.LevelWarn, "%s: tenant=%s executor_id=%s call_id=%s", msg, tenant, pc.executorID, id)
			res = Result{Content: msg, IsError: true, execErr: msg}
		}
	}
	select {
	case pc.ch <- res:
	default:
	}
	return true
}
