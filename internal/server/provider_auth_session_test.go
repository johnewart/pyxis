package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeAuthOutputStream captures a server-streaming response in-process.
type fakeAuthOutputStream struct {
	grpc.ServerStream
	ctx    context.Context
	frames []*llmv1.ProviderAuthCommandOutput
}

func (f *fakeAuthOutputStream) Context() context.Context { return f.ctx }
func (f *fakeAuthOutputStream) Send(o *llmv1.ProviderAuthCommandOutput) error {
	f.frames = append(f.frames, o)
	return nil
}

func (f *fakeAuthOutputStream) output() string {
	var sb strings.Builder
	for _, fr := range f.frames {
		sb.Write(fr.GetOutput())
	}
	return sb.String()
}

// writeFakeLoginCLI makes a script that prints a login URL immediately, then
// exits — standing in for `claude login`'s opening output.
func writeFakeLoginCLI(t *testing.T, banner string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI fixture is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '"+banner+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestProviderAuthSession_ReplayPreservesOutputPrintedBeforeStreamAttached(t *testing.T) {
	// The whole reason this session design buffers: the CLI prints the login
	// URL immediately, which is before a browser can realistically attach its
	// output stream. Without replay those bytes are lost and the user stares at
	// an empty terminal with no way to finish signing in.
	const banner = "Visit https://claude.ai/login?code=ABC123 to continue"
	bin := writeFakeLoginCLI(t, banner)
	srv := &LlmServer{}

	start, err := srv.StartProviderAuthSession(context.Background(),
		&llmv1.StartProviderAuthSessionRequest{
			ProviderType: llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE_V2,
			Binary:       &bin,
		})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_, _ = srv.EndProviderAuthSession(context.Background(),
			&llmv1.EndProviderAuthSessionRequest{SessionId: start.GetSessionId()})
	})

	// Attach deliberately late, after the process has printed and exited.
	time.Sleep(300 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream := &fakeAuthOutputStream{ctx: ctx}
	if err := srv.StreamProviderAuthOutput(
		&llmv1.StreamProviderAuthOutputRequest{SessionId: start.GetSessionId(), Replay: true},
		stream,
	); err != nil {
		t.Fatalf("stream: %v", err)
	}

	if got := stream.output(); !strings.Contains(got, "claude.ai/login?code=ABC123") {
		t.Fatalf("replayed output lost the login URL; got %q", got)
	}
}

func TestProviderAuthSession_StreamReportsExitCode(t *testing.T) {
	bin := writeFakeLoginCLI(t, "done")
	srv := &LlmServer{}
	start, err := srv.StartProviderAuthSession(context.Background(),
		&llmv1.StartProviderAuthSessionRequest{
			ProviderType: llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE_V2,
			Binary:       &bin,
		})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream := &fakeAuthOutputStream{ctx: ctx}
	if err := srv.StreamProviderAuthOutput(
		&llmv1.StreamProviderAuthOutputRequest{SessionId: start.GetSessionId(), Replay: true},
		stream,
	); err != nil {
		t.Fatalf("stream: %v", err)
	}
	// A terminating exit frame is how the client knows to stop rendering a
	// terminal and re-run the connection check.
	last := stream.frames[len(stream.frames)-1]
	if _, ok := last.GetFrame().(*llmv1.ProviderAuthCommandOutput_ExitCode); !ok {
		t.Fatalf("stream did not end with an exit_code frame, got %T", last.GetFrame())
	}
}

func TestProviderAuthSession_UnknownSessionIsNotFound(t *testing.T) {
	srv := &LlmServer{}
	_, err := srv.SendProviderAuthInput(context.Background(),
		&llmv1.SendProviderAuthInputRequest{
			SessionId: "nope",
			Frame:     &llmv1.SendProviderAuthInputRequest_Stdin{Stdin: []byte("x")},
		})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %s, want NotFound", status.Code(err))
	}
}

func TestProviderAuthSession_EndIsIdempotentlyRemoved(t *testing.T) {
	bin := writeFakeLoginCLI(t, "hi")
	srv := &LlmServer{}
	start, err := srv.StartProviderAuthSession(context.Background(),
		&llmv1.StartProviderAuthSessionRequest{
			ProviderType: llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE_V2,
			Binary:       &bin,
		})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := srv.EndProviderAuthSession(context.Background(),
		&llmv1.EndProviderAuthSessionRequest{SessionId: start.GetSessionId()}); err != nil {
		t.Fatalf("end: %v", err)
	}
	// Ending must actually forget the session, or the registry grows forever.
	_, err = srv.EndProviderAuthSession(context.Background(),
		&llmv1.EndProviderAuthSessionRequest{SessionId: start.GetSessionId()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("second end: code = %s, want NotFound", status.Code(err))
	}
}
