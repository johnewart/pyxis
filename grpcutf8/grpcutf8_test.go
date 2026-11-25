package grpcutf8

import (
	"context"
	"testing"
	"unicode/utf8"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
)

// A JPEG's first bytes: what a tool reading a binary fixture returns.
const jpeg = "\xff\xd8\xff\xe0\x00\x10JFIF"

func TestSanitizeMakesAMarshalableMessage(t *testing.T) {
	bad := "boom: " + jpeg
	frame := &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_ToolExecutionFinished{
		ToolExecutionFinished: &llmv1.EvToolExecutionFinished{Name: "read_code", ErrorMessage: &bad},
	}}
	// The incident: gRPC cannot send this.
	if _, err := proto.Marshal(frame); err == nil {
		t.Fatal("expected the invalid frame not to marshal")
	}
	if !Sanitize(frame) {
		t.Fatal("Sanitize reported no change")
	}
	if _, err := proto.Marshal(frame); err != nil {
		t.Fatalf("after Sanitize: %v", err)
	}
	if got := frame.GetToolExecutionFinished().GetErrorMessage(); !utf8.ValidString(got) || got[:6] != "boom: " {
		t.Fatalf("message = %q", got)
	}

	// Repeated and nested strings too; valid text is untouched.
	req := &llmv1.ChatStreamRequest{Messages: []*llmv1.Message{
		{Role: "tool", Content: "fine — →"},
		{Role: "tool", Content: jpeg},
	}}
	Sanitize(req)
	if _, err := proto.Marshal(req); err != nil {
		t.Fatalf("request: %v", err)
	}
	if req.GetMessages()[0].GetContent() != "fine — →" {
		t.Fatal("valid text changed")
	}
	if Sanitize(req) {
		t.Fatal("an already valid message was changed")
	}
}

// fakeStream marshals what it is sent, exactly as a gRPC transport does.
type fakeStream struct {
	grpc.ServerStream
	err error
}

func (f *fakeStream) SendMsg(m any) error {
	_, f.err = proto.Marshal(m.(proto.Message))
	return f.err
}
func (f *fakeStream) Context() context.Context { return context.Background() }

func TestTheServerInterceptorSendsWhatWouldHaveFailed(t *testing.T) {
	bad := jpeg
	frame := &llmv1.ConversationStreamFrame{Frame: &llmv1.ConversationStreamFrame_ToolExecutionFinished{
		ToolExecutionFinished: &llmv1.EvToolExecutionFinished{ErrorMessage: &bad},
	}}
	fs := &fakeStream{}
	err := StreamServerInterceptor(nil, fs, &grpc.StreamServerInfo{}, func(_ any, ss grpc.ServerStream) error {
		return ss.SendMsg(frame)
	})
	if err != nil || fs.err != nil {
		t.Fatalf("send failed: %v / %v", err, fs.err)
	}
}
