//go:build integration

package geminiintegration

import (
	"encoding/json"
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	geminillm "github.com/johnewart/pyxis/internal/llm/gemini"
)

// TestIntegrationBuildTagActive confirms this package only compiles with -tags=integration.
func TestIntegrationBuildTagActive(t *testing.T) {}

func TestIntegrationGeminiGenerate(t *testing.T) {
	c := geminillm.NewIntegrationClient(t)
	ctx := geminillm.IntegrationChatContext(t)

	reply, err := c.Generate(ctx, "You reply tersely.", "Reply exactly: pong", nil, nil)
	geminillm.AssertContextActive(t, ctx, "Generate")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(reply.GetContent()), "pong") {
		t.Fatalf("reply=%q", reply.GetContent())
	}
}

func TestIntegrationGeminiChat(t *testing.T) {
	c := geminillm.NewIntegrationClient(t)
	ctx := geminillm.IntegrationChatContext(t)

	reply, err := c.Chat(ctx, []*llmv1.Message{{Role: "user", Content: "Say hello in one word."}}, nil, nil)
	geminillm.AssertContextActive(t, ctx, "Chat")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(reply.GetContent()) == "" {
		t.Fatal("empty reply")
	}
}

func TestIntegrationGeminiChatMultiTurn(t *testing.T) {
	c := geminillm.NewIntegrationClient(t)
	ctx := geminillm.IntegrationChatContext(t)

	msgs := []*llmv1.Message{
		{Role: "system", Content: "You are a terse assistant."},
		{Role: "user", Content: "My name is Ada."},
		{Role: "assistant", Content: "Nice to meet you, Ada."},
		{Role: "user", Content: "What is my name? Reply with just the name."},
	}
	reply, err := c.Chat(ctx, msgs, nil, nil)
	geminillm.AssertContextActive(t, ctx, "Chat multi-turn")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(reply.GetContent()), "ada") {
		t.Fatalf("reply=%q", reply.GetContent())
	}
}

func TestIntegrationGeminiChatStream(t *testing.T) {
	c := geminillm.NewIntegrationClient(t)
	ctx := geminillm.IntegrationChatContext(t)

	var text strings.Builder
	var sawStarted, sawDone bool
	err := c.ChatStream(ctx, []*llmv1.Message{{Role: "user", Content: "Reply with exactly: stream-ok"}}, nil, nil, func(f *llmv1.ConversationStreamFrame) error {
		if err := geminillm.CheckStreamFrameContext(ctx); err != nil {
			return err
		}
		if f.GetAssistantTurnStarted() != nil {
			sawStarted = true
		} else if d := f.GetAssistantTextDelta(); d != nil {
			text.WriteString(d.GetDelta())
		} else if f.GetAssistantTurnFinished() != nil || f.GetStreamComplete() != nil {
			sawDone = true
		}
		return nil
	})
	geminillm.AssertContextActive(t, ctx, "ChatStream")
	if err != nil {
		t.Fatal(err)
	}
	if !sawStarted || !sawDone {
		t.Fatal("missing stream lifecycle frames")
	}
	if !strings.Contains(strings.ToLower(text.String()), "stream") {
		t.Fatalf("text=%q", text.String())
	}
}

func TestIntegrationGeminiChatWithTools(t *testing.T) {
	c := geminillm.NewIntegrationClient(t)
	ctx := geminillm.IntegrationToolContext(t)

	tools := []*llmv1.ToolDefinition{{
		Name:                 "echo",
		Description:          "Echo the message field back as JSON.",
		ParametersSchemaJson: `{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`,
	}}
	reply, err := c.Chat(ctx, []*llmv1.Message{{Role: "user", Content: "Call echo with message set to hi."}}, nil, tools)
	geminillm.AssertContextActive(t, ctx, "Chat with tools")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.GetToolCalls()) != 1 {
		t.Fatalf("tool calls=%v content=%q", reply.GetToolCalls(), reply.GetContent())
	}
	tc := reply.GetToolCalls()[0]
	if tc.GetName() != "echo" {
		t.Fatalf("tool=%+v", tc)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.GetArgumentsJson()), &args); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationGeminiToolRoundTrip(t *testing.T) {
	c := geminillm.NewIntegrationClient(t)
	ctx := geminillm.IntegrationToolContext(t)

	tools := []*llmv1.ToolDefinition{{
		Name:                 "echo",
		Description:          "Echo the message.",
		ParametersSchemaJson: `{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`,
	}}
	first, err := c.Chat(ctx, []*llmv1.Message{{Role: "user", Content: "Call echo once with message=roundtrip."}}, nil, tools)
	geminillm.AssertContextActive(t, ctx, "tool round-trip first chat")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.GetToolCalls()) != 1 {
		t.Fatalf("first tool calls=%v", first.GetToolCalls())
	}
	tc := first.GetToolCalls()[0]
	msgs := []*llmv1.Message{
		{Role: "user", Content: "Call echo once with message=roundtrip."},
		{Role: "assistant", Content: first.GetContent(), ToolCalls: first.GetToolCalls()},
		{Role: "tool", ToolCallId: tc.GetId(), Content: `{"message":"roundtrip"}`},
		{Role: "user", Content: "What message did echo return? Reply with only that word."},
	}
	second, err := c.Chat(ctx, msgs, nil, nil)
	geminillm.AssertContextActive(t, ctx, "tool round-trip second chat")
	if err != nil {
		t.Fatalf("second chat: %v", err)
	}
	if !strings.Contains(strings.ToLower(second.GetContent()), "roundtrip") {
		t.Fatalf("second reply=%q", second.GetContent())
	}
}

func TestIntegrationGeminiStreamWithTools(t *testing.T) {
	c := geminillm.NewIntegrationClient(t)
	ctx := geminillm.IntegrationToolContext(t)

	tools := []*llmv1.ToolDefinition{{
		Name:                 "echo",
		Description:          "Echo a message.",
		ParametersSchemaJson: `{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`,
	}}
	var sawToolStart bool
	err := c.ChatStream(ctx, []*llmv1.Message{{Role: "user", Content: "Call echo with message=stream-tool."}}, nil, func() []*llmv1.ToolDefinition { return tools }, func(f *llmv1.ConversationStreamFrame) error {
		if err := geminillm.CheckStreamFrameContext(ctx); err != nil {
			return err
		}
		if f.GetToolCallStreamStarted() != nil {
			sawToolStart = true
		}
		return nil
	})
	geminillm.AssertContextActive(t, ctx, "ChatStream with tools")
	if err != nil {
		t.Fatal(err)
	}
	if !sawToolStart {
		t.Fatal("stream missing tool start frame")
	}
}

func TestIntegrationScanGeminiModels(t *testing.T) {
	ctx := geminillm.IntegrationScanContext(t)

	ids, err := geminillm.ScanModels(ctx, geminillm.SkipUnlessGeminiAPIKey(t), "", geminillm.IntegrationCheapModel)
	geminillm.AssertContextActive(t, ctx, "ScanModels")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 {
		t.Fatal("expected models")
	}
	foundCheap := false
	for _, id := range ids {
		if strings.Contains(id, "flash") || id == geminillm.IntegrationCheapModel {
			foundCheap = true
			break
		}
	}
	if !foundCheap {
		t.Logf("models=%v (cheap default may differ by account)", ids)
	}
}
