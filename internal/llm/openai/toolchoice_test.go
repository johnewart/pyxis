package openaillm

import (
	"encoding/json"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	openai "github.com/sashabaranov/go-openai"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func toolChoiceReq(mode llmv1.ToolChoice_Mode, name string, tools []*llmv1.ToolDefinition) (openai.ChatCompletionRequest, error) {
	c := New("k", "", "gpt-4o-mini", 1024)
	return c.chatRequest(
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
		false,
		&llmv1.CallOptions{ToolChoice: &llmv1.ToolChoice{Mode: mode, ToolName: name}},
		tools,
	)
}

func wireToolChoice(t *testing.T, req openai.ChatCompletionRequest) string {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return string(m["tool_choice"])
}

func testTools() []*llmv1.ToolDefinition {
	return []*llmv1.ToolDefinition{{Name: "get_weather", Description: "d", ParametersSchemaJson: `{"type":"object"}`}}
}

func TestChatRequest_ToolChoiceWire(t *testing.T) {
	cases := []struct {
		name string
		mode llmv1.ToolChoice_Mode
		tool string
		want string
	}{
		{"unspecified", llmv1.ToolChoice_MODE_UNSPECIFIED, "", `"auto"`},
		{"auto", llmv1.ToolChoice_MODE_AUTO, "", `"auto"`},
		{"none", llmv1.ToolChoice_MODE_NONE, "", `"none"`},
		{"required", llmv1.ToolChoice_MODE_REQUIRED, "", `"required"`},
		{"named", llmv1.ToolChoice_MODE_NAMED, "get_weather", `{"type":"function","function":{"name":"get_weather"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := toolChoiceReq(tc.mode, tc.tool, testTools())
			if err != nil {
				t.Fatalf("chatRequest: %v", err)
			}
			if got := wireToolChoice(t, req); got != tc.want {
				t.Fatalf("tool_choice = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestChatRequest_ToolChoiceUnset(t *testing.T) {
	c := New("k", "", "gpt-4o-mini", 1024)
	req, err := c.chatRequest(nil, false, nil, nil)
	if err != nil {
		t.Fatalf("chatRequest: %v", err)
	}
	if got := wireToolChoice(t, req); got != "" {
		t.Fatalf("expected no tool_choice, got %s", got)
	}
}

func TestChatRequest_ToolChoiceErrors(t *testing.T) {
	cases := []struct {
		name  string
		mode  llmv1.ToolChoice_Mode
		tool  string
		tools []*llmv1.ToolDefinition
	}{
		{"required without tools", llmv1.ToolChoice_MODE_REQUIRED, "", nil},
		{"named without tools", llmv1.ToolChoice_MODE_NAMED, "get_weather", nil},
		{"named unknown", llmv1.ToolChoice_MODE_NAMED, "nope", testTools()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := toolChoiceReq(tc.mode, tc.tool, tc.tools)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("expected InvalidArgument, got %v", err)
			}
		})
	}
}
