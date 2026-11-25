package policyhook

import (
	"context"
	"time"

	"github.com/johnewart/pyxis/llmdlog"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const logModule = "policyhook"

// Guard wraps Hooks and applies the fail mode and metrics. A hook error or
// timeout becomes a decision (DENY when closed, ALLOW when open), so the
// methods return a nil error. The no-op hook never fails and is not measured.
//
// Every hook call runs inside a policy.<hook> span (policy.check_llm_call,
// policy.filter_tools, policy.check_tool_call) started in Guard.run.
type Guard struct {
	hooks   Hooks
	mode    FailMode
	metrics *Metrics
	tracer  trace.Tracer
	noop    bool
}

// NewGuard wraps hooks. metrics and tracer may be nil (nil tracer: no spans).
func NewGuard(hooks Hooks, mode FailMode, metrics *Metrics, tracer trace.Tracer) *Guard {
	if hooks == nil {
		hooks = Noop{}
	}
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("policyhook")
	}
	_, isNoop := hooks.(Noop)
	return &Guard{hooks: hooks, mode: mode, metrics: metrics, tracer: tracer, noop: isNoop}
}

// run times a hook call inside a policy.<hook> span and returns the outcome
// label for it. decided reports the hook's decision after a successful call;
// on error the fail-mode decision is recorded instead.
func (g *Guard) run(ctx context.Context, hook string, call func(context.Context) error, decided func() Decision) (outcome string, err error) {
	ctx, span := g.tracer.Start(ctx, "policy."+hook, trace.WithAttributes(attribute.String("policy.hook", hook)))
	defer span.End()
	err = call(ctx)
	outcome = "ok"
	decision := decided()
	if err != nil {
		outcome = "error"
		if IsTimeout(err) {
			outcome = "timeout"
		}
		decision = g.failDecision()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.SetAttributes(attribute.String("policy.decision", decision.String()), attribute.String("policy.outcome", outcome))
	if err != nil {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn, "hook failed: hook=%s outcome=%s mode=%s err=%v", hook, outcome, g.mode, err)
	}
	return outcome, err
}

func (g *Guard) failDecision() Decision {
	if g.mode == FailOpen {
		return DecisionAllow
	}
	return DecisionDeny
}

// GetCheckPolicy passes through to the wrapped hooks.
func (g *Guard) GetCheckPolicy(ctx context.Context) (HookPolicies, error) {
	return g.hooks.GetCheckPolicy(ctx)
}

func (g *Guard) CheckLLMCall(ctx context.Context, r LLMCallRequest) (Verdict, error) {
	return g.verdict(ctx, "check_llm_call", func(c context.Context) (Verdict, error) { return g.hooks.CheckLLMCall(c, r) })
}

func (g *Guard) CheckToolCall(ctx context.Context, r ToolCallRequest) (Verdict, error) {
	return g.verdict(ctx, "check_tool_call", func(c context.Context) (Verdict, error) { return g.hooks.CheckToolCall(c, r) })
}

func (g *Guard) verdict(ctx context.Context, hook string, fn func(context.Context) (Verdict, error)) (Verdict, error) {
	if g.noop {
		return Verdict{Decision: DecisionAllow}, nil
	}
	start := time.Now()
	var v Verdict
	outcome, err := g.run(ctx, hook, func(c context.Context) error {
		var e error
		v, e = fn(c)
		return e
	}, func() Decision { return v.Decision })
	if err != nil {
		v = Verdict{Decision: g.failDecision(), Reason: "policy hook " + outcome + ": " + err.Error()}
	}
	g.metrics.observe(hook, v.Decision, outcome, time.Since(start).Seconds())
	return v, nil
}

func (g *Guard) FilterTools(ctx context.Context, r FilterToolsRequest) (FilterResult, error) {
	if g.noop {
		return FilterResult{Decision: DecisionAllow, AllowedToolNames: allToolNames(r.Tools)}, nil
	}
	start := time.Now()
	var res FilterResult
	outcome, err := g.run(ctx, "filter_tools", func(c context.Context) error {
		var e error
		res, e = g.hooks.FilterTools(c, r)
		return e
	}, func() Decision { return res.Decision })
	if err != nil {
		res = FilterResult{Decision: g.failDecision(), Reason: "policy hook " + outcome + ": " + err.Error()}
		if res.Decision == DecisionAllow {
			res.AllowedToolNames = allToolNames(r.Tools)
		}
	} else {
		// Keep only names that are real tools, in the original tool order.
		allowed := make(map[string]bool, len(res.AllowedToolNames))
		for _, n := range res.AllowedToolNames {
			allowed[n] = true
		}
		kept := make([]string, 0, len(allowed))
		for _, n := range allToolNames(r.Tools) {
			if allowed[n] {
				kept = append(kept, n)
			}
		}
		res.AllowedToolNames = kept
		g.metrics.toolsFiltered(len(r.Tools) - len(kept))
	}
	g.metrics.observe("filter_tools", res.Decision, outcome, time.Since(start).Seconds())
	return res, nil
}
