package server

import (
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/protobuf/proto"
)

func TestCallOptions_ReadOnlyRoundTrip(t *testing.T) {
	readOnly := true
	in := &llmv1.CallOptions{
		Model:    "test-model",
		ReadOnly: &readOnly,
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out := &llmv1.CallOptions{}
	if err := proto.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
	if !out.GetReadOnly() {
		t.Fatal("read_only not preserved across proto round-trip")
	}
}
