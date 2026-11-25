package backend

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// plainBackend implements only Backend (no GenerateParts).
type plainBackend struct {
	system, prompt string
}

func (p *plainBackend) Chat(context.Context, []*llmv1.Message, *llmv1.CallOptions, []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	return nil, nil
}

func (p *plainBackend) Generate(_ context.Context, system, prompt string, _ *llmv1.CallOptions, _ []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	p.system, p.prompt = system, prompt
	return &llmv1.AssistantReply{Content: "ok"}, nil
}

func (p *plainBackend) ChatStream(context.Context, []*llmv1.Message, *llmv1.CallOptions, func() []*llmv1.ToolDefinition, func(*llmv1.ConversationStreamFrame) error) error {
	return nil
}

func TestGenerateWithParts_FallbackJoinsText(t *testing.T) {
	be := &plainBackend{}
	parts := []*llmv1.PromptPart{
		{Text: "summary"},
		{Text: "turn one", CacheBreakpoint: true},
		{Text: "choice"},
	}
	if _, err := GenerateWithParts(context.Background(), be, "sys", parts, nil, nil); err != nil {
		t.Fatal(err)
	}
	want := "summary\n\nturn one\n\nchoice"
	if be.prompt != want {
		t.Fatalf("prompt=%q want %q", be.prompt, want)
	}
	if be.system != "sys" {
		t.Fatalf("system=%q", be.system)
	}
}

// partsBackend also implements PartsGenerator.
type partsBackend struct {
	plainBackend
	gotParts       []*llmv1.PromptPart
	gotSystem      string
	generateCalled int
	partsCalled    int
}

func (p *partsBackend) Generate(ctx context.Context, system, prompt string, o *llmv1.CallOptions, tools []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	p.generateCalled++
	return p.plainBackend.Generate(ctx, system, prompt, o, tools)
}

func (p *partsBackend) GenerateParts(_ context.Context, system string, parts []*llmv1.PromptPart, _ *llmv1.CallOptions, _ []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	p.partsCalled++
	p.gotSystem, p.gotParts = system, parts
	return &llmv1.AssistantReply{Content: "parts"}, nil
}

func TestGenerateWithParts_DispatchesToPartsGenerator(t *testing.T) {
	be := &partsBackend{}
	parts := []*llmv1.PromptPart{{Text: "a"}, {Text: "b", CacheBreakpoint: true}}
	reply, err := GenerateWithParts(context.Background(), be, "sys", parts, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply.GetContent() != "parts" {
		t.Fatalf("content=%q", reply.GetContent())
	}
	if be.partsCalled != 1 || be.generateCalled != 0 {
		t.Fatalf("GenerateParts=%d Generate=%d", be.partsCalled, be.generateCalled)
	}
	if be.gotSystem != "sys" || len(be.gotParts) != 2 || be.gotParts[0] != parts[0] || be.gotParts[1] != parts[1] {
		t.Fatalf("parts not passed unchanged: %q %v", be.gotSystem, be.gotParts)
	}
}

func TestJoinPromptParts(t *testing.T) {
	got := JoinPromptParts([]*llmv1.PromptPart{{Text: "a"}, {Text: ""}, {Text: "b", CacheBreakpoint: true}, nil, {Text: "c"}})
	if want := "a\n\nb\n\nc"; got != want {
		t.Fatalf("joined=%q want %q", got, want)
	}
	if got := JoinPromptParts(nil); got != "" {
		t.Fatalf("empty joined=%q", got)
	}
}
