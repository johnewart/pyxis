package toolexec

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/toolmerge"
	"github.com/johnewart/pyxis/internal/tracing"
	"github.com/johnewart/pyxis/toolseam"
)

const (
	// DefaultMaxToolRounds is the maximum number of LLM invocations (including the first) per Chat/ChatStream when tools are used.
	DefaultMaxToolRounds = 48
	// MaxToolRoundsCap is a hard upper bound to avoid runaway loops.
	MaxToolRoundsCap      = 128
	DefaultWebhookTimeout = 30 * time.Second
	maxWebhookBodyBytes   = 256 * 1024
)

// webhookClient sends webhook requests with W3C traceparent for the span in
// the request context.
var webhookClient = &http.Client{Transport: tracing.HTTPTransport(nil, nil)}

// Invocation is one tool_use the model requested. Alias of toolseam.Invocation.
type Invocation = toolseam.Invocation

// ReadOnlyMutationError is returned when a mutation tool is invoked on a read-only turn.
const ReadOnlyMutationError = "read-only turn: mutation tool rejected"

// ExecutorNotConfiguredError is returned when a tool names an external
// executor but no ExecutorDispatcher is installed on the handler set.
const ExecutorNotConfiguredError = "executor dispatch is not configured"

// RunWithHandlers executes the tool per ToolDefinition.execution against an
// explicit inline-builtin handler set. Every production and test caller uses
// this: there is no process-wide fallback — a caller that forgets to wire a
// handler set must pass one explicitly, so two independently constructed
// servers can never share registration.
//
// inv is the single source of truth for invocation identity. It used to be
// passed twice (once here, once inside an Env that this function overwrote),
// which made a caller's workspace binding silently discardable; there is now
// only one copy and no way for the two to drift.
func RunWithHandlers(ctx context.Context, def *llmv1.ToolDefinition, inv Invocation, handlers *Handlers) (content string, success bool, errMsg string) {
	obs := handlers.Observer()
	if obs == nil {
		return runWithHandlers(ctx, def, inv, handlers)
	}
	done := obs.Begin(def.GetName(), inv.Origin, bindingOf(def))
	defer func() {
		done(toolseam.InvocationResult{
			Outcome:     outcomeOf(ctx, def, inv, handlers, content, success),
			ResultBytes: len(content),
		})
	}()
	return runWithHandlers(ctx, def, inv, handlers)
}

// bindingOf names how def executes, for observers.
func bindingOf(def *llmv1.ToolDefinition) toolseam.Binding {
	switch {
	case def.GetHttpWebhook() != nil:
		return toolseam.BindingWebhook
	case def.GetInlineBuiltin() != nil:
		return toolseam.BindingInline
	case def.GetDelegateToClient() != nil:
		return toolseam.BindingDelegate
	case def.GetExecutor() != nil:
		return toolseam.BindingExecutor
	default:
		return toolseam.BindingNone
	}
}

// outcomeOf classifies a finished dispatch. It mirrors runWithHandlers' own
// early refusals so they are counted apart from tool errors.
func outcomeOf(ctx context.Context, def *llmv1.ToolDefinition, inv Invocation, handlers *Handlers, content string, success bool) toolseam.Outcome {
	switch {
	case success:
		return toolseam.OutcomeOK
	case inv.ReadOnly && toolmerge.IsMutationTool(def):
		return toolseam.OutcomeReadOnly
	case def == nil:
		return toolseam.OutcomeNotRegistered
	case bindingOf(def) == toolseam.BindingNone, bindingOf(def) == toolseam.BindingDelegate,
		bindingOf(def) == toolseam.BindingExecutor && content == ExecutorNotConfiguredError:
		return toolseam.OutcomeNotConfigured
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return toolseam.OutcomeTimeout
	case errors.Is(ctx.Err(), context.Canceled):
		return toolseam.OutcomeCanceled
	default:
		return toolseam.OutcomeError
	}
}

func runWithHandlers(ctx context.Context, def *llmv1.ToolDefinition, inv Invocation, handlers *Handlers) (content string, success bool, errMsg string) {
	if inv.ReadOnly && toolmerge.IsMutationTool(def) {
		return ReadOnlyMutationError, false, ReadOnlyMutationError
	}
	if def == nil {
		return "tool is not registered", false, "tool is not registered"
	}
	switch {
	case def.GetHttpWebhook() != nil:
		return runHTTPWebhook(ctx, def.GetHttpWebhook(), def.GetName(), inv)
	case def.GetInlineBuiltin() != nil:
		name := strings.TrimSpace(def.GetName())
		if name == "" {
			name = strings.TrimSpace(inv.Name)
		}
		return runInlineBuiltin(ctx, name, handlers, inv)
	case def.GetDelegateToClient() != nil:
		return "client-delegated tools are handled by the gRPC server (ChatStream + SubmitToolResults); toolexec.Run does not execute them", false, "delegate_to_client"
	case def.GetExecutor() != nil:
		if handlers == nil || handlers.Executor() == nil {
			return ExecutorNotConfiguredError, false, ExecutorNotConfiguredError
		}
		// The executor's result may carry cards; it delivers them through
		// the same sink an inline handler's go through.
		return handlers.Executor().Call(toolseam.WithCardSink(ctx, handlers.CardSink()), def, inv)
	default:
		return "tool has no execution configuration (set http_webhook, inline_builtin, delegate_to_client, or executor)", false, "no execution binding"
	}
}

// webhookTypedResult is the opt-in typed webhook response body.
type webhookTypedResult struct {
	Content  *string `json:"content"`
	IsError  bool    `json:"is_error"`
	EndsTurn bool    `json:"ends_turn"`
}

// parseTypedWebhookResult strictly decodes a typed result. ok is false (and the
// caller keeps legacy behavior) unless the media type is application/json and
// the body is exactly an object with only the known fields and a content string.
func parseTypedWebhookResult(contentType string, data []byte) (r webhookTypedResult, ok bool) {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil || mt != "application/json" {
		return r, false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return webhookTypedResult{}, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return webhookTypedResult{}, false
	}
	if r.Content == nil {
		return webhookTypedResult{}, false
	}
	return r, true
}

// webhookPayload is the JSON body POSTed to http_webhook tools.
type webhookPayload struct {
	ToolCallID     string            `json:"tool_call_id"`
	Name           string            `json:"name"`
	ArgumentsJSON  string            `json:"arguments_json"`
	AgentID        string            `json:"agent_id"`
	SessionID      string            `json:"session_id"`
	IdempotencyKey string            `json:"idempotency_key"`
	TenantID       string            `json:"tenant_id"`
	ScopeID        string            `json:"scope_id"`
	PrincipalID    string            `json:"principal_id,omitempty"`
	ToolContext    map[string]string `json:"tool_context,omitempty"`
}

func runHTTPWebhook(ctx context.Context, wh *llmv1.ToolExecutionHttpWebhook, toolName string, inv Invocation) (content string, success bool, errMsg string) {
	if wh == nil {
		return "webhook configuration missing", false, "webhook configuration missing"
	}
	u := strings.TrimSpace(wh.GetUrl())
	if u == "" {
		return "webhook url is missing", false, "webhook url is missing"
	}
	to := time.Duration(wh.GetTimeoutSeconds()) * time.Second
	if to <= 0 {
		to = DefaultWebhookTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	// The signature below is computed over exactly these marshaled bytes.
	body, err := json.Marshal(webhookPayload{
		ToolCallID:     inv.ToolCallID,
		Name:           inv.Name,
		ArgumentsJSON:  inv.ArgumentsJSON,
		AgentID:        inv.AgentID,
		SessionID:      inv.SessionID,
		IdempotencyKey: inv.ToolCallID,
		TenantID:       inv.Scope.TenantID,
		ScopeID:        inv.Scope.ScopeID,
		PrincipalID:    inv.Scope.PrincipalID,
		ToolContext:    inv.ToolContext,
	})
	if err != nil {
		return fmt.Sprintf("failed to build webhook payload: %v", err), false, err.Error()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err.Error(), false, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	if tok := strings.TrimSpace(wh.GetBearerToken()); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if sec := strings.TrimSpace(wh.GetHmacSecret()); sec != "" {
		mac := hmac.New(sha256.New, []byte(sec))
		_, _ = mac.Write(body)
		req.Header.Set("X-Llmd-Signature", hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := webhookClient.Do(req)
	if err != nil {
		return fmt.Sprintf("webhook request failed: %v", err), false, err.Error()
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWebhookBodyBytes+1))
	if err != nil {
		return fmt.Sprintf("failed to read webhook response: %v", err), false, err.Error()
	}
	if len(data) > maxWebhookBodyBytes {
		return "webhook response body too large", false, "response body too large"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(data))
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return msg, false, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	if tr, ok := parseTypedWebhookResult(resp.Header.Get("Content-Type"), data); ok {
		if tr.IsError {
			msg := *tr.Content
			if strings.TrimSpace(msg) == "" {
				msg = "webhook reported is_error"
			}
			return *tr.Content, false, msg
		}
		if tr.EndsTurn {
			name := strings.TrimSpace(toolName)
			if name == "" {
				name = inv.Name
			}
			toolseam.TurnControlFrom(ctx).RequestEnd(toolseam.EndTurnRequest{ToolCallID: inv.ToolCallID, ToolName: name, Source: "webhook"})
		}
		return *tr.Content, true, ""
	}
	return string(data), true, ""
}

// EffectiveMaxToolRounds returns n from options or DefaultMaxToolRounds, capped at MaxToolRoundsCap.
func EffectiveMaxToolRounds(opt *llmv1.CallOptions) int {
	if opt == nil || opt.MaxToolRounds == nil {
		return DefaultMaxToolRounds
	}
	n := int(*opt.MaxToolRounds)
	if n <= 0 {
		return DefaultMaxToolRounds
	}
	if n > MaxToolRoundsCap {
		return MaxToolRoundsCap
	}
	return n
}

const defaultDelegateWait = 120 * time.Second

// DelegateWaitDuration returns how long to wait for SubmitToolResults for delegate_to_client tools.
func DelegateWaitDuration(def *llmv1.ToolDefinition) time.Duration {
	if def == nil {
		return defaultDelegateWait
	}
	d := def.GetDelegateToClient()
	if d == nil || d.GetTimeoutSeconds() <= 0 {
		return defaultDelegateWait
	}
	return time.Duration(d.GetTimeoutSeconds()) * time.Second
}
