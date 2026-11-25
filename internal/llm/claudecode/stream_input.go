package claudecodeclillm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

type streamUserEnvelope struct {
	Type            string         `json:"type"`
	SessionID       string         `json:"session_id"`
	Message         map[string]any `json:"message"`
	ParentToolUseID *string        `json:"parent_tool_use_id"`
}

func newStreamRequestID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("req_llmd_%s", hex.EncodeToString(b[:]))
}

func writeInitializeRequest(w io.Writer, requestID string) error {
	payload := map[string]any{
		"type":       "control_request",
		"request_id": requestID,
		"request": map[string]any{
			"subtype": "initialize",
			"hooks":   nil,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = w.Write(append(raw, '\n'))
	return err
}

func writeStreamUserLines(w io.Writer, lines []string) error {
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if _, err := io.WriteString(w, line); err != nil {
			return err
		}
		if !strings.HasSuffix(line, "\n") {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

// messagesToStreamUserLines converts llmd messages to Claude Code stream-json stdin input.
//
// It always returns exactly ONE user frame. stream-json input is a queue of user
// turns, not a history format: every user frame can start its own CLI turn, and
// frames that arrive while a turn is running get batched into the next one,
// depending on timing. Several frames therefore produce several `result` lines,
// and consumeStreamV2 stops at the first, returning the answer to an earlier part
// of the conversation and killing the CLI before the real answer. So everything
// for this turn goes into one frame as ordered text blocks.
//
// When resumeSessionID is set, only user-role messages are included (the Claude
// session already holds prior turns). Otherwise (full resync) the whole transcript
// is replayed. Assistant turns and tool results are flattened to labelled text: the
// CLI does not accept real assistant/tool_use history over stdin (the model treats
// injected assistant frames as untrusted), and a tool_result block without its
// tool_use would be rejected by the API.
func messagesToStreamUserLines(messages []*llmv1.Message, resumeSessionID string) ([]string, error) {
	resumed := strings.TrimSpace(resumeSessionID) != ""
	var blocks []map[string]any
	for _, m := range messages {
		if text, ok := messageToStreamText(m, resumed); ok {
			blocks = append(blocks, map[string]any{"type": "text", "text": text})
		}
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("claude-code-cli-v2: no non-empty message content")
	}
	line, _, err := marshalStreamUserLine("", blocks)
	if err != nil {
		return nil, err
	}
	return []string{line}, nil
}

// messageToStreamText renders one llmd message as a text block for the single
// user frame, or ok=false when it contributes nothing to this turn.
func messageToStreamText(m *llmv1.Message, resumed bool) (string, bool) {
	if m == nil {
		return "", false
	}
	role := strings.ToLower(strings.TrimSpace(m.GetRole()))
	content := strings.TrimSpace(m.GetContent())
	if role == "system" || content == "" {
		return "", false
	}
	switch role {
	case "user":
		return content, true
	case "assistant":
		if resumed {
			return "", false
		}
		return "[Previous assistant reply]\n" + content, true
	case "tool":
		if resumed {
			return "", false
		}
		if id := strings.TrimSpace(m.GetToolCallId()); id != "" {
			return "[Tool result " + id + "]\n" + content, true
		}
		return "[Tool result]\n" + content, true
	default:
		return "", false
	}
}

func marshalStreamUserLine(text string, blocks []map[string]any) (string, bool, error) {
	var content any
	switch {
	case len(blocks) > 0:
		content = blocks
	case strings.TrimSpace(text) != "":
		content = text
	default:
		return "", false, nil
	}
	env := streamUserEnvelope{
		Type:            "user",
		SessionID:       "",
		Message:         map[string]any{"role": "user", "content": content},
		ParentToolUseID: nil,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", false, fmt.Errorf("claude-code-cli-v2: marshal user frame: %w", err)
	}
	return string(raw), true, nil
}
