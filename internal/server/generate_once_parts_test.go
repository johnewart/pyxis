package server

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// generateCaptureBackend records the prompt handed to Generate.
type generateCaptureBackend struct {
	completeBackend
	prompt string
	called int
}

func (b *generateCaptureBackend) Generate(_ context.Context, _ string, prompt string, _ *llmv1.CallOptions, _ []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	b.called++
	b.prompt = prompt
	return &llmv1.AssistantReply{Content: "generated"}, nil
}

func newGenerateCaptureServer(t *testing.T) (*LlmServer, *generateCaptureBackend) {
	t.Helper()
	s, _, _ := newCompleteTestServerDB(t)
	e, ok := s.Providers.Get(context.Background(), testScope, "claude")
	if !ok || e == nil {
		t.Fatal("provider claude not resolvable")
	}
	be := &generateCaptureBackend{}
	e.Backend = be
	return s, be
}

func TestGenerateOnce_EmptyPartsUsesPrompt(t *testing.T) {
	s, be := newGenerateCaptureServer(t)
	ctx := testScope.IncomingContext(context.Background())
	if _, err := s.GenerateOnce(ctx, &llmv1.GenerateOnceRequest{ProviderId: strPtr("claude"), Prompt: "hello"}); err != nil {
		t.Fatal(err)
	}
	if be.called != 1 || be.prompt != "hello" {
		t.Fatalf("called=%d prompt=%q", be.called, be.prompt)
	}
}

func TestGenerateOnce_PartsJoinedForPlainBackend(t *testing.T) {
	s, be := newGenerateCaptureServer(t)
	ctx := testScope.IncomingContext(context.Background())
	_, err := s.GenerateOnce(ctx, &llmv1.GenerateOnceRequest{
		ProviderId: strPtr("claude"),
		PromptParts: []*llmv1.PromptPart{
			{Text: "summary"},
			{Text: "turn", CacheBreakpoint: true},
			{Text: "choice"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if be.called != 1 || be.prompt != "summary\n\nturn\n\nchoice" {
		t.Fatalf("called=%d prompt=%q", be.called, be.prompt)
	}
}

func TestGenerateOnce_PromptAndPartsRejected(t *testing.T) {
	s, be := newGenerateCaptureServer(t)
	ctx := testScope.IncomingContext(context.Background())
	_, err := s.GenerateOnce(ctx, &llmv1.GenerateOnceRequest{
		ProviderId:  strPtr("claude"),
		Prompt:      "hello",
		PromptParts: []*llmv1.PromptPart{{Text: "a"}},
	})
	if c := status.Code(err); c != codes.InvalidArgument {
		t.Fatalf("code=%v err=%v want InvalidArgument", c, err)
	}
	if be.called != 0 {
		t.Fatalf("backend called %d times", be.called)
	}
}
