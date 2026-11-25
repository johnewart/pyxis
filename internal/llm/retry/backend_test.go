package retry

import (
	"context"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

type fakeBackend struct {
	chatErrs     []error // per call; nil entry = success
	chatCalls    int
	genCalls     int
	streamCalls  int
	streamFrames [][]*llmv1.ConversationStreamFrame // per attempt, emitted before returning its error
	streamErrs   []error
}

func (f *fakeBackend) Chat(context.Context, []*llmv1.Message, *llmv1.CallOptions, []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	i := f.chatCalls
	f.chatCalls++
	if i < len(f.chatErrs) && f.chatErrs[i] != nil {
		return nil, f.chatErrs[i]
	}
	return &llmv1.AssistantReply{Content: "ok"}, nil
}

func (f *fakeBackend) Generate(ctx context.Context, _, _ string, _ *llmv1.CallOptions, _ []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	i := f.genCalls
	f.genCalls++
	if i < len(f.chatErrs) && f.chatErrs[i] != nil {
		return nil, f.chatErrs[i]
	}
	return &llmv1.AssistantReply{Content: "ok"}, nil
}

func (f *fakeBackend) ChatStream(_ context.Context, _ []*llmv1.Message, _ *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	i := f.streamCalls
	f.streamCalls++
	if i < len(f.streamFrames) {
		for _, fr := range f.streamFrames[i] {
			if err := emit(fr); err != nil {
				return err
			}
		}
	}
	if i < len(f.streamErrs) {
		return f.streamErrs[i]
	}
	return nil
}

func textDelta() *llmv1.ConversationStreamFrame {
	return &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{AssistantTextDelta: &llmv1.EvAssistantTextDelta{}}}
}

func thinkingDelta() *llmv1.ConversationStreamFrame {
	return &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantThinkingDelta{AssistantThinkingDelta: &llmv1.EvAssistantThinkingDelta{}}}
}

func toolStarted() *llmv1.ConversationStreamFrame {
	return &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_ToolCallStreamStarted{ToolCallStreamStarted: &llmv1.EvToolCallStreamStarted{}}}
}

func turnStarted() *llmv1.ConversationStreamFrame {
	return &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{}}}
}

func wrapped(be *fakeBackend, clk *fakeClock) *retrying {
	return &retrying{inner: be, policy: testPolicy(clk, 0.5)}
}

func TestWrapNilBackend(t *testing.T) {
	if Wrap(nil, DefaultPolicy(), "p") != nil {
		t.Fatal("nil backend must stay nil")
	}
}

func TestChatRetriesThenSucceeds(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	be := &fakeBackend{chatErrs: []error{llmErr(t, true, 0), llmErr(t, true, 0), nil}}
	rep, err := wrapped(be, clk).Chat(context.Background(), nil, nil, nil)
	if err != nil || rep.GetContent() != "ok" {
		t.Fatalf("rep=%v err=%v", rep, err)
	}
	if be.chatCalls != 3 || len(clk.sleeps) != 2 {
		t.Fatalf("calls=%d sleeps=%d", be.chatCalls, len(clk.sleeps))
	}
}

func TestChatNonRetryableSingleAttempt(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	want := llmErr(t, false, 0)
	be := &fakeBackend{chatErrs: []error{want, nil}}
	_, err := wrapped(be, clk).Chat(context.Background(), nil, nil, nil)
	if err != want || be.chatCalls != 1 {
		t.Fatalf("err=%v calls=%d", err, be.chatCalls)
	}
}

func TestChatExhaustedReturnsLastError(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	be := &fakeBackend{chatErrs: []error{llmErr(t, true, 0), llmErr(t, true, 0), llmErr(t, true, 0), nil}}
	_, err := wrapped(be, clk).Chat(context.Background(), nil, nil, nil)
	if err == nil || be.chatCalls != 3 {
		t.Fatalf("err=%v calls=%d", err, be.chatCalls)
	}
}

func TestGenerateRetries(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	be := &fakeBackend{chatErrs: []error{llmErr(t, true, 0), nil}}
	rep, err := wrapped(be, clk).Generate(context.Background(), "s", "p", nil, nil)
	if err != nil || rep == nil || be.genCalls != 2 {
		t.Fatalf("rep=%v err=%v calls=%d", rep, err, be.genCalls)
	}
}

func TestChatNoAttemptWithTooLittleDeadline(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	ctx, cancel := context.WithDeadline(context.Background(), clk.now.Add(10*time.Millisecond))
	defer cancel()
	be := &fakeBackend{chatErrs: []error{llmErr(t, true, 0), nil}}
	_, err := wrapped(be, clk).Chat(ctx, nil, nil, nil)
	if err == nil || be.chatCalls != 1 {
		t.Fatalf("err=%v calls=%d", err, be.chatCalls)
	}
}

func TestStreamRetriesBeforeFirstDelta(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	be := &fakeBackend{
		// Attempt 1 emits only a non-delta frame, then fails; attempt 2 succeeds.
		streamFrames: [][]*llmv1.ConversationStreamFrame{{turnStarted()}, {turnStarted(), textDelta()}},
		streamErrs:   []error{llmErr(t, true, 0), nil},
	}
	var got []*llmv1.ConversationStreamFrame
	err := wrapped(be, clk).ChatStream(context.Background(), nil, nil, nil, func(f *llmv1.ConversationStreamFrame) error {
		got = append(got, f)
		return nil
	})
	if err != nil || be.streamCalls != 2 {
		t.Fatalf("err=%v calls=%d", err, be.streamCalls)
	}
	if len(got) != 3 {
		t.Fatalf("frames forwarded = %d, want 3", len(got))
	}
}

func TestStreamNotRetriedAfterDelta(t *testing.T) {
	cases := map[string]*llmv1.ConversationStreamFrame{
		"text": textDelta(), "thinking": thinkingDelta(), "tool": toolStarted(),
	}
	for name, fr := range cases {
		t.Run(name, func(t *testing.T) {
			clk := &fakeClock{now: time.Now()}
			want := llmErr(t, true, 0)
			be := &fakeBackend{
				streamFrames: [][]*llmv1.ConversationStreamFrame{{fr}, {fr}},
				streamErrs:   []error{want, nil},
			}
			err := wrapped(be, clk).ChatStream(context.Background(), nil, nil, nil, func(*llmv1.ConversationStreamFrame) error { return nil })
			if err != want || be.streamCalls != 1 {
				t.Fatalf("err=%v calls=%d", err, be.streamCalls)
			}
		})
	}
}

func TestStreamNonRetryableNotRetried(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	be := &fakeBackend{streamErrs: []error{llmErr(t, false, 0), nil}}
	err := wrapped(be, clk).ChatStream(context.Background(), nil, nil, nil, func(*llmv1.ConversationStreamFrame) error { return nil })
	if err == nil || be.streamCalls != 1 {
		t.Fatalf("err=%v calls=%d", err, be.streamCalls)
	}
}
