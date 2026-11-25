package mcpproxy

import (
	"strings"
	"testing"
)

func TestStderrTail_keepsOnlyTheTail(t *testing.T) {
	var s stderrTail
	_, _ = s.Write([]byte(strings.Repeat("a", stderrTailMax)))
	_, _ = s.Write([]byte("boom: server crashed"))
	got := s.String()
	if len(got) > stderrTailMax {
		t.Fatalf("tail exceeds bound: %d > %d", len(got), stderrTailMax)
	}
	if !strings.HasSuffix(got, "boom: server crashed") {
		t.Fatalf("latest stderr lost; tail ends with %q", got[len(got)-30:])
	}
	var nilTail *stderrTail
	if nilTail.String() != "" {
		t.Fatal("nil tail should read as empty")
	}
}
