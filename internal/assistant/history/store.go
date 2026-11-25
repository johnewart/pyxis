package history

import (
	"sync"
	"unicode/utf8"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// Store is a pluggable chat history backend (mirrors ai-library ChatHistoryStore).
type Store interface {
	Store(sessionID string, message *llmv1.Message)
	History(sessionID string, maxLength int) []*llmv1.Message
	Clear(sessionID string)
	Remove(sessionID string, messageID string)
	SessionIDs() []string
	TokenCount(sessionID string) int64
}

// Memory is an in-process Store keyed by session id.
type Memory struct {
	mu   sync.RWMutex
	data map[string][]*llmv1.Message
}

func NewMemory() *Memory {
	return &Memory{data: make(map[string][]*llmv1.Message)}
}

func (m *Memory) Store(sessionID string, message *llmv1.Message) {
	if sessionID == "" || message == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[sessionID] = append(m.data[sessionID], cloneMessage(message))
}

func (m *Memory) History(sessionID string, maxLength int) []*llmv1.Message {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src := m.data[sessionID]
	if len(src) == 0 {
		return nil
	}
	if maxLength <= 0 || maxLength >= len(src) {
		return cloneSlice(src)
	}
	return cloneSlice(src[len(src)-maxLength:])
}

func (m *Memory) Clear(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, sessionID)
}

func (m *Memory) Remove(sessionID string, messageID string) {
	if sessionID == "" || messageID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.data[sessionID]
	if len(cur) == 0 {
		return
	}
	out := cur[:0]
	for _, msg := range cur {
		if msg != nil && msg.GetId() == messageID {
			continue
		}
		out = append(out, msg)
	}
	if len(out) == 0 {
		delete(m.data, sessionID)
		return
	}
	m.data[sessionID] = out
}

func (m *Memory) SessionIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.data))
	for id := range m.data {
		ids = append(ids, id)
	}
	return ids
}

func (m *Memory) TokenCount(sessionID string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var n int64
	for _, msg := range m.data[sessionID] {
		if msg == nil {
			continue
		}
		n += roughTokens(msg.GetContent())
		for _, tc := range msg.GetToolCalls() {
			if tc == nil {
				continue
			}
			n += roughTokens(tc.GetName())
			n += roughTokens(tc.GetArgumentsJson())
		}
	}
	return n
}

func roughTokens(s string) int64 {
	if s == "" {
		return 0
	}
	// Cheap heuristic: ~4 chars per token for Latin text; utf8-aware minimum 1 per rune.
	r := int64(utf8.RuneCountInString(s))
	ch := int64(len(s))
	est := ch / 4
	if est < r {
		return r
	}
	return est
}

func cloneSlice(in []*llmv1.Message) []*llmv1.Message {
	out := make([]*llmv1.Message, len(in))
	for i, m := range in {
		out[i] = cloneMessage(m)
	}
	return out
}

func cloneMessage(m *llmv1.Message) *llmv1.Message {
	if m == nil {
		return nil
	}
	cp := &llmv1.Message{
		Role:             m.GetRole(),
		Content:          m.GetContent(),
		ThinkingContent:  m.GetThinkingContent(),
		ActivityHeadline: m.GetActivityHeadline(),
		Id:               m.GetId(),
		ToolCallId:       m.GetToolCallId(),
		ToolCalls:        nil,
	}
	for _, tc := range m.GetToolCalls() {
		if tc == nil {
			continue
		}
		cp.ToolCalls = append(cp.ToolCalls, &llmv1.ToolCall{
			Id:               tc.GetId(),
			Name:             tc.GetName(),
			ArgumentsJson:    tc.GetArgumentsJson(),
			ThoughtSignature: append([]byte(nil), tc.GetThoughtSignature()...),
		})
	}
	return cp
}
