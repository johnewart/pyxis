// Package toolcapture provides a context-carried sink that CLI-backed LLM providers
// (Claude Code, Cursor) can use to record tool call / tool result messages observed
// during their internal agentic loops, so the grpc server can persist them to SQLite.
package toolcapture

import (
	"context"
	"sync"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

type sinkKey struct{}

// Sink collects *llmv1.Message values (role "assistant" with ToolCalls, or role "tool")
// produced during a provider's internal tool loop.
type Sink struct {
	mu   sync.Mutex
	msgs []*llmv1.Message
}

// Add appends messages to the sink. Nil messages are ignored.
func (s *Sink) Add(msgs ...*llmv1.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range msgs {
		if m != nil {
			s.msgs = append(s.msgs, m)
		}
	}
}

// Take returns and clears all accumulated messages.
func (s *Sink) Take() []*llmv1.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.msgs
	s.msgs = nil
	return out
}

// FromContext returns the Sink stored in ctx, or nil.
func FromContext(ctx context.Context) *Sink {
	v, _ := ctx.Value(sinkKey{}).(*Sink)
	return v
}

// NewContext returns a copy of ctx carrying sink.
func NewContext(ctx context.Context, sink *Sink) context.Context {
	return context.WithValue(ctx, sinkKey{}, sink)
}
