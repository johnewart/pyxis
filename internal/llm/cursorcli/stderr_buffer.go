package cursorclillm

import (
	"bytes"
	"strings"
	"sync"
)

// stderrBuffer collects CLI stderr. exec copies into cmd.Stderr from its own
// goroutine until the process exits, so ChatStream (which builds error messages
// before Wait) must read it under a lock; a bare bytes.Buffer would race.
type stderrBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *stderrBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// suffix returns ": <stderr>" for appending to an error, or "" when empty.
func (b *stderrBuffer) suffix() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := strings.TrimSpace(b.buf.String()); s != "" {
		return ": " + s
	}
	return ""
}
