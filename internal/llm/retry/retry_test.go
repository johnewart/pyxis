package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

func llmErr(t *testing.T, retryable bool, after time.Duration) error {
	t.Helper()
	d := &llmv1.LlmError{Retryable: retryable}
	if after > 0 {
		d.RetryAfter = durationpb.New(after)
	}
	st, err := status.New(codes.Unavailable, "x").WithDetails(d)
	if err != nil {
		t.Fatalf("WithDetails: %v", err)
	}
	return st.Err()
}

func testPolicy(clk *fakeClock, r float64) Policy {
	return Policy{MaxAttempts: 3, BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second,
		MinRemaining: 50 * time.Millisecond, Clock: clk, Rand: func() float64 { return r }}
}

func TestRetriesUpToLimitAndReturnsLastError(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	calls := 0
	var last error
	_, err := Unary(context.Background(), testPolicy(clk, 0.5), nil, "chat", func(context.Context) (int, error) {
		calls++
		last = llmErr(t, true, 0)
		return 0, last
	})
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	if err != last {
		t.Fatalf("final error not the last attempt's error unchanged: %v", err)
	}
}

func TestPerCallMaxAttemptsOverride(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	n := int32(5)
	calls := 0
	_, _ = Unary(context.Background(), testPolicy(clk, 0), &llmv1.CallOptions{MaxAttempts: &n}, "chat", func(context.Context) (int, error) {
		calls++
		return 0, llmErr(t, true, 0)
	})
	if calls != 5 {
		t.Fatalf("calls = %d, want 5", calls)
	}
}

func TestNonRetryableNotRetried(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	for _, e := range []error{llmErr(t, false, 0), errors.New("plain")} {
		calls := 0
		_, err := Unary(context.Background(), testPolicy(clk, 0), nil, "chat", func(context.Context) (int, error) {
			calls++
			return 0, e
		})
		if calls != 1 || err != e {
			t.Fatalf("calls = %d err = %v", calls, err)
		}
	}
}

func TestSuccessAfterRetry(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	calls := 0
	got, err := Unary(context.Background(), testPolicy(clk, 0.5), nil, "chat", func(context.Context) (string, error) {
		calls++
		if calls < 2 {
			return "", llmErr(t, true, 0)
		}
		return "ok", nil
	})
	if err != nil || got != "ok" || calls != 2 {
		t.Fatalf("got %q err %v calls %d", got, err, calls)
	}
}

func TestDeadlineMinimumStopsRetry(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	// 150ms left, backoff 50ms leaves 100ms, below the 120ms minimum.
	p := testPolicy(clk, 0.5)
	p.MinRemaining = 120 * time.Millisecond
	ctx, cancel := context.WithDeadline(context.Background(), clk.now.Add(150*time.Millisecond))
	defer cancel()
	calls := 0
	_, err := Unary(ctx, p, nil, "chat", func(context.Context) (int, error) {
		calls++
		return 0, llmErr(t, true, 0)
	})
	if calls != 1 || err == nil || len(clk.sleeps) != 0 {
		t.Fatalf("calls = %d sleeps = %v err = %v", calls, clk.sleeps, err)
	}
}

func TestRetryAfterHonored(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	calls := 0
	_, _ = Unary(context.Background(), testPolicy(clk, 0), nil, "chat", func(context.Context) (int, error) {
		calls++
		return 0, llmErr(t, true, 3*time.Second)
	})
	if len(clk.sleeps) != 2 || clk.sleeps[0] != 3*time.Second || clk.sleeps[1] != 3*time.Second {
		t.Fatalf("sleeps = %v", clk.sleeps)
	}
}

func TestRetryAfterBeyondDeadlineStops(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	ctx2, cancel2 := context.WithDeadline(context.Background(), clk.now.Add(2*time.Second))
	defer cancel2()
	calls := 0
	_, _ = Unary(ctx2, testPolicy(clk, 0), nil, "chat", func(context.Context) (int, error) {
		calls++
		return 0, llmErr(t, true, 5*time.Second)
	})
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestFullJitterBounds(t *testing.T) {
	p := Policy{BaseDelay: 100 * time.Millisecond, MaxDelay: 500 * time.Millisecond}
	cases := []struct {
		attempt int
		ceil    time.Duration
	}{{1, 100 * time.Millisecond}, {2, 200 * time.Millisecond}, {3, 400 * time.Millisecond}, {4, 500 * time.Millisecond}, {10, 500 * time.Millisecond}}
	for _, c := range cases {
		p.Rand = func() float64 { return 0 }
		if d := p.Backoff(c.attempt); d != 0 {
			t.Fatalf("attempt %d low = %v", c.attempt, d)
		}
		p.Rand = func() float64 { return 0.999999 }
		if d := p.Backoff(c.attempt); d < 0 || d >= c.ceil {
			t.Fatalf("attempt %d high = %v, ceil %v", c.attempt, d, c.ceil)
		}
	}
}

func TestAttemptTimeout(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	ms := int32(50)
	_, _ = Unary(context.Background(), testPolicy(clk, 0), &llmv1.CallOptions{AttemptTimeoutMs: &ms}, "chat", func(ctx context.Context) (int, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("expected per-attempt deadline")
		}
		return 0, nil
	})
	_, _ = Unary(context.Background(), testPolicy(clk, 0), nil, "chat", func(ctx context.Context) (int, error) {
		if _, ok := ctx.Deadline(); ok {
			t.Error("unexpected deadline when attempt timeout is 0")
		}
		return 0, nil
	})
}

func TestStreamNoRetryAfterEmit(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	calls := 0
	err := Stream(context.Background(), testPolicy(clk, 0), nil, "stream",
		func(*llmv1.ConversationStreamFrame) error { return nil },
		func(_ context.Context, emit func(*llmv1.ConversationStreamFrame) error) error {
			calls++
			_ = emit(textDelta())
			return llmErr(t, true, 0)
		})
	if calls != 1 || err == nil {
		t.Fatalf("calls = %d err = %v", calls, err)
	}
	calls = 0
	_ = Stream(context.Background(), testPolicy(clk, 0), nil, "stream",
		func(*llmv1.ConversationStreamFrame) error { return nil },
		func(context.Context, func(*llmv1.ConversationStreamFrame) error) error {
			calls++
			return llmErr(t, true, 0)
		})
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}
