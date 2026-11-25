// Package retry implements a retry policy with full-jitter exponential backoff
// for LLM backend calls. It honors the structured provider errors produced by
// llmerr (retryable marker and retry_after), the caller's context deadline and a
// per-attempt timeout.
package retry

import (
	"context"
	"math/rand"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/llmerr"
	"github.com/johnewart/pyxis/llmdlog"
)

const logModule = "llm.retry"

// Clock abstracts time so tests can run without real sleeping.
type Clock interface {
	Now() time.Time
	// Sleep blocks for d or until ctx is done, returning ctx.Err() in that case.
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Policy configures retries. Use DefaultPolicy for server defaults.
type Policy struct {
	// MaxAttempts is the server default total attempts (first try included).
	MaxAttempts int
	// BaseDelay is the backoff ceiling before the first retry; it doubles per attempt.
	BaseDelay time.Duration
	// MaxDelay caps the backoff ceiling.
	MaxDelay time.Duration
	// MinRemaining is the least time that must remain before the context
	// deadline (after the backoff wait) for another attempt to start.
	MinRemaining time.Duration

	// Provider and Model identify the backend in per-attempt logs.
	// ProviderType is used to classify errors that are not yet structured.
	Provider     string
	Model        string
	ProviderType llmv1.LlmProviderType

	// Clock and Rand are injectable for tests; nil uses real time and math/rand.
	Clock Clock
	Rand  func() float64 // returns a value in [0, 1)
}

// DefaultPolicy returns the server default policy.
func DefaultPolicy() Policy {
	return Policy{
		MaxAttempts:  3,
		BaseDelay:    500 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		MinRemaining: time.Second,
	}
}

func (p Policy) clock() Clock {
	if p.Clock != nil {
		return p.Clock
	}
	return realClock{}
}

func (p Policy) rnd() float64 {
	if p.Rand != nil {
		return p.Rand()
	}
	return rand.Float64()
}

// attempts resolves the per-call override against the server default.
func (p Policy) attempts(opt *llmv1.CallOptions) int {
	n := p.MaxAttempts
	if opt != nil && opt.MaxAttempts != nil && opt.GetMaxAttempts() > 0 {
		n = int(opt.GetMaxAttempts())
	}
	if n < 1 {
		n = 1
	}
	return n
}

// Backoff returns the full-jitter delay before retry number attempt (1 = first
// retry): a uniform value in [0, min(MaxDelay, BaseDelay*2^(attempt-1))).
func (p Policy) Backoff(attempt int) time.Duration {
	ceil := p.BaseDelay
	for i := 1; i < attempt && (p.MaxDelay <= 0 || ceil < p.MaxDelay); i++ {
		ceil *= 2
	}
	if p.MaxDelay > 0 && ceil > p.MaxDelay {
		ceil = p.MaxDelay
	}
	return time.Duration(p.rnd() * float64(ceil))
}

// classify uses the structured LlmError detail when present and otherwise
// falls back to llmerr.Classify for raw provider errors.
func (p Policy) classify(err error) llmerr.Classification {
	if c, ok := llmerr.FromStatus(err); ok {
		return c
	}
	return llmerr.Classify(err, p.ProviderType)
}

func (p Policy) logf(level llmdlog.Level, op string, attempt int, kind llmv1.LlmErrorKind, format string, args ...any) {
	prefix := "%s provider=%s model=%s attempt=%d kind=%s: "
	all := append([]any{op, p.Provider, p.Model, attempt, kind.String()}, args...)
	llmdlog.Stderr(logModule, level, prefix+format, all...)
}

// attemptCtx derives the per-attempt context; 0 timeout means none.
func attemptCtx(ctx context.Context, opt *llmv1.CallOptions) (context.Context, context.CancelFunc) {
	if opt != nil && opt.GetAttemptTimeoutMs() > 0 {
		return context.WithTimeout(ctx, time.Duration(opt.GetAttemptTimeoutMs())*time.Millisecond)
	}
	return context.WithCancel(ctx)
}

// Do runs fn until it succeeds, fails with a non-retryable error, attempts are
// exhausted, or the deadline leaves too little time. fn receives the per-attempt
// context. The last attempt's error is returned unchanged. If fn reports
// retriable=false (for example a stream that already emitted frames), the error
// is returned immediately.
func (p Policy) Do(ctx context.Context, opt *llmv1.CallOptions, op string, fn func(ctx context.Context) (retriable bool, err error)) error {
	maxAttempts := p.attempts(opt)
	clk := p.clock()
	for attempt := 1; ; attempt++ {
		actx, cancel := attemptCtx(ctx, opt)
		ok, err := fn(actx)
		cancel()
		if err == nil {
			return nil
		}
		c := p.classify(err)
		kind := c.Kind
		if !ok || !c.Retryable {
			p.logf(llmdlog.LevelWarn, op, attempt, kind, "failed, not retrying (retryable=%t retriable_state=%t): %v", c.Retryable, ok, err)
			return err
		}
		if attempt >= maxAttempts {
			p.logf(llmdlog.LevelWarn, op, attempt, kind, "failed, attempts exhausted (max=%d): %v", maxAttempts, err)
			return err
		}
		if ctx.Err() != nil {
			p.logf(llmdlog.LevelWarn, op, attempt, kind, "failed, context done (%v): %v", ctx.Err(), err)
			return err
		}
		delay := p.Backoff(attempt)
		if c.RetryAfter > delay {
			delay = c.RetryAfter
		}
		if dl, has := ctx.Deadline(); has {
			if dl.Sub(clk.Now())-delay < p.MinRemaining {
				p.logf(llmdlog.LevelWarn, op, attempt, kind, "failed, deadline too close to retry (delay=%s): %v", delay, err)
				return err
			}
		}
		p.logf(llmdlog.LevelInfo, op, attempt, kind, "failed, retrying in %s (max=%d): %v", delay, maxAttempts, err)
		if serr := clk.Sleep(ctx, delay); serr != nil {
			p.logf(llmdlog.LevelWarn, op, attempt, kind, "retry wait interrupted (%v): %v", serr, err)
			return err
		}
	}
}

// Unary retries a unary call and returns the reply of the successful attempt.
func Unary[T any](ctx context.Context, p Policy, opt *llmv1.CallOptions, op string, fn func(ctx context.Context) (T, error)) (T, error) {
	var out T
	err := p.Do(ctx, opt, op, func(actx context.Context) (bool, error) {
		r, err := fn(actx)
		if err == nil {
			out = r
		}
		return true, err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

// IsDeltaFrame reports whether f carries model output (a text, thinking or
// tool-call delta). Once one is forwarded the stream cannot be retried.
func IsDeltaFrame(f *llmv1.ConversationStreamFrame) bool {
	switch f.GetFrame().(type) {
	case *llmv1.ConversationStreamFrame_AssistantThinkingDelta,
		*llmv1.ConversationStreamFrame_AssistantTextDelta,
		*llmv1.ConversationStreamFrame_ToolCallStreamStarted,
		*llmv1.ConversationStreamFrame_ToolCallArgsDelta,
		*llmv1.ConversationStreamFrame_ToolCallStreamFinished:
		return true
	}
	return false
}

// Stream retries a streaming call. emit is wrapped so that once the first
// delta frame has been delivered the call is no longer retried (frames cannot
// be unsent). Non-delta frames before that do not block a retry.
func Stream(ctx context.Context, p Policy, opt *llmv1.CallOptions, op string,
	emit func(*llmv1.ConversationStreamFrame) error,
	fn func(ctx context.Context, emit func(*llmv1.ConversationStreamFrame) error) error) error {
	emitted := false
	wrapped := func(f *llmv1.ConversationStreamFrame) error {
		if IsDeltaFrame(f) {
			emitted = true
		}
		return emit(f)
	}
	return p.Do(ctx, opt, op, func(actx context.Context) (bool, error) {
		err := fn(actx, wrapped)
		return !emitted, err
	})
}
