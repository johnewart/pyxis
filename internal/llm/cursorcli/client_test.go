package cursorclillm

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

func TestClient_baseArgs_readOnlyAddsAskMode(t *testing.T) {
	c := &Client{cfg: Config{Mode: "agent"}}
	readOnly := true
	opt := &llmv1.CallOptions{ReadOnly: &readOnly}
	args, err := c.baseArgs(context.Background(), opt, "json", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !containsArgPair(args, "--mode", "ask") {
		t.Fatalf("read_only=true should add --mode ask, got %v", args)
	}

	c2 := &Client{cfg: Config{Mode: "agent"}}
	args2, err := c2.baseArgs(context.Background(), nil, "json", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if containsArgPair(args2, "--mode", "ask") {
		t.Fatalf("read_only=false should not force ask mode, got %v", args2)
	}
}

func containsArgPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
