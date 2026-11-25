package mcpproxy

import (
	"strings"
	"sync"
)

// stderrTailMax bounds how much of an upstream MCP server's stderr we keep: a
// long-running server can log indefinitely, and only the tail explains a crash.
const stderrTailMax = 8 << 10

// stderrTail keeps the last stderrTailMax bytes an MCP server wrote to stderr.
// exec copies into it from its own goroutine, so access is locked.
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

func (s *stderrTail) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	if over := len(s.buf) - stderrTailMax; over > 0 {
		s.buf = append(s.buf[:0], s.buf[over:]...)
	}
	return len(p), nil
}

func (s *stderrTail) String() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(string(s.buf))
}
