package cursorclillm

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadStreamJSONLine_acceptsMultiMegabyteLine(t *testing.T) {
	payload := strings.Repeat("x", 2*1024*1024)
	raw := `{"type":"assistant","message":{"content":"` + payload + `"}}` + "\n"
	r := bufio.NewReader(strings.NewReader(raw))
	line, err := readStreamJSONLine(r)
	if err != nil {
		t.Fatalf("readStreamJSONLine: %v", err)
	}
	if len(line) <= len(payload) {
		t.Fatalf("expected line longer than payload, got %d", len(line))
	}
}

func TestReadStreamJSONLine_rejectsOversizeLine(t *testing.T) {
	over := streamJSONLineMaxBytes + 1
	raw := strings.Repeat("a", over) + "\n"
	r := bufio.NewReader(strings.NewReader(raw))
	_, err := readStreamJSONLine(r)
	if err == nil {
		t.Fatal("expected error for oversize line")
	}
}
