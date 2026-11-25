package bedrockllm

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestToolResultContentBlock_jsonObjectUsesJsonBlock(t *testing.T) {
	blk, err := toolResultContentBlock("tc1", `{"ok":true}`)
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := blk.(*types.ContentBlockMemberToolResult)
	if !ok || tr == nil {
		t.Fatalf("expected *ContentBlockMemberToolResult, got %T", blk)
	}
	if len(tr.Value.Content) != 1 {
		t.Fatalf("content blocks: %d", len(tr.Value.Content))
	}
	if _, ok := tr.Value.Content[0].(*types.ToolResultContentBlockMemberJson); !ok {
		t.Fatalf("expected JSON tool result block, got %T", tr.Value.Content[0])
	}
}

func TestToolResultContentBlock_jsonArrayUsesTextBlock(t *testing.T) {
	blk, err := toolResultContentBlock("tc1", `[]`)
	if err != nil {
		t.Fatal(err)
	}
	tr := blk.(*types.ContentBlockMemberToolResult)
	if len(tr.Value.Content) != 1 {
		t.Fatalf("content blocks: %d", len(tr.Value.Content))
	}
	tb, ok := tr.Value.Content[0].(*types.ToolResultContentBlockMemberText)
	if !ok {
		t.Fatalf("expected text tool result block for JSON array, got %T", tr.Value.Content[0])
	}
	if tb.Value != `[]` {
		t.Fatalf("text body: %q", tb.Value)
	}
}
