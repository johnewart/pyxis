package server

import (
	"fmt"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/texttoolcall"
	llmdlog "github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/protobuf/proto"
)

// textToolCallModeToMode maps the wire enum to the detector mode. Unspecified
// means STRIP.
func textToolCallModeToMode(m llmv1.TextToolCallMode) texttoolcall.Mode {
	if m == llmv1.TextToolCallMode_TEXT_TOOL_CALL_MODE_CONVERT {
		return texttoolcall.ModeConvert
	}
	return texttoolcall.ModeStrip
}

// applyTextToolCalls detects tool-call markup in final's content for tools
// offered this round, then strips it or converts it to structured tool calls
// per mode. It returns final unchanged when nothing matches. Every detection is
// logged with provider and model.
func applyTextToolCalls(final *llmv1.AssistantReply, offered []string, mode llmv1.TextToolCallMode, providerID, model, sid string, round int) *llmv1.AssistantReply {
	if final == nil || final.GetContent() == "" || len(offered) == 0 {
		return final
	}
	cleaned, calls, dets := texttoolcall.Process(final.GetContent(), offered, textToolCallModeToMode(mode))
	if len(dets) == 0 {
		return final
	}
	for _, d := range dets {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"ChatStream text tool call markup detected: provider=%q model=%q session=%q tool=%q mode=%s converted=%v parse_error=%q",
			providerID, model, sid, d.Name, mode.String(), d.Converted, d.Error)
	}
	out, ok := proto.Clone(final).(*llmv1.AssistantReply)
	if !ok {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream text tool call: clone reply failed: provider=%q model=%q session=%q", providerID, model, sid)
		return final
	}
	out.Content = cleaned
	for n, c := range calls {
		out.ToolCalls = append(out.ToolCalls, &llmv1.ToolCall{
			Id:            fmt.Sprintf("textcall_%s_r%d_%d", final.GetId(), round, n+1),
			Name:          c.Name,
			ArgumentsJson: c.Arguments,
		})
	}
	return out
}
